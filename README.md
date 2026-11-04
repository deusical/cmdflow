# cmdflow

[![Go Reference](https://pkg.go.dev/badge/github.com/deusical/cmdflow.svg)](https://pkg.go.dev/github.com/deusical/cmdflow)
[![CI](https://github.com/deusical/cmdflow/actions/workflows/ci.yml/badge.svg)](https://github.com/deusical/cmdflow/actions/workflows/ci.yml)

Commands as configuration, for Go applications.

cmdflow is a small library for applications that let people build their own
commands (chat bots, automations, internal tools) without letting those
commands run arbitrary code. A command is a **trigger** plus a **linear list of
steps**. Each step runs a **module** your application registers, with typed
inputs and outputs. The engine checks commands when they're saved, snapshots
each run, stores runs as plain JSON so they can pause and resume, and sends
every step that acts on the world through a gate your application controls.

It imports only the Go standard library.

```
trigger.args ──► [ text.upper ] ──► [ work.wait ] ──► [ reply ]
                   pure, immediate    local, yields     sink: the host decides
```

## Install

```sh
go get github.com/deusical/cmdflow
```

Requires Go 1.22 or later.

## Why

Letting users (or an AI) assemble behaviour is useful, but running what they
write is risky. cmdflow keeps the two apart:

- **Configuration only connects.** A command can only wire registered modules
  together, so it can never do more than the registered capabilities allow.
  New capabilities are reviewed code, never configuration. Modules still
  treat their inputs and settings as untrusted: a module that takes a prompt
  or a URL does whatever that prompt or URL says.
- **Effects are gated.** A sink step, one that acts on the world (sending a
  message, charging a card), produces an *intent*. Your `Host.Effect` carries
  it out or refuses. You can let specific modules act directly if you choose.
  The other effect classes (`pure`, `local`, `paid`) are declarations the
  engine shows you but doesn't gate.
- **Runs are bounded.** A command is a fixed list of steps with no loops, and
  each step runs at most once.
- **Mistakes are caught when a command is saved,** not halfway through a run:
  unknown modules, missing or later-step connections, incompatible types, bad
  settings and your own policy rules.
- **Runs survive restarts.** A run is one JSON document recording where each
  step stands. A slow step can yield, and the run resumes from there later,
  even in another process.
- **Edits don't change running work.** Each run keeps the command, module
  versions and settings it started with.

## Quick start

```go
package main

import (
	"fmt"
	"strings"

	"github.com/deusical/cmdflow"
)

type ctx struct{} // your per-run context, passed to every module call

type host struct{}

func (host) Effect(s *cmdflow.Step) error                { fmt.Println(s.Text("text")); return nil }
func (host) Halt(_ *cmdflow.Step, v cmdflow.Value) error { fmt.Println(v.Text); return nil }
func (host) Save() error                                 { return nil } // persist the run here
func (host) Fail(code string) error                      { return nil }

func main() {
	e := cmdflow.New[ctx](cmdflow.Policy{DefaultTrigger: "hello", RequireEffect: true, EffectLast: true})
	e.RegisterType(cmdflow.Type{ID: "text", Title: "Text"})
	e.RegisterTrigger(cmdflow.Trigger{ID: "hello", Title: "Someone says hello",
		Outputs: []cmdflow.Port{{Name: "name", Title: "Their name", Types: []string{"text"}}}})

	text := []cmdflow.Port{{Name: "text", Title: "Text", Types: []string{"text"}, Required: true}}
	e.Register(cmdflow.Module[ctx]{
		Definition: cmdflow.Definition{ID: "greet", Version: 1, Title: "Greet", Effect: cmdflow.Pure, Immediate: true,
			Inputs:  []cmdflow.Port{{Name: "name", Title: "Name", Types: []string{"text"}, Required: true}},
			Outputs: []cmdflow.Port{{Name: "text", Title: "Greeting", Types: []string{"text"}}}},
		Accept: func(_ ctx, s *cmdflow.Step) (cmdflow.Outcome, error) {
			s.Out("text", cmdflow.Value{Type: "text", Text: "Hi, " + strings.TrimSpace(s.Text("name")) + "!"})
			return cmdflow.Done(), nil
		},
	})
	e.Register(cmdflow.Module[ctx]{
		Definition: cmdflow.Definition{ID: "say", Version: 1, Title: "Say", Effect: cmdflow.Sink, Inputs: text},
		Accept:     func(ctx, *cmdflow.Step) (cmdflow.Outcome, error) { return cmdflow.Done(), nil },
	})

	// A command is configuration, usually loaded from JSON and checked when saved.
	cmd := cmdflow.Pipeline{Steps: []cmdflow.StepConfig{
		{ID: "greeting", Module: "greet", Inputs: map[string]string{"name": "trigger.name"}},
		{ID: "out", Module: "say", Inputs: map[string]string{"text": "greeting.text"}},
	}}
	if err := e.Validate(&cmd, nil); err != nil {
		panic(err)
	}

	run := cmdflow.Snapshot(cmd, map[string]cmdflow.Value{"name": {Type: "text", Text: "Ada"}})
	e.Accept(ctx{}, run, host{}) // works out the greeting; nothing acts yet
	// A real host stores the accepted run here.
	e.Resume(ctx{}, run, host{}) // prints "Hi, Ada!" through host.Effect
}
```

A fuller example, with commands loaded from JSON, settings, a step that yields
and runs resumed from storage, is in [`examples/chatbot`](examples/chatbot):

```sh
go run ./examples/chatbot
```

## Concepts

| Term | Meaning |
|---|---|
| **Trigger** | A kind of event that starts a run (a chat command, a schedule, a webhook) and the outputs it provides. |
| **Module** | A registered capability: stable id and version, typed input and output ports, a strict settings schema and an effect class. |
| **Effect class** | `pure` (computes), `local` (works on the host, such as a download), `paid` (costs money, such as an AI call) or `sink` (acts on the world). |
| **Pipeline** | A command's configuration: a trigger and a linear list of steps. Each input connects to `trigger.<output>` or `<earlier step>.<output>`. Settings are fixed when the command is saved; values that change from run to run travel only through ports. Layout for an editor (positions and so on) is stored separately. |
| **Value type** | Registered by the host. A type can count as others (`Is`), so an input taking `file` accepts a `video`. Values carry text, a handle to something the host stores, or host-defined data. |
| **Run** | A snapshot of a pipeline plus each step's status (pending, intent, done, halted, skipped or failed), as one JSON document. |

## Lifecycle

```go
err := e.Validate(&cmd, env)                    // when a command is saved; env reaches module settings
run := cmdflow.Snapshot(cmd, triggerOutputs)    // when the trigger fires; cmd must be validated
result, err := e.Accept(ctx, run, host)         // Declined, Ready or Halted; then store the run
stopped, err := e.Resume(ctx, run, host)        // until !stopped
trace, halt, err := e.Simulate(ctx, run)        // offline preview; leaves run unchanged
```

1. **Accept** runs every step's `Admit` first (any can decline the run), then
   each step's `Accept` in order. Steps whose inputs are known when the event
   arrives can finish here; the rest are pending. A sink step that finishes
   here holds its intent: **no effect is delivered during Accept**. (A module
   your policy lets act directly is trusted to wait for `Run` itself.) A
   declined run is discarded; an accepted one is stored by the host. Accept
   changes the run only when it accepts it, so after an error it can be
   called again on the same run. It refuses a run made from a pipeline
   `Validate` never accepted, and trigger values whose type doesn't fit the
   input they're connected to.
2. **Resume** runs the remaining steps in order, calling `Host.Save` after each
   one finishes, and delivers each sink's intent to `Host.Effect` when its turn
   comes, after every earlier step. A step can `Yield` (for example while a
   download runs); the run is saved and the host calls `Resume` again later
   with the stored run.
3. A step can **Halt** the run early with a value (such as "nothing to do"),
   which goes to `Host.Halt`; the steps after it are skipped. A step can
   **Fail** with a code, which goes to `Host.Fail` and is recorded in the run
   (`run.Failed()`), and the run ends there.

Before delivering an intent, Resume checks that every required input of the
sink has a value; if one doesn't, the step fails with `CodeIncomplete`
instead.

The engine refuses to accept a run twice, to resume a run that was never
accepted, and to resume one that failed.

## The host

Your application implements `Host`:

| Method | Called when |
|---|---|
| `Effect(step)` | During `Resume`, a sink step's turn has come. Its connected inputs are the intent. |
| `Halt(step, value)` | A step ended the run early with a value. |
| `Save()` | During `Resume`, a step finished, yielded or failed, or an effect was delivered. Persist the run. |
| `Fail(code)` | The run failed: a module failed, is no longer registered (`CodeUnavailable`), or broke its contract (`CodeIncomplete`). |

What the engine expects of your host:

- **Store the run after `Accept`, before `Resume`.** Nothing has acted yet, so a
  run lost at this point is simply never carried out.
- **Effects are delivered at least once.** If your process stops after
  `Effect` returns but before the following `Save`, the next `Resume` calls
  `Effect` again for that step. Key effects by your run id and the step id if
  acting twice would hurt. An `Effect` error leaves the intent in the run, and
  the next `Resume` tries again without running the module again.
- **Errors are retryable.** An error from a module's `Run` or from any `Host`
  method leaves the run where it was, so the next `Resume` tries again: a
  `Halt` or `Fail` that returned an error is called again. Retrying, backing
  off or giving up is your choice.
- **One goroutine per run at a time.** Two processes resuming the same stored
  run need your storage to stop them (a lock or a version check).

and chooses a `Policy`:

| Field | Effect |
|---|---|
| `MaxSteps` | Limits the steps in a command. |
| `MaxEffects` | Limits sink steps. |
| `EffectLast` | Sink steps must be last in the list. |
| `RequireEffect` | The last step in the list must be a sink. |
| `Direct` | Sink modules allowed to act themselves instead of through `Host.Effect`. |
| `DefaultTrigger` | The trigger used when a command names none. |
| `Check` | Your own rules, run after the structural checks. |

Modules receive your per-run context type `C`, so they can reach your
application's state without the engine knowing about it. Use
`RegisterUpgrade` to rewrite old configuration when a module changes shape.
Upgrades run every time `Validate` runs, so each must leave configuration it
has already upgraded alone. `Validate` changes the pipeline only when it is
valid.

## Scope

cmdflow is deliberately small. It is **not** a workflow server, a job queue or
a scheduler: it doesn't store runs, retry them or run them in the background.
Your application does those things in whatever way suits it, and cmdflow keeps
the command model honest. Pipelines are linear for now. Branching may come
later as a graph of the same steps and connections, with each step running at
most once and no loops, so runs stay bounded.

## Status

Pre-1.0. The API may change between minor versions until 1.0; changes are
listed in [CHANGELOG.md](CHANGELOG.md). cmdflow was extracted from the
command pipeline in [SmsBot](https://github.com/deusical/smsbot-server), which
uses this package for every chat command.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: see
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
