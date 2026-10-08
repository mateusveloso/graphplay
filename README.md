# graph-issue-triage

A small, complete Go program built around one idea: **the graph is the unit of engineering;
a model is just a node.**

It triages a public GitHub issue. Nine nodes, three kinds of model, one human gate. Every
decision is made at the cheapest layer that can make it:

| Layer | Cost | Used for |
|---|---|---|
| **code** | free, deterministic | fetching, capping, validating citations, routing, rendering |
| **decision model** ([TypeSafe Jev](https://typesafe.ai)) | cents per thousand calls; returns probabilities, cannot write | "which category?", "does this need code?", "is each rubric rule met?" |
| **small generative model** | cheap | planning: bounded output, cheap to get slightly wrong |
| **big generative model** | expensive | writing the triage, and judging only the drafts the cheaper layers could not settle |

```mermaid
graph TD;
	__start__([start]):::first
	load_issue(load_issue)
	classify(classify)
	planner(planner)
	collect(collect)
	writer(writer)
	check(check)
	critic(critic)
	gate(gate)
	finalize(finalize)
	__end__([end]):::last
	__start__ --> load_issue;
	load_issue --> classify;
	classify -.-> planner;
	classify -.-> writer;
	planner --> collect;
	collect --> writer;
	writer --> check;
	check -.-> critic;
	check -.-> gate;
	check -.-> writer;
	critic -.-> gate;
	critic -.-> writer;
	gate -.-> finalize;
	gate -.-> writer;
	finalize --> __end__;
	classDef default fill:#f2f0ff,line-height:1.2
	classDef first fill-opacity:0
	classDef last fill:#bfb6fc
```

Dotted edges are decisions. Every one of them is a Go function reading typed state and a
threshold from [`Config`](internal/triage/config.go). No prompt owns a loop bound.

## The graph runtime is under 500 lines, comments included, and it is yours

There is no LangGraph for Go, and this repo does not try to be one. [`graph/`](graph/) is
the whole control plane:

- `Graph[S]`: named nodes, fixed `Edge`s, and `Route`s whose targets are declared so the
  diagram can draw them and the runner can reject a router that wanders off.
- `Runner[S]`: executes nodes, checkpoints after every one, records how long each took.
- `Interrupt[T](ctx, payload)`: pauses the run with a payload; on resume the node runs
  again and receives a typed answer. A resume value of the wrong type is an error, not a panic.
- `Store[S]`: `FileStore` writes one JSON file per thread (you can `cat` a run) with an
  atomic rename; `MemoryStore` is for tests and round-trips through JSON on purpose.
- `Mermaid()`: deterministic, so `docs/graph.mmd` is committed and CI diffs it.

The package is generic over the state type and knows nothing about models, GitHub or
issues. `graph/example_test.go` is a 30-line pipeline that pauses for approval.

## The nodes

| Node | Layer | What it does |
|---|---|---|
| `load_issue` | code | Fetches the issue and the repo's top-level listing. |
| `classify` | decision | One `choice` (category, with confidence) and one `noul` ("does answering need the code?"). Produces a `Prior`. |
| `planner` | small LLM | Up to 3 hypotheses and the evidence requests to test them. Typed `Plan`. |
| `collect` | code | Executes the plan against GitHub, concurrently with a bound, preserving order. Caps requests. A failed fetch is evidence, not an error. |
| `writer` | big LLM | Writes the triage from issue + prior + evidence + every rejection so far. Typed `Triage`. |
| `check` | code, then decision | First a validator: a draft that cites evidence never collected is rejected without any model. Then one `noul` per rubric rule. Code turns the probabilities into a verdict: `reject`, `pass` or `uncertain`. |
| `critic` | big LLM | Generative judgment, paid only for `uncertain` drafts. Sees the rubric probabilities. |
| `gate` | human | `Interrupt`. The run is checkpointed and stops. A person approves or rejects with feedback from the CLI, now or next week. |
| `finalize` | code | Renders `triage.md` with the whole review trail. |

All nine live in [`internal/triage/nodes.go`](internal/triage/nodes.go); routing is in
[`graph.go`](internal/triage/graph.go) next to it:

- `classify -> writer` only when P(needs code) is at or below `SkipCodeBelow`; otherwise `-> planner`.
- `check -> writer` on `reject` while the round budget lasts, `-> gate` on `pass` or when the
  budget is exhausted, `-> critic` on `uncertain`.
- `critic -> writer` on rejection within budget, `-> gate` otherwise.
- `gate -> finalize` on approval; `-> writer` on rejection, with the human feedback appended to
  the same `Reviews` list the validator, the rubric and the critic write to, and the budget reset.

Prompts are `text/template` files in [`internal/triage/prompts/`](internal/triage/prompts/),
embedded at build time. Each defines a `system` and a `user` block; a test asserts they never
shadow each other.

## The decision model

Jev is not a chat model. You send a `state` and typed `questions`; it returns calibrated
probabilities. Three question types: `noul` (a yes/no statement, answered with P(yes)),
`choice` (one of up to 255 options, with per-option probabilities and a confidence), `score`
(an ordered scale). It cannot write a sentence, which is the point: a node that can only
decide cannot drift into doing the writer's job.

Two places in this graph want a decision and nothing else:

1. **Before any evidence**: what kind of issue is this, and do we need to read code at all?
   The answer routes the graph. A confident "no code" saves the planner, the collector and
   the GitHub calls.
2. **After every draft**: is each rubric rule met? One `noul` per rule, never a combined
   judgment. Code reads the probabilities against two thresholds. Clear fail: rejection with
   feedback templated from the rule that failed. Clear pass: straight to the human. Anything
   in between: the big model earns its price.

Without `TYPESAFE_API_KEY` the decider returns an error and the graph takes the full
generative path on every decision. Jev can never block a run. The client is
[`internal/jev`](internal/jev/client.go) and the thresholds are in `Config`.

## Run it

```bash
cp .env.example .env            # models per role, API keys, thresholds
go run ./cmd/triage run octocat/Hello-World#1
```

The run stops at the gate and prints the prior, the rubric probabilities, the draft and a
thread id:

```
thread:  k3fj2m9qpz1a
approve: triage resume k3fj2m9qpz1a -approve
reject:  triage resume k3fj2m9qpz1a -reject "what to change"
```

Resume whenever you want, from any shell. State is one JSON file per thread under `.triage/runs/`.

```bash
go run ./cmd/triage resume k3fj2m9qpz1a -reject "say which function raises"
go run ./cmd/triage resume k3fj2m9qpz1a -approve
# written: .triage/runs/k3fj2m9qpz1a/triage.md
```

`go install ./cmd/triage` gives you a `triage` binary. `GITHUB_TOKEN` is optional, but code
search and sane rate limits need one.

## Why the shape matters

- **Deterministic by default.** Four of nine nodes have no model in them, and every edge
  decision is code. Fetching, capping, validating, routing and rendering never drift.
- **Decide with a decider, write with a writer.** Classification and rubric checks are
  probabilities read by code, not prose read by another model. The generative critic only
  runs on what the probabilities could not settle.
- **One model per role, not one model.** The planner is small because its output is bounded
  and cheap to get slightly wrong. The writer and the critic are big because that is where
  quality is paid for. Swapping either is a `.env` change.
- **Humans are a gate, not a chat.** `Interrupt` pauses the graph with a checkpoint. Approval
  is a resume with a typed value. Nothing is re-run; nothing is lost between processes.
- **Fakes make it testable.** Nodes receive the models, the decider and the repository as
  interfaces. The tests run the whole graph with scripted answers under the race detector in
  well under a second: the skip-code route, the three rubric verdicts, the budget, the
  validator short-circuit, the human rejection path, resume from disk by a second runner, and
  the no-decider fallback.

```bash
make test       # go test -race ./...
make lint       # golangci-lint
make diagram    # regenerates docs/graph.mmd; CI fails if it drifts
```

## Layout

```
cmd/triage/            CLI: run / resume / diagram
graph/                 the control plane: Graph, Runner, Interrupt, Store, Mermaid
internal/triage/       the domain: State, nodes, routing, Config, prompts/*.tmpl
internal/github/       Repository interface + api.github.com client
internal/jev/          Decider interface + System One client + question builders
internal/llm/          Model interface + Anthropic structured-output adapter
docs/graph.mmd         generated
```

Dependencies: the official Anthropic Go SDK and `golang.org/x/sync`. Everything else is the
standard library.

MIT.
