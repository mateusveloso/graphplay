// Package baseline plays the same worlds without the graph, to measure what the structure
// buys. Two players: a large language model alone, with the whole transcript as memory, and
// a decision model alone, choosing among the legal commands each turn. Both get the same
// mercy the graph gets (a death reloads the previous state) and the same turn budget.
package baseline

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/mateusveloso/graphplay/internal/jev"
	"github.com/mateusveloso/graphplay/internal/llm"
	"github.com/mateusveloso/graphplay/internal/player"
	"github.com/mateusveloso/graphplay/internal/world"
)

// Mode names the player.
type Mode string

// The two baselines.
const (
	ModeLLM      Mode = "llm"      // one large generative model, no code around it
	ModeDecision Mode = "decision" // one decision model over the legal commands, no memory
)

// Limits shared with the graph so the comparison is fair.
type Limits struct {
	MaxTurns  int
	MaxDeaths int
}

// Result is what a baseline game produced.
type Result struct {
	World  string         `json:"world"`
	Mode   Mode           `json:"mode"`
	Turns  int            `json:"turns"`
	Deaths int            `json:"deaths"`
	Won    bool           `json:"won"`
	Ledger []player.Entry `json:"ledger"`
}

// Move is the generative baseline's typed output.
type Move struct {
	Thinking string `json:"thinking" jsonschema_description:"One or two sentences on why."`
	Command  string `json:"command" jsonschema_description:"go <exit> | take <item> | answer <text> | look"`
}

const llmSystem = `You are playing a text adventure. Each turn you see the room, the exits,
the visible items, your inventory and the full transcript so far. Reply with exactly one
command: "go <exit>", "take <item>", "answer <text>" (when a riddle is posed) or "look".
If you die, the game reloads the moment before your command; learn from it. Find the chest.
Commands keep their English verbs; exits, items and answers are in the language of the game.`

// PlayLLM lets a single generative model play, remembering everything it saw.
func PlayLLM(ctx context.Context, w world.World, model llm.Model, lim Limits) (Result, error) {
	res := Result{World: w.ID, Mode: ModeLLM}
	game := world.New(w)
	var transcript []string
	for res.Turns < lim.MaxTurns && res.Deaths < lim.MaxDeaths {
		obs := game.Observe()
		if obs.Won {
			res.Won = true
			break
		}
		var b strings.Builder
		if len(transcript) > 0 {
			b.WriteString("Transcript so far:\n" + strings.Join(transcript, "\n") + "\n\n")
		}
		b.WriteString("Now:\n" + situation(obs))
		var move Move
		if err := model.Ask(ctx, llmSystem, b.String(), &move); err != nil {
			return res, err
		}
		entry := step(&res, game, w, obs, move.Command, player.LayerLarge, move.Thinking)
		transcript = append(transcript, fmt.Sprintf("[%s] > %s  => %s", obs.Name, entry.Command, cmp(entry.Outcome, "(moved)")))
		if entry.Died {
			transcript = append(transcript, "(you died and were reloaded to before that command)")
		}
	}
	return res, nil
}

// PlayDecision lets a decision model pick, each turn, among the commands that are legal
// right now. It cannot write, so it can never answer a riddle; that is the honest limit.
func PlayDecision(ctx context.Context, w world.World, decider jev.Decider, lim Limits) (Result, error) {
	res := Result{World: w.ID, Mode: ModeDecision}
	game := world.New(w)
	var recent []string
	for res.Turns < lim.MaxTurns && res.Deaths < lim.MaxDeaths {
		obs := game.Observe()
		if obs.Won {
			res.Won = true
			break
		}
		options := map[string]string{"look": "Look around the room."}
		for _, dir := range obs.Exits {
			options["go "+dir] = "Walk " + dir + "."
		}
		for _, it := range obs.Items {
			options["take "+it] = "Pick up the " + it + "."
		}
		state := situation(obs)
		if len(recent) > 0 {
			state += "\nRecent moves:\n" + strings.Join(recent, "\n")
		}
		answers, err := decider.Decide(ctx, state, map[string]jev.Question{
			"next": jev.Choice("Which command brings the player closer to finding the chest?", options),
		})
		if err != nil {
			return res, err
		}
		pick := answers["next"]
		if _, ok := options[pick.Choice]; !ok {
			pick.Choice = "look"
		}
		entry := step(&res, game, w, obs, pick.Choice, player.LayerDecision, fmt.Sprintf("confidence %.2f", pick.Confidence))
		recent = append(recent, fmt.Sprintf("[%s] %s => %s", obs.Name, entry.Command, cmp(entry.Outcome, "(moved)")))
		if len(recent) > 8 {
			recent = recent[1:]
		}
	}
	return res, nil
}

// step applies one command with the same death-reloads mercy the graph has.
func step(res *Result, game *world.Game, w world.World, before world.Observation, command string, layer player.Layer, note string) player.Entry {
	res.Turns++
	entry := player.Entry{Turn: res.Turns, Room: before.Room, Command: command, Layer: layer, Note: note}
	cmd, err := world.Parse(command)
	if err != nil {
		entry.Outcome = err.Error()
		res.Ledger = append(res.Ledger, entry)
		return entry
	}
	entry.Command = cmd.String()
	after := game.Do(cmd)
	entry.Outcome = after.Message
	switch {
	case after.Dead:
		res.Deaths++
		entry.Died = true
		log := game.Log()
		*game = *world.Replay(w, log[:len(log)-1])
	case after.Won, after.Room != before.Room, len(after.Inventory) > len(before.Inventory),
		before.Riddle != "" && after.Riddle == "":
		entry.Progress = true
	}
	res.Ledger = append(res.Ledger, entry)
	return entry
}

func situation(obs world.Observation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are in: %s\n%s\n\nExits: %s\nVisible items: %s\nInventory: %s\n",
		obs.Name, obs.Description, strings.Join(obs.Exits, ", "), cmp(strings.Join(obs.Items, ", "), "none"),
		cmp(strings.Join(obs.Inventory, ", "), "empty"))
	if obs.Message != "" {
		fmt.Fprintf(&b, "Last outcome: %s\n", obs.Message)
	}
	if obs.Riddle != "" {
		fmt.Fprintf(&b, "A riddle is posed: %s\n", obs.Riddle)
	}
	return b.String()
}

func cmp(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

//go:embed report.md.tmpl
var reportFS embed.FS

var reportTmpl = template.Must(template.ParseFS(reportFS, "report.md.tmpl"))

// Report renders the same shape of report the graph writes, so the two read side by side.
func (r Result) Report() (string, error) {
	var b bytes.Buffer
	if err := reportTmpl.Execute(&b, r); err != nil {
		return "", err
	}
	return b.String(), nil
}
