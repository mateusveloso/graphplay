package player

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mateusveloso/graphplay/graph"
	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/llm"
	"github.com/mateusveloso/graphplay/internal/world"
)

// Nodes are constructors: they take their dependencies once and return the function the
// graph calls. Code nodes never see a model; model nodes never touch the engine.

// observe rebuilds the game from the command log and refreshes what the player sees.
func observe(w world.World) graph.Node[State] {
	return func(_ context.Context, s *State) error {
		s.Obs = world.Replay(w, s.Commands).Observe()
		s.here()
		s.Chosen, s.Candidates = nil, nil
		return nil
	}
}

var kinds = map[string]string{
	"ordinary": "A plain location: nothing here threatens or blocks the player.",
	"hazard":   "Something here, or just beyond an exit, kills an unprepared player.",
	"puzzle":   "Progress is blocked by something to solve or answer.",
	"dead_end": "Nothing useful here and only the way back.",
	"goal":     "The objective of the game.",
}

// assess asks the decision model what a careful player would infer from the prose. One
// choice for the kind of place, and per exit two yes/no statements that code combines:
// is a danger described that way, and does the player carry what it takes. Splitting the
// judgment is what makes the probabilities usable; "is it lethal now?" asks the model to
// weigh two things at once and it answers somewhere in the middle.
func assess(decider jev.Decider) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		k := s.here()
		k.Assessed, k.AssessedWith = true, slices.Clone(s.Obs.Inventory)
		k.Danger = make(map[string]float64, len(s.Obs.Exits))
		k.Protected = make(map[string]float64, len(s.Obs.Exits))
		k.Risk = make(map[string]float64, len(s.Obs.Exits))

		state, err := assessPrompt.render("state", s)
		if err != nil {
			return err
		}
		questions := map[string]jev.Question{"kind": jev.Choice("What kind of place is this?", kinds)}
		for _, dir := range s.Obs.Exits {
			questions["danger_"+dir] = jev.Noul(
				fmt.Sprintf("The text describes something in the %s direction that would kill an unprepared player (darkness, a pit, a creature, a drop).", dir),
				"The description points to a lethal danger that way.",
				"Nothing in the description suggests that way is dangerous.",
			)
			questions["protected_"+dir] = jev.Noul(
				fmt.Sprintf("The player's inventory contains what is needed to survive whatever lies %s (a light for darkness, a payment for a toll-taker).", dir),
				"The inventory listed covers the danger described that way.",
				"The inventory is empty or does not address that danger.",
			)
		}
		answers, err := decider.Decide(ctx, state, questions)
		if err != nil {
			return nil //nolint:nilerr // no decision: every exit stays at risk 0 and code explores
		}
		k.Kind = answers["kind"].Choice
		for _, dir := range s.Obs.Exits {
			danger, protected := probability(answers, "danger_"+dir), probability(answers, "protected_"+dir)
			k.Danger[dir], k.Protected[dir] = danger, protected
			k.Risk[dir] = danger * (1 - protected)
		}
		return nil
	}
}

// probability reads a noul answer; a missing one is maximum uncertainty, not a pass.
func probability(answers map[string]jev.Answer, key string) float64 {
	if a, ok := answers[key]; ok && a.Noul != nil {
		return *a.Noul
	}
	return 0.5
}

// cheapMove is the deterministic policy, in order: take what is visible; explore a safe
// unexplored exit here; walk the known map towards the nearest room that still has one;
// when the frontier is gone, look closely at rooms not yet looked at, nearest first. Every
// text-adventure player does these without thinking, so they cost nothing here either.
// When it returns nothing, the models earn their turn.
func cheapMove(cfg Config) graph.Node[State] {
	return func(_ context.Context, s *State) error {
		if len(s.Obs.Items) > 0 {
			s.Chosen = &Choice{Command: world.Take + " " + s.Obs.Items[0], Layer: LayerCode}
			return nil
		}
		if dir, ok := s.safeUnexploredExit(s.Obs.Room, cfg); ok {
			s.Chosen = &Choice{Command: world.Go + " " + dir, Layer: LayerCode}
			return nil
		}
		if dir, ok := s.stepTowards(func(room string) bool {
			_, ok := s.safeUnexploredExit(room, cfg)
			return ok
		}); ok {
			s.Chosen = &Choice{Command: world.Go + " " + dir, Layer: LayerCode, Note: "walking to the frontier"}
			return nil
		}
		if !s.here().Looked {
			s.Chosen = &Choice{Command: world.Look, Layer: LayerCode, Note: "frontier exhausted; looking closely"}
			return nil
		}
		if dir, ok := s.stepTowards(func(room string) bool { return !s.Map[room].Looked }); ok {
			s.Chosen = &Choice{Command: world.Go + " " + dir, Layer: LayerCode, Note: "walking to a room not yet looked at"}
		}
		return nil
	}
}

// propose asks the small model for candidate commands. Bounded output, cheap to get wrong:
// rank filters it and the engine ignores nonsense.
func propose(model llm.Model, cfg Config) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		view := proposeView{State: s, Unexplored: s.frontier(), Refused: s.refused(cfg)}
		user, err := proposePrompt.render("user", view)
		if err != nil {
			return err
		}
		var p Proposal
		if err := model.Ask(ctx, proposePrompt.system(), user, &p); err != nil {
			return err
		}
		s.Candidates = p.Commands
		return nil
	}
}

// rank keeps the legal candidates, then lets the decision model pick one. A single legal
// candidate needs no model; zero means the player is stuck.
func rank(decider jev.Decider) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		legal := slices.DeleteFunc(slices.Clone(s.Candidates), func(c string) bool { return !s.legal(c) })
		switch len(legal) {
		case 0:
			return nil
		case 1:
			s.Chosen = &Choice{Command: legal[0], Layer: LayerSmall, Note: "only legal proposal"}
			return nil
		}
		state, err := assessPrompt.render("state", s)
		if err != nil {
			return err
		}
		options := make(map[string]string, len(legal))
		for _, c := range legal {
			options[c] = "The player types: " + c
		}
		answers, err := decider.Decide(ctx, state, map[string]jev.Question{
			"next": jev.Choice("Which command is most likely to make progress towards the goal?", options),
		})
		var picked jev.Answer
		if err == nil {
			picked = answers["next"]
		}
		if !slices.Contains(legal, picked.Choice) {
			// No decision, or one outside the legal set: the proposer's own order stands.
			s.Chosen = &Choice{Command: legal[0], Layer: LayerSmall, Note: "decision model unavailable; first proposal"}
			return nil
		}
		s.Chosen = &Choice{
			Command: picked.Choice,
			Layer:   LayerDecision,
			Note:    fmt.Sprintf("confidence %.2f over %d proposals", picked.Confidence, len(legal)),
		}
		return nil
	}
}

// solve is the one place that needs real reading: a riddle. Large model, typed answer.
func solve(model llm.Model) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		user, err := solvePrompt.render("user", s)
		if err != nil {
			return err
		}
		var a RiddleAnswer
		if err := model.Ask(ctx, solvePrompt.system(), user, &a); err != nil {
			return err
		}
		s.Chosen = &Choice{Command: world.Answer + " " + strings.ToLower(strings.TrimSpace(a.Answer)), Layer: LayerLarge}
		return nil
	}
}

// act applies the chosen command to the engine and learns from the outcome. Death is not
// the end: the fatal command is remembered for this room and the log is rolled back one
// step, which is all a reload is when the engine is a replay.
func act(w world.World) graph.Node[State] {
	return func(_ context.Context, s *State) error {
		cmd, err := world.Parse(s.Chosen.Command)
		if err != nil {
			return fmt.Errorf("act: %w", err)
		}
		before := s.Obs
		s.Turn++
		s.Commands = append(s.Commands, cmd)
		after := world.Replay(w, s.Commands).Observe()
		entry := Entry{Turn: s.Turn, Room: before.Room, Command: cmd.String(), Layer: s.Chosen.Layer, Note: s.Chosen.Note, Outcome: after.Message}

		if after.Dead {
			s.Deaths++
			s.Commands = s.Commands[:len(s.Commands)-1]
			if s.Fatal == nil {
				s.Fatal = make(map[string][]Death)
			}
			s.Fatal[before.Room] = append(s.Fatal[before.Room], Death{Command: cmd.String(), Inventory: slices.Clone(before.Inventory)})
			entry.Died = true
			s.record(entry)
			return nil
		}

		switch {
		case after.Won:
			s.Won = true
			entry.Progress = true
		case cmd.Verb == world.Go && after.Room != before.Room:
			s.here().Exits[cmd.Arg] = after.Room // learn where the exit led
			s.Obs = after
			_, seen := s.Map[after.Room]
			s.here()
			entry.Progress = !seen
		case cmd.Verb == world.Take && len(after.Inventory) > len(before.Inventory):
			entry.Progress = true
		case cmd.Verb == world.Answer && before.Riddle != "" && after.Riddle == "":
			entry.Progress = true
		case cmd.Verb == world.Look:
			s.here().Looked = true
			if len(after.Exits) > len(before.Exits) {
				s.Obs = after // a revealed exit must reach the map before the next cheap move
				for _, dir := range after.Exits {
					if _, known := s.here().Exits[dir]; !known {
						s.here().Exits[dir] = ""
					}
				}
				entry.Progress = true
			}
		default:
			// Nothing changed: remember so neither code nor models repeat it here.
			if cmd.Verb != world.Go || after.Riddle == "" {
				s.Tried = appendTo(s.Tried, before.Room, cmd.String())
			}
		}
		if entry.Progress {
			s.LastProgress = s.Turn
		}
		s.record(entry)
		return nil
	}
}

// gate hands the controls to a human when the cheaper layers have run out of ideas.
func gate(ctx context.Context, s *State) error {
	screen := Screen{Obs: s.Obs, Turn: s.Turn, Deaths: s.Deaths, Why: s.whyStuck(), Summary: s.Summary()}
	if n := len(s.Ledger); n > 0 {
		screen.Recent = s.Ledger[max(0, n-5):]
	}
	decision, err := graph.Interrupt[Decision](ctx, screen)
	if err != nil {
		return err
	}
	if decision.Stop {
		s.Stopped = true
		return nil
	}
	if _, err := world.Parse(decision.Command); err != nil {
		return fmt.Errorf("gate: %w", err)
	}
	s.Chosen = &Choice{Command: decision.Command, Layer: LayerHuman}
	s.Deaths, s.LastProgress = 0, s.Turn // the human's turn resets the patience counters
	return nil
}

// finalize writes the game report next to the checkpoint.
func finalize(cfg Config) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		report, err := reportPrompt.render("report", s)
		if err != nil {
			return err
		}
		dir := filepath.Join(cfg.StateDir, "runs", graph.Thread(ctx))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, "game.md")
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			return err
		}
		s.OutputPath = path
		return nil
	}
}

// --- helpers on State that only the nodes need

func (s *State) safeUnexploredExit(room string, cfg Config) (string, bool) {
	k, ok := s.Map[room]
	if !ok {
		return "", false
	}
	for _, dir := range slices.Sorted(maps.Keys(k.Exits)) {
		if k.Exits[dir] == "" && k.Risk[dir] < cfg.RiskThreshold && !s.ruledOut(room, world.Go+" "+dir) {
			return dir, true
		}
	}
	return "", false
}

// stepTowards walks the known map (breadth-first) to the nearest room other than the
// current one that satisfies want, and returns the first step of that walk.
func (s *State) stepTowards(want func(room string) bool) (string, bool) {
	type hop struct{ room, first string }
	start := s.Obs.Room
	queue := []hop{{room: start}}
	seen := map[string]bool{start: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.room != start && want(cur.room) {
			return cur.first, true
		}
		k := s.Map[cur.room]
		for _, dir := range slices.Sorted(maps.Keys(k.Exits)) {
			next := k.Exits[dir]
			if next == "" || seen[next] {
				continue
			}
			seen[next] = true
			first := cur.first
			if cur.room == start {
				first = dir
			}
			queue = append(queue, hop{room: next, first: first})
		}
	}
	return "", false
}

// frontier lists unexplored exits anywhere as "room/direction", for the propose prompt.
func (s *State) frontier() []string {
	var out []string
	for _, room := range slices.Sorted(maps.Keys(s.Map)) {
		for _, dir := range slices.Sorted(maps.Keys(s.Map[room].Exits)) {
			if s.Map[room].Exits[dir] == "" {
				out = append(out, room+"/"+dir)
			}
		}
	}
	return out
}

// refused lists the unexplored exits code would not take, with the numbers behind it.
func (s *State) refused(cfg Config) []string {
	var out []string
	for _, room := range slices.Sorted(maps.Keys(s.Map)) {
		k := s.Map[room]
		for _, dir := range slices.Sorted(maps.Keys(k.Exits)) {
			if k.Exits[dir] == "" && k.Risk[dir] >= cfg.RiskThreshold {
				out = append(out, fmt.Sprintf("%s/%s (danger %.2f, protected %.2f)", room, dir, k.Danger[dir], k.Protected[dir]))
			}
		}
	}
	return out
}

// legal is the code filter on proposals: parseable, possible here, not already ruled out.
func (s *State) legal(command string) bool {
	cmd, err := world.Parse(command)
	if err != nil || s.ruledOut(s.Obs.Room, cmd.String()) {
		return false
	}
	switch cmd.Verb {
	case world.Go:
		return slices.Contains(s.Obs.Exits, cmd.Arg)
	case world.Take:
		return slices.Contains(s.Obs.Items, cmd.Arg)
	case world.Answer:
		return s.Obs.Riddle != ""
	}
	return true
}

func (s *State) whyStuck() string {
	switch {
	case s.Deaths > 0:
		return fmt.Sprintf("%d deaths", s.Deaths)
	case s.Obs.Riddle != "":
		return "the riddle resisted every attempt"
	default:
		return fmt.Sprintf("no progress for %d turns", s.Turn-s.LastProgress)
	}
}

func appendTo(m map[string][]string, key, value string) map[string][]string {
	if m == nil {
		m = make(map[string][]string)
	}
	if !slices.Contains(m[key], value) {
		m[key] = append(m[key], value)
	}
	return m
}
