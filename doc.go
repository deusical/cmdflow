// Package cmdflow runs commands written as configuration: a trigger followed
// by a linear list of steps, each running a registered, versioned module with
// typed input and output ports and a strict settings schema. Each input is
// connected to the trigger or to an earlier step's output.
//
// The engine knows nothing about what triggers, modules or effects mean. The
// host application registers them, stores runs, and decides whether an effect
// may happen. The engine guarantees:
//
//   - Configuration is checked when it is saved: connections are earlier-only
//     and type-compatible, settings pass their module's checks, module
//     versions exist. Settings are fixed then; values that change from run
//     to run travel only through ports.
//   - A run is a snapshot: the recipe, module versions and effective settings
//     it started with. Editing the command never changes a run.
//   - A run is one serializable document recording each step's status. Any
//     step can yield and the run resumes later, from the step it stopped at.
//   - Effects go through the host: a sink module's step produces an intent
//     (its connected inputs), and the host's Effect decides what happens,
//     unless the host's policy lets that module act directly. No intent is
//     delivered during Accept; Resume delivers each in order, after every
//     earlier step, at least once. A module the policy lets act directly
//     is trusted to keep the same order itself.
//   - Runs are bounded: no loops, and each step runs at most once.
//   - Composition only: configuration can connect registered modules, never
//     add code, so a command can't exceed the registered capabilities.
//
// A typical host calls Validate when a command is saved, Snapshot (of the
// validated pipeline) and Accept when its trigger fires, stores the Run, and calls Resume until it is no
// longer stopped. Simulate previews a command offline.
package cmdflow
