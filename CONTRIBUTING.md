# Contributing

Thanks for your interest. Issues and pull requests are welcome.

## Before you start

For anything beyond a small fix, please open an issue first so we can agree
on the approach. cmdflow aims to stay small: it models commands and runs, and
leaves storage, scheduling and transport to the host application.

## Ground rules

- **Standard library only.** The package must not import anything outside the
  Go standard library; `TestImportsOnlyTheStandardLibrary` enforces this.
- **Go 1.22 compatible.** Don't use language features or standard library APIs
  newer than the version in `go.mod`.
- **Tests with every change.** Use the toy host in `engine_test.go` or a new
  one; never depend on a real application.
- **Document exported API** with doc comments, and update `README.md` and
  `CHANGELOG.md` when behaviour changes.

## Checks

CI runs these on Linux, macOS and Windows; please run them locally first:

```sh
gofmt -l .                # must print nothing
go vet ./...
go test -race ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

## Commit messages

Write a short summary line in the imperative ("Add Engine.Steps"), then a
blank line and any detail.
