package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mateusveloso/graphplay/graph"
	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/llm"
	"github.com/mateusveloso/graphplay/internal/metrics"
	"github.com/mateusveloso/graphplay/internal/player"
	"github.com/mateusveloso/graphplay/internal/world"
)

//go:embed ui/*.html
var ui embed.FS

// uiEvent is what the browser receives: the runner's event plus what the models cost so far.
type uiEvent struct {
	graph.Event[player.State]
	Usage []metrics.Row `json:"usage"`
	At    time.Time     `json:"at"`
}

// hub fans events out to every open browser tab and replays history to late joiners.
type hub struct {
	mu      sync.Mutex
	history [][]byte
	subs    map[chan []byte]struct{}
}

func newHub() *hub { return &hub{subs: make(map[chan []byte]struct{})} }

func (h *hub) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = nil
}

func (h *hub) publish(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, data)
	for ch := range h.subs {
		select {
		case ch <- data:
		default: // a slow tab drops events rather than slowing the game
		}
	}
}

func (h *hub) subscribe() (<-chan []byte, [][]byte, func()) {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	history := append([][]byte(nil), h.history...)
	h.mu.Unlock()
	return ch, history, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// server runs one game at a time and streams it.
type server struct {
	cfg    player.Config
	pace   time.Duration
	hub    *hub
	mu     sync.Mutex
	meter  *metrics.Meter
	runner *graph.Runner[player.State]
	thread string
	busy   bool
}

func cmdServe(ctx context.Context, cfg player.Config, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:8080", "address to listen on")
	pace := flags.Duration("pace", 0, "pause after every node so a person can follow, e.g. 1500ms")
	flags.StringVar(&cfg.World, "world", cfg.World, "default world")
	if _, err := parseInterspersed(flags, args); err != nil {
		return err
	}
	s := &server{cfg: cfg, pace: *pace, hub: newHub()}
	pages, err := fs.Sub(ui, "ui")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(pages))
	mux.HandleFunc("GET /graph.mmd", s.handleDiagram)
	mux.HandleFunc("GET /worlds", s.handleWorlds)
	mux.HandleFunc("GET /world", s.handleWorld)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /start", s.handleStart)
	mux.HandleFunc("POST /resume", s.handleResume)

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if _, err := fmt.Fprintf(out, "watching at http://%s  (world: %s)\n", *addr, cfg.World); err != nil {
		return err
	}
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *server) handleDiagram(w http.ResponseWriter, _ *http.Request) {
	g := player.Build(world.World{}, llm.Models{}, jev.None{}, s.cfg)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, g.Mermaid())
}

func (s *server) handleWorlds(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"worlds": world.Available(), "default": s.cfg.World})
}

// handleWorld returns a world's drawing geometry and nothing else: where each room sits on
// the grid and which directions point where. No names, no prose, no exits. The map the page
// draws is the player's own map, from the state; the fog is honest.
func (s *server) handleWorld(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		id = s.cfg.World
	}
	wd, err := world.Load(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	pos := make(map[string]world.Pos, len(wd.Rooms))
	for rid, room := range wd.Rooms {
		if room.Pos != nil {
			pos[rid] = *room.Pos
		}
	}
	writeJSON(w, map[string]any{
		"id": wd.ID, "start": wd.Start, "lang": wd.Lang, "pos": pos,
		"risk_threshold": s.cfg.RiskThreshold,
		"dirs": map[string][2]int{
			"north": {0, -1}, "south": {0, 1}, "east": {1, 0}, "west": {-1, 0}, "up": {1, -1}, "down": {1, 1},
			"norte": {0, -1}, "sul": {0, 1}, "leste": {1, 0}, "oeste": {-1, 0}, "cima": {1, -1}, "baixo": {1, 1},
		},
	})
}

// handleStart begins a new game in the background; events flow through the hub.
func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		http.Error(w, "a game is already running", http.StatusConflict)
		return
	}
	cfg := s.cfg
	if id := r.URL.Query().Get("world"); id != "" {
		cfg.World = id
	}
	meter := metrics.New()
	runner, err := appWith(cfg, meter,
		graph.WithObserver(func(e graph.Event[player.State]) {
			s.hub.publish(uiEvent{Event: e, Usage: meter.Rows(), At: time.Now()})
		}),
		graph.WithPace[player.State](s.pace),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.hub.reset()
	s.meter, s.runner, s.thread, s.busy = meter, runner, rand.Text()[:12], true
	go s.drive(func(ctx context.Context) (*graph.Checkpoint[player.State], error) {
		return runner.Start(ctx, s.thread, player.State{World: cfg.World})
	})
	writeJSON(w, map[string]string{"thread": s.thread, "world": cfg.World})
}

// handleResume answers the human gate of the running game.
func (s *server) handleResume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
		Stop    bool   `json:"stop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner == nil || s.busy {
		http.Error(w, "nothing is waiting for you", http.StatusConflict)
		return
	}
	s.busy = true
	runner, thread := s.runner, s.thread
	go s.drive(func(ctx context.Context) (*graph.Checkpoint[player.State], error) {
		return runner.Resume(ctx, thread, player.Decision{Command: req.Command, Stop: req.Stop})
	})
	writeJSON(w, map[string]string{"thread": thread})
}

// drive runs one segment of a game (start or resume) and clears the busy flag after it.
func (s *server) drive(segment func(context.Context) (*graph.Checkpoint[player.State], error)) {
	cp, err := segment(context.Background())
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
	if err != nil {
		s.hub.publish(map[string]any{"phase": "error", "error": err.Error(), "at": time.Now()})
		return
	}
	if cp.Done && cp.State.OutputPath != "" {
		if f, err := os.OpenFile(cp.State.OutputPath, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = io.WriteString(f, usageMarkdown(s.meter))
			_ = f.Close()
		}
	}
}

// handleEvents is the Server-Sent Events stream: history first, then live.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, history, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	send := func(data []byte) bool {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false // the tab went away
		}
		flusher.Flush()
		return true
	}
	for _, data := range history {
		if !send(data) {
			return
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			if !send(data) {
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// appWith is app with extra runner options; the plain CLI commands use app.
func appWith(cfg player.Config, meter *metrics.Meter, opts ...graph.Option[player.State]) (*graph.Runner[player.State], error) {
	w, err := world.Load(cfg.World)
	if err != nil {
		return nil, err
	}
	models, err := generative(cfg, meter)
	if err != nil {
		return nil, err
	}
	var decider jev.Decider = jev.None{}
	if cfg.TypeSafeAPIKey != "" {
		decider = jev.NewClient(cfg.TypeSafeAPIKey, meter)
	}
	store := graph.FileStore[player.State]{Dir: filepath.Join(cfg.StateDir, "runs")}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	opts = append([]graph.Option[player.State]{graph.WithLogger[player.State](logger)}, opts...)
	return graph.NewRunner(player.Build(w, models, decider, cfg), store, opts...)
}
