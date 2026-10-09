// Command triage runs the issue-triage graph from the terminal.
//
//	triage run owner/repo#123          start a thread; stops at the human gate
//	triage resume <thread> -approve    answer the gate
//	triage resume <thread> -reject "what to change"
//	triage diagram                     print the graph as Mermaid
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
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/mateusveloso/graph-issue-triage/graph"
	"github.com/mateusveloso/graph-issue-triage/internal/github"
	"github.com/mateusveloso/graph-issue-triage/internal/jev"
	"github.com/mateusveloso/graph-issue-triage/internal/llm"
	"github.com/mateusveloso/graph-issue-triage/internal/triage"
)

func main() {
	if err := runWithSignals(); err != nil {
		fmt.Fprintln(os.Stderr, "triage:", err)
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
		return errors.New("usage: triage <run|resume|diagram> [flags]")
	}
	cfg := triage.FromEnv()
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
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// app wires real dependencies. Tests build the same graph with fakes.
func app(cfg triage.Config) (*graph.Runner[triage.State], error) {
	models := llm.Models{
		Planner: llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.ModelPlanner),
		Writer:  llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.ModelWriter),
		Critic:  llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.ModelCritic),
	}
	var decider jev.Decider = jev.None{}
	if cfg.TypeSafeAPIKey != "" {
		decider = jev.NewClient(cfg.TypeSafeAPIKey)
	}
	g := triage.Build(models, github.NewClient(cfg.GitHubToken), decider, cfg)
	store := graph.FileStore[triage.State]{Dir: filepath.Join(cfg.StateDir, "runs")}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return graph.NewRunner(g, store, graph.WithLogger[triage.State](logger))
}

// parseInterspersed parses flags wherever they appear and returns the positional
// arguments. The standard flag package stops at the first non-flag, which makes
// "triage resume <thread> -approve" fail for no good reason.
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

func cmdRun(ctx context.Context, cfg triage.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	thread := fs.String("thread", "", "thread id to use (default: random)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: triage run owner/repo#123 [-thread id]")
	}
	if *thread == "" {
		*thread = rand.Text()[:12]
	}
	r, err := app(cfg)
	if err != nil {
		return err
	}
	cp, err := r.Start(ctx, *thread, triage.State{IssueRef: positional[0]})
	if err != nil {
		return err
	}
	return report(out, cp)
}

func cmdResume(ctx context.Context, cfg triage.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	approve := fs.Bool("approve", false, "approve the draft and write the report")
	reject := fs.String("reject", "", "reject the draft with this feedback")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 || *approve == (*reject != "") {
		return errors.New(`usage: triage resume <thread> (-approve | -reject "feedback")`)
	}
	r, err := app(cfg)
	if err != nil {
		return err
	}
	cp, err := r.Resume(ctx, positional[0], triage.Decision{Approve: *approve, Feedback: *reject})
	if err != nil {
		return err
	}
	return report(out, cp)
}

func cmdDiagram(cfg triage.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("diagram", flag.ContinueOnError)
	path := fs.String("out", "docs/graph.mmd", "file to write (empty: stdout only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The drawing only needs the shape: nil dependencies are never called.
	g := triage.Build(llm.Models{}, nil, jev.None{}, cfg)
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

// report prints what the user needs next: the gate payload with resume commands, or the
// path of the written triage.
func report(out io.Writer, cp *graph.Checkpoint[triage.State]) error {
	if !cp.Paused() {
		_, err := fmt.Fprintf(out, "written: %s\n", cp.State.OutputPath)
		return err
	}
	payload, ok := cp.Pause.Payload.(triage.GatePayload)
	if !ok {
		return fmt.Errorf("unexpected pause payload %T", cp.Pause.Payload)
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== draft for %s (critic rounds: %d) ===\n%s\n", payload.IssueRef, payload.CriticRounds, body)
	fmt.Fprintf(&b, "\nthread:  %s\n", cp.Thread)
	fmt.Fprintf(&b, "approve: triage resume %s -approve\n", cp.Thread)
	fmt.Fprintf(&b, "reject:  triage resume %s -reject \"what to change\"\n", cp.Thread)
	_, err = io.WriteString(out, b.String())
	return err
}
