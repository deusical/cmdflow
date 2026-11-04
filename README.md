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
every side effect through a gate your application controls.

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
  together. New capabilities are reviewed code, never configuration.
- **Effects are gated.** A step that acts on the world (sending a message,
  charging a card) produces an *intent*. Your `Host.Effect` carries it out or
  refuses. You can let specific modules act directly if you choose.
- **Mistakes are caught when a command is saved,** not halfway through a run:
  unknown modules, missing or later-step connections, incompatible types, bad
  settings and your own policy rules.
- **Runs survive restarts.** A run is one JSON document. A slow step can yield,
  and the run resumes from there later, even in another process.
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
	e.Accept(ctx{}, run, host{}) // prints "Hi, Ada!" through host.Effect
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
| **Pipeline** | A command's configuration: a trigger and a linear list of steps. Each input connects to `trigger.<output>` or `<earlier step>.<output>`. |
| **Value type** | Registered by the host. A type can count as others (`Is`), so an input taking `file` accepts a `video`. Values carry text, a handle to something the host stores, or host-defined data. |
| **Run** | A snapshot of a pipeline plus each step's progress, as one JSON document. |

## Lifecycle

```go
err := e.Validate(&cmd, env)                    // when a command is saved; env reaches module settings
run := cmdflow.Snapshot(cmd, triggerOutputs)    // when the trigger fires
result, err := e.Accept(ctx, run, host)         // Declined, Ready or Halted
stopped, err := e.Resume(ctx, run, host)        // later, until !stopped
trace, halt, err := e.Simulate(ctx, run)        // offline preview, no effects
```

1. **Accept** runs every step's `Admit` first (any can decline the run), then
   each step's `Accept` in order. Steps whose inputs are known when the event
   arrives can finish here; the rest are pending. The host stores the run.
2. **Resume** runs the remaining steps in order, calling `Host.Save` after each
   one finishes. A step can `Yield` (for example while a download runs); the
   host calls `Resume` again later with the stored run.
3. A step can **Halt** the run early with a value (such as "nothing to do"),
   which goes to `Host.Halt`, or **Fail** it with a code, which goes to
   `Host.Fail`.

## The host

Your application implements `Host`:

| Method | Called when |
|---|---|
| `Effect(step)` | A sink step finished. Its connected inputs are the intent. |
| `Halt(step, value)` | A step ended the run early with a value. |
| `Save()` | A step finished during `Resume`. Persist the run. |
| `Fail(code)` | The run failed: a module failed, or is no longer registered (`CodeUnavailable`). |

and chooses a `Policy`:

| Field | Effect |
|---|---|
| `MaxSteps` | Limits the steps in a command. |
| `MaxEffects` | Limits sink steps. |
| `EffectLast` | Sink steps must be last. |
| `RequireEffect` | The last step must be a sink. |
| `Direct` | Sink modules allowed to act themselves instead of through `Host.Effect`. |
| `DefaultTrigger` | The trigger used when a command names none. |
| `Check` | Your own rules, run after the structural checks. |

Modules receive your per-run context type `C`, so they can reach your
application's state without the engine knowing about it. Use
`RegisterUpgrade` to rewrite old configuration when a module changes shape.

## Scope

cmdflow is deliberately small. It is **not** a workflow server, a job queue or
a scheduler: it doesn't store runs, retry them or run them in the background.
Your application does those things in whatever way suits it, and cmdflow keeps
the command model honest. Pipelines are linear; branching may come later.

## Status

Pre-1.0. The API may change between minor versions until 1.0; changes are
listed in [CHANGELOG.md](CHANGELOG.md). cmdflow was extracted from
[SmsBot](https://github.com/deusical/smsbot-server), where it runs every chat
command.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: see
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
