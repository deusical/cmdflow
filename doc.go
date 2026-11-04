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
//     and type-compatible, settings match their schema, module versions exist.
//   - A run is a snapshot: the recipe, module versions and effective settings
//     it started with. Editing the command never changes a run.
//   - A run is one serializable document. Any step can yield and the run
//     resumes later, from the step it stopped at.
//   - Effects go through the host: a sink module's step produces an intent
//     (its connected inputs), and the host's Effect decides what happens,
//     unless the host's policy lets that module act directly.
//   - Composition only: configuration can connect registered modules, never
//     add code.
//
// A typical host calls Validate when a command is saved, Snapshot and Accept
// when its trigger fires, stores the Run, and calls Resume until it is no
// longer stopped. Simulate previews a command offline.
package cmdflow
