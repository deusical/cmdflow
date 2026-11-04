# Changelog

All notable changes are listed here. The project follows
[Semantic Versioning](https://semver.org/); until 1.0, minor versions may
change the API.

## [Unreleased]

### Fixed

- A `Host.Halt` or `Host.Fail` that returns an error is called again on the
  next `Resume`. Before, the run was already marked halted or failed, so the
  host was never told. `Accept` returns `Declined` with the error when `Halt`
  fails, leaving the run unaccepted.
- A sink's intent is delivered only when every required input has a value;
  otherwise the step fails with `CodeIncomplete`. A sink that finished at
  acceptance skipped the check.
- `Accept` changes the run only when it returns `Ready` or `Halted`. Before,
  an error or a decline left earlier steps finished, and calling `Accept`
  again skipped their `Admit`.
- `Fail("")` records `CodeIncomplete` instead of an empty code.

### Added

- `ErrNotValidated`: `Accept` and `Simulate` refuse a run made from a
  pipeline `Validate` never accepted (its `Format` is 0).
- `ErrTriggerValue`: `Accept` and `Simulate` refuse a trigger value whose
  type doesn't fit the input it is connected to.
- Validation errors name the unknown trigger or module, and the step id
  message says an id starts with a letter.

## [0.2.0] - 2026-11-03

This release changes the API and the run format; runs stored by 0.1.0 can't
be resumed.

### Changed

- Nothing acts during `Accept`. A sink step that finishes there holds its
  intent, and `Resume` delivers it to `Host.Effect`, in order, after every
  earlier step. Hosts call `Resume` after storing an accepted run, even when
  every step finished at acceptance.
- A run records each step's `Status` (pending, intent, done, halted, skipped,
  failed) and failure `Code` in place of `Done`, plus `Format` and
  `Accepted`. `Run.Halted` is now a method, and `Run.Failed` reports the
  failed step.
- `Definition.Effect` has the type `Effect`.
- `Simulate` works on a copy of the run, returns a `*StepError` for a step
  that fails and `ErrNoPreview` for one that can't be previewed, and its
  trace shows each step's inputs (a sink's intent) and effect class.
- `Resume` saves the run when a step yields, fails, or has its effect
  delivered, as well as when a step finishes.
- `Take` clears the taken data only once the taking step finishes.
- A module without `Run` is `AcceptOnly`, and `AcceptOnly` no longer
  requires `Immediate`.

### Fixed

- An `Effect` error no longer marks the step done: the intent stays, and the
  next `Resume` delivers it without running the module again.
- A sink step no longer acts before an earlier pending step, or before a
  later step declines or halts the run.
- `Accept` refuses a run already accepted, and `Resume` refuses a run that
  was never accepted (which skipped every `Admit`) or that failed.
- `Validate` changes the pipeline only when it is valid.
- Settings errors keep their identity (`errors.Is`, `errors.As`), and an
  `*Error` from `Settings` keeps its port.
- `Register` panics on an unknown effect class (a misspelled sink skipped the
  gate), an empty id, a version below 1, neither `Accept` nor `Run`, an
  `Immediate` or `AcceptOnly` module without `Accept`, and duplicate ports.
  `RegisterType` and `RegisterTrigger` panic on duplicates instead of
  replacing silently.
- A step that sets an undeclared or mistyped output, or whose required input
  has no value when it runs, fails with `CodeIncomplete` instead of passing
  the gap on to an effect.

### Added

- `Pipeline.Format`, `Run.Format` and `FormatVersion`.
- `Run.Clone`, `StepRun.Finished`, `Error.Err` and `Error.Unwrap`.
- `ErrFormat`, `ErrAccepted`, `ErrNotAccepted`, `ErrFailed`, `ErrNoPreview`.

### Docs

- What a host must do: store the run before `Resume`, at-least-once effects,
  retryable errors, one goroutine per run.
- Upgrades must be idempotent; settings are fixed at save time; only sink
  steps are gated; SmsBot runs its own copy of the pipeline, not this
  package.

## [0.1.0] - 2026-11-03

First public release.

- Engine with registered value types, triggers, modules and upgrades.
- Save-time validation: earlier-only, type-compatible connections, required
  inputs, strict module settings, module versions and a host policy (step and
  effect limits, effect position, a required final effect, a `Check` hook).
- Runs as JSON snapshots with `Accept`, `Resume` (with `Yield`), `Halt`,
  `Decline` and `Fail`, and an offline `Simulate`.
- Effects gated through `Host.Effect`, or direct per module via
  `Policy.Direct`.
- Example chat bot in `examples/chatbot`.

[0.2.0]: https://github.com/deusical/cmdflow/releases/tag/v0.2.0
[0.1.0]: https://github.com/deusical/cmdflow/releases/tag/v0.1.0
