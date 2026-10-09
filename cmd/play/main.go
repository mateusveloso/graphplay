// Command play lets the graph play a text adventure from the terminal.
//
//	play run                         start a game; pauses only when the graph needs a human
//	play run -world cellar -thread t1
//	play resume <thread> -command "go north"
//	play resume <thread> -stop
//	play resume <thread>             continue a run that stopped on an error
//	play diagram                     print the graph as Mermaid
//	play baseline -mode llm|decision [-world caverns]
//	                                 play the same world without the graph
//	play serve [-addr 127.0.0.1:8080]
//	                                 watch the graph play in a browser
//
// Flags may come before or after the positional argument.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/mateusveloso/graphplay/graph"
	"github.com/mateusveloso/graphplay/internal/baseline"
	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/llm"
	"github.com/mateusveloso/graphplay/internal/metrics"
	"github.com/mateusveloso/graphplay/internal/player"
	"github.com/mateusveloso/graphplay/internal/world"
)

func main() {
	if err := runWithSignals(); err != nil {
		fmt.Fprintln(os.Stderr, "play:", err)
		os.Exit(1)
	}
}

// runWithSignals owns the context so that main can exit without skipping deferred work.
func runWithSignals() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdout)
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: play <run|resume|diagram|baseline|serve> [flags]")
	}
	cfg := player.FromEnv()
	if err := cfg.Validate(); err != nil {
		return err
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "run":
		return cmdRun(ctx, cfg, rest, out)
	case "resume":
		return cmdResume(ctx, cfg, rest, out)
	case "diagram":
		return cmdDiagram(cfg, rest, out)
	case "baseline":
		return cmdBaseline(ctx, cfg, rest, out)
	case "serve":
		return cmdServe(ctx, cfg, rest, out)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// app wires real dependencies. Tests build the same graph with fakes.
func app(cfg player.Config, meter *metrics.Meter) (*graph.Runner[player.State], error) {
	return appWith(cfg, meter)
}

// generative picks the provider of the two generative roles from Config.
func generative(cfg player.Config, meter *metrics.Meter) (llm.Models, error) {
	switch cfg.Provider {
	case player.ProviderDeepSeek:
		if cfg.DeepSeekAPIKey == "" {
			return llm.Models{}, errors.New("DEEPSEEK_API_KEY is not set")
		}
		return llm.Models{
			Small: llm.NewDeepSeek(cfg.DeepSeekAPIKey, cfg.ModelProposer, meter),
			Large: llm.NewDeepSeek(cfg.DeepSeekAPIKey, cfg.ModelSolver, meter),
		}, nil
	case player.ProviderAnthropic:
		return llm.Models{
			Small: llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.ModelProposer, meter),
			Large: llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.ModelSolver, meter),
		}, nil
	}
	return llm.Models{}, fmt.Errorf("unknown provider %q", cfg.Provider)
}

// parseInterspersed parses flags wherever they appear and returns the positional
// arguments. The standard flag package stops at the first non-flag, which makes
// "play resume <thread> -stop" fail for no good reason.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func cmdRun(ctx context.Context, cfg player.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	thread := fs.String("thread", "", "thread id to use (default: random)")
	fs.StringVar(&cfg.World, "world", cfg.World, "embedded world to play: "+strings.Join(world.Available(), ", "))
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return errors.New("usage: play run [-world id] [-thread id]")
	}
	if *thread == "" {
		*thread = rand.Text()[:12]
	}
	meter := metrics.New()
	r, err := app(cfg, meter)
	if err != nil {
		return err
	}
	cp, err := r.Start(ctx, *thread, player.State{World: cfg.World})
	if err != nil {
		return err
	}
	return report(out, cp, meter)
}

func cmdResume(ctx context.Context, cfg player.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	command := fs.String("command", "", `a game command for the player to execute, e.g. "go north"`)
	stop := fs.Bool("stop", false, "end the game here and write the report")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 || (*stop && *command != "") {
		return errors.New(`usage: play resume <thread> [-command "go north" | -stop]`)
	}
	meter := metrics.New()
	r, err := app(cfg, meter)
	if err != nil {
		return err
	}
	var cp *graph.Checkpoint[player.State]
	if *stop || *command != "" {
		cp, err = r.Resume(ctx, positional[0], player.Decision{Command: *command, Stop: *stop})
	} else {
		cp, err = r.Continue(ctx, positional[0]) // no decision given: retry where it broke
	}
	if err != nil {
		return err
	}
	return report(out, cp, meter)
}

// cmdBaseline plays without the graph: one model, one loop, same world, same mercy.
func cmdBaseline(ctx context.Context, cfg player.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	mode := fs.String("mode", "", "llm (one large generative model) or decision (one decision model)")
	thread := fs.String("thread", "", "run id (default: random)")
	fs.StringVar(&cfg.World, "world", cfg.World, "embedded world to play")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *thread == "" {
		*thread = rand.Text()[:12]
	}
	w, err := world.Load(cfg.World)
	if err != nil {
		return err
	}
	lim := baseline.Limits{MaxTurns: cfg.MaxTurns, MaxDeaths: cfg.MaxDeaths}
	meter := metrics.New()
	var res baseline.Result
	switch baseline.Mode(*mode) {
	case baseline.ModeLLM:
		models, err := generative(cfg, meter)
		if err != nil {
			return err
		}
		res, err = baseline.PlayLLM(ctx, w, models.Large, lim)
		if err != nil {
			return err
		}
	case baseline.ModeDecision:
		if cfg.TypeSafeAPIKey == "" {
			return errors.New("TYPESAFE_API_KEY is not set")
		}
		res, err = baseline.PlayDecision(ctx, w, jev.NewClient(cfg.TypeSafeAPIKey, meter), lim)
		if err != nil {
			return err
		}
	default:
		return errors.New("usage: play baseline -mode llm|decision [-world id] [-thread id]")
	}
	report, err := res.Report()
	if err != nil {
		return err
	}
	report += usageMarkdown(meter)
	dir := filepath.Join(cfg.StateDir, "baselines", fmt.Sprintf("%s-%s-%s", cfg.World, *mode, *thread))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "game.md")
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return err
	}
	verdict := "not finished"
	if res.Won {
		verdict = "won"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nbaseline %s on %s: %s in %d turns, %d deaths\n", *mode, cfg.World, verdict, res.Turns, res.Deaths)
	b.WriteString(usageText(meter))
	fmt.Fprintf(&b, "report: %s\n", path)
	_, err = io.WriteString(out, b.String())
	return err
}

// usageText is the meter as a terminal table.
func usageText(meter *metrics.Meter) string {
	rows := meter.Rows()
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("model usage:\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-18s %3d calls  %7d in  %6d out  %6.1fs", r.Model, r.Calls, r.InputTokens, r.OutputTokens, r.Duration.Seconds())
		if r.Errors > 0 {
			fmt.Fprintf(&b, "  (%d failed)", r.Errors)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// usageMarkdown is the meter as a section appended to a report.
func usageMarkdown(meter *metrics.Meter) string {
	rows := meter.Rows()
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Model usage\n\n| model | calls | input tokens | output tokens | time |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %.1fs |\n", r.Model, r.Calls, r.InputTokens, r.OutputTokens, r.Duration.Seconds())
	}
	return b.String()
}

func cmdDiagram(cfg player.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("diagram", flag.ContinueOnError)
	path := fs.String("out", "docs/graph.mmd", "file to write (empty: stdout only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The drawing only needs the shape: nil dependencies are never called.
	g := player.Build(world.World{}, llm.Models{}, jev.None{}, cfg)
	if err := g.Validate(); err != nil {
		return err
	}
	mermaid := g.Mermaid()
	if *path != "" {
		if err := os.WriteFile(*path, []byte(mermaid), 0o644); err != nil {
			return err
		}
	}
	_, err := io.WriteString(out, mermaid)
	return err
}

// report prints what the user needs next: the screen with resume commands, or the path of
// the written game report and its layer summary.
func report(out io.Writer, cp *graph.Checkpoint[player.State], meter *metrics.Meter) error {
	var b strings.Builder
	if !cp.Paused() {
		s := cp.State
		switch {
		case s.Won:
			fmt.Fprintf(&b, "\nwon in %d turns, %d deaths\n", s.Turn, s.Deaths)
		case s.Stopped:
			fmt.Fprintf(&b, "\nstopped after %d turns\n", s.Turn)
		default:
			fmt.Fprintf(&b, "\nnot finished after %d turns\n", s.Turn)
		}
		for _, row := range s.Summary() {
			fmt.Fprintf(&b, "  %-15s %d\n", row.Layer, row.Turns)
		}
		b.WriteString(usageText(meter))
		if s.OutputPath != "" {
			// The report knows who decided; the meter knows what it cost. Put them together.
			if f, err := os.OpenFile(s.OutputPath, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				_, _ = io.WriteString(f, usageMarkdown(meter))
				_ = f.Close()
			}
		}
		fmt.Fprintf(&b, "report: %s\n", s.OutputPath)
		_, err := io.WriteString(out, b.String())
		return err
	}
	screen, ok := cp.Pause.Payload.(player.Screen)
	if !ok {
		return fmt.Errorf("unexpected pause payload %T", cp.Pause.Payload)
	}
	body, err := json.MarshalIndent(screen, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(&b, "\n=== the graph needs you (%s) ===\n%s\n", screen.Why, body)
	b.WriteString(usageText(meter))
	fmt.Fprintf(&b, "\nthread:  %s\n", cp.Thread)
	fmt.Fprintf(&b, "command: play resume %s -command \"go north\"\n", cp.Thread)
	fmt.Fprintf(&b, "stop:    play resume %s -stop\n", cp.Thread)
	_, err = io.WriteString(out, b.String())
	return err
}
