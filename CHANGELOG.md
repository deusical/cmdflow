# Changelog

All notable changes are listed here. The project follows
[Semantic Versioning](https://semver.org/); until 1.0, minor versions may
change the API.

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

[0.1.0]: https://github.com/deusical/cmdflow/releases/tag/v0.1.0
