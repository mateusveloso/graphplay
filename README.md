# graphplay

A Go program built around one idea: **the graph is the unit of engineering; a model is just a node.**

The graph plays a text adventure. Every turn it decides the next command at the cheapest layer
that can decide it, and writes down who decided. The final table is the point:

| layer | what it is | decides |
|---|---|---|
| **code** | deterministic, free | the map, the inventory, which exits are unexplored, the walk to the nearest frontier, what is legal, what killed us last time |
| **decision model** ([TypeSafe Jev](https://typesafe.ai)) | probabilities, cannot write | reading the room's prose: what kind of place is this, is walking through that exit lethal right now; which of several proposals to try |
| **small LLM** | cheap, bounded output | proposing candidate commands when code has no safe move left |
| **large LLM** | real reading | the riddle on the carved door, and nothing else |
| **human** | a gate, not a chat | after three deaths, eight idle turns or three wrong answers: the run pauses, saves, and waits |

A clean run of the bundled world ends with something like "22 turns: 17 code, 3 decision model,
1 small LLM, 1 large LLM". That number, not the game, is what this repository is about.

```mermaid
graph TD;
	__start__([start]):::first
	observe(observe)
	assess(assess)
	cheap_move(cheap_move)
	propose(propose)
	rank(rank)
	solve(solve)
	act(act)
	gate(gate)
	finalize(finalize)
	__end__([end]):::last
	__start__ --> observe;
	observe -.-> assess;
	observe -.-> cheap_move;
	observe -.-> finalize;
	observe -.-> gate;
	observe -.-> solve;
	assess --> cheap_move;
	cheap_move -.-> act;
	cheap_move -.-> propose;
	propose --> rank;
	rank -.-> act;
	rank -.-> gate;
	solve --> act;
	act --> observe;
	gate -.-> act;
	gate -.-> finalize;
	finalize --> __end__;
	classDef default fill:#f2f0ff,line-height:1.2
	classDef first fill-opacity:0
	classDef last fill:#bfb6fc
```

Dotted edges are decisions. Every one of them is a Go function reading typed state and a
threshold from [`Config`](internal/player/config.go). No prompt owns a loop bound.

## The runtime is under 500 lines, comments included, and it is yours

There is no LangGraph for Go, and this repo does not try to be one. [`graph/`](graph/) is the
whole control plane:

- `Graph[S]`: named nodes, fixed `Edge`s, and `Route`s whose targets are declared so the
  diagram can draw them and the runner can reject a router that wanders off.
- `Runner[S]`: executes nodes, checkpoints after every one, records how long each took.
  `Continue` retries a run from the node that failed.
- `Interrupt[T](ctx, payload)`: pauses the run with a payload; on resume the node runs again
  and receives a typed answer. A resume value of the wrong type is an error, not a panic.
- `Store[S]`: `FileStore` writes one JSON file per thread with an atomic rename (a save game
  you can `cat`); `MemoryStore` is for tests and round-trips through JSON on purpose.
- `Mermaid()`: deterministic, so `docs/graph.mmd` is committed and CI diffs it.

The package is generic over the state type and knows nothing about models or games.
[`graph/example_test.go`](graph/example_test.go) is a 30-line pipeline that pauses for approval.

## The game

[`internal/world`](internal/world/world.go) is a deterministic text-adventure engine, 250 lines,
with worlds as JSON. It only ever reveals what a player sees: prose, exits, visible items. It never
exposes the map, and dangers live in the prose ("from the west you hear slow, heavy breathing"),
not in structured fields. That is what forces reading. Every game is replayable from its command
log, which is why death is cheap: **reload is a replay without the fatal command.**

The bundled world, [`cellar`](internal/world/worlds/cellar.json): seven rooms, a lantern, a coin,
a passage that kills you in the dark, a troll who takes the coin or you, a door that asks a riddle,
and a chest.

## The player

| node | layer | what it does |
|---|---|---|
| `observe` | code | replays the command log and refreshes what the player sees |
| `assess` | decision | on a new room (or a changed inventory): one `choice` for the kind of place, one `noul` per exit for "walking through this now is lethal". Probabilities stored on the map. |
| `cheap_move` | code | take what is visible; explore a safe unexplored exit; or walk the known map (BFS) towards the nearest room that still has one. Most turns end here. |
| `propose` | small LLM | only when code has nothing: 3 to 5 candidate commands |
| `rank` | code, then decision | drop illegal and already-failed candidates; one left needs no model; several: Jev picks |
| `solve` | large LLM | a riddle is posed: answer it. The only node that needs real reading. |
| `act` | code | run the command; learn where the exit led; on death, roll the log back one step and remember the command as fatal here |
| `gate` | human | `Interrupt` with the screen and the reason; resume with a command or stop |
| `finalize` | code | write `game.md`: the layer table and every turn |

Routing is in [`internal/player/graph.go`](internal/player/graph.go): a turn is sorted in
priority order (game over; a human is owed a look; a riddle is posed; the room is new; code
moves), and the expensive nodes only run when the cheap ones had nothing to say.

Prompts are `text/template` files in [`internal/player/prompts/`](internal/player/prompts/),
embedded at build time.

## Run it

```bash
cp .env.example .env            # models per role, API keys, thresholds
go run ./cmd/play run
```

The graph plays until it wins, hits the turn limit, or needs you:

```
=== the graph needs you (the riddle resisted every attempt) ===
{ "obs": { "name": "Carved Antechamber", ... }, "recent": [...], "summary": [...] }

thread:  k3fj2m9qpz1a
command: play resume k3fj2m9qpz1a -command "answer echo"
stop:    play resume k3fj2m9qpz1a -stop
```

Resume whenever you want, from any shell. The save game is one JSON file per thread under
`.play/runs/`. The report lands next to it as `game.md`. If a run dies on an external error
(an expired key, a provider outage), `play resume <thread>` with no flags continues from the
node that failed; nothing before it is re-executed.

`PLAY_PROVIDER` picks who plays the two generative roles: `deepseek` (default; `deepseek-flash`
proposes, `deepseek-v4-pro` solves, through the OpenAI-compatible endpoint with JSON mode) or
`anthropic` (`claude-haiku-5-5` and `claude-opus-5-5`, through the official SDK with structured
outputs). Both adapters derive the JSON schema from the same Go struct; the graph never sees
the difference.

Without `TYPESAFE_API_KEY` every exit is treated as safe: code explores everything, dies,
reloads, remembers, and still wins. The ledger then shows what the decision model would have
saved. Without a generative key the run fails at the first node that needs one, which in the
bundled world is the riddle; `play resume <thread>` continues from there once the key is in.

## Why the shape matters

- **Deterministic by default.** Five of nine nodes have no model in them, and every edge
  decision is code. The map, the legality filter and the reload never drift.
- **Decide with a decider, generate with a generator.** Reading prose for risk is a judgment,
  not a composition: it returns a probability that code compares to a threshold. Only the
  riddle needs a sentence back.
- **One model per role, not one model.** Proposing commands is bounded and cheap to get
  wrong; solving a riddle is not. Swapping either is a `.env` change.
- **Death is a slice operation.** Because the engine is a replay, reload is `log[:len-1]`
  plus a note. No model is asked to "be more careful".
- **Humans are a gate, not a chat.** `Interrupt` pauses the graph with a checkpoint. The
  human's command runs through the same `act` node as everyone else's and is counted in the
  same ledger.
- **Fakes make it testable.** Nodes receive the models, the decider and the world as values or
  interfaces. [`internal/player`](internal/player/) runs whole games with scripted answers
  under the race detector in well under a second: a code-only win, death and reload, the risk
  assessment preventing a death, the proposal path, the decision model choosing among
  proposals, the riddle budget handing over to a human, the human stopping the run.

```bash
make test       # go test -race ./...
make lint       # golangci-lint
make diagram    # regenerates docs/graph.mmd; CI fails if it drifts
```

## Layout

```
cmd/play/              CLI: run / resume / diagram
graph/                 the control plane: Graph, Runner, Interrupt, Store, Mermaid
internal/world/        the engine and its worlds/*.json
internal/player/       the agent: State, nodes, routing, Config, prompts/*.tmpl
internal/jev/          Decider interface + System One client + question builders
internal/llm/          Model interface; Anthropic (structured outputs) and OpenAI-compatible (JSON mode) adapters
docs/graph.mmd         generated
```

Dependencies: the official Anthropic Go SDK and `invopop/jsonschema` (the schema the prompts
carry). Everything else is the standard library.

MIT.
