# bubblelint

Catch common `testing/synctest` mistakes in Go tests. bubblelint can also suggest
concurrent tests that may benefit from synctest's virtual time.

## Quick start

Requires Go 1.26 or newer. Use a Go toolchain at least as new as the project you
want to check.

```sh
go install github.com/rleungx/bubblelint/cmd/bubblelint@v0.1.0
```

Run from your project's module directory:

```sh
bubblelint ./...
```

This checks for synctest misuse. To also get migration suggestions:

```sh
bubblelint -suggest ./...
```

Or run only migration suggestions:

```sh
bubblelint -check-misuse=false -suggest ./...
```

## What it checks

A *bubble* is the group of goroutines created by `synctest.Test`, with its own
virtual clock.

| Code | Check | Enabled by default |
| --- | --- | --- |
| ST001 | `synctest.Wait` or Go 1.27+ `synctest.Sleep` called outside a bubble. | Yes |
| ST002 | A synctest bubble started inside another bubble. | Yes |
| ST003 | `Run`, `Parallel`, or `Deadline` called on the `*testing.T` supplied by `synctest.Test`. | Yes |
| ST100 | Sleeping after starting asynchronous work outside a bubble. | No |
| ST101 | A `select` waiting on both `time.After` and another channel outside a bubble. | No |

For example, bubblelint reports both mistakes in this test:

```go
func TestWorker(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        t.Parallel() // ST003: this T cannot run in parallel.
    })
    synctest.Wait() // ST001: this call is outside the bubble.
}
```

Move `synctest.Wait` into the callback. If the test should run in parallel, call
`t.Parallel()` on the outer T before entering `synctest.Test`.

## Using migration suggestions

Suggestions are starting points for review. Check that the test's goroutines,
channels, and dependencies can all run within a bubble before changing it.
Tests using real network connections, mutex contention, or deliberate timing
and stress loops may need a different approach.

`time.Sleep` is valid inside a bubble and uses virtual time. `synctest.Wait`
waits for other goroutines to finish or become durably blocked; it does not
advance time to the next timer. Replacing every sleep with `Wait` would change
the test's behavior. Suggestions are available for projects declaring Go 1.25
or newer, and bubblelint does not make automatic edits.

## Use with golangci-lint

bubblelint uses golangci-lint's [module plugin system](https://golangci-lint.run/docs/plugins/module-plugins/).
Build a custom binary with bubblelint included. With `golangci-lint` installed:

```sh
git clone --branch v0.1.0 https://github.com/rleungx/bubblelint.git
cd bubblelint
make custom
```

The included build configuration pins golangci-lint v2.13.0. Add the following
to the project's `.golangci.yml`, merging it with any existing configuration:

```yaml
version: "2"
run:
  tests: true
linters:
  enable:
    - bubblelint
  settings:
    custom:
      bubblelint:
        type: module
        settings:
          check-misuse: true
          suggest: false # Set to true for migration suggestions.
```

Then run the custom binary from the project you want to check:

```sh
cd /path/to/your/project
/path/to/bubblelint/bin/golangci-lint-bubble run ./...
```

With golangci-lint, you can suppress a finding using `//nolint:bubblelint` with
a reason, or use your project's usual exclusion rules.

## Scope

bubblelint starts at `TestXxx(*testing.T)` functions and follows same-package
helper functions, closures, and supported callbacks such as `T.Run` and
`T.Cleanup`. It does not follow methods on user-defined types (including suite
tests), cross-package helpers, or dynamic callbacks. Benchmarks, examples, and
fuzz tests are outside its scope.

Use `go test -race` alongside bubblelint to check for data races.

## License

[Apache-2.0](LICENSE).
