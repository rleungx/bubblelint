# bubblelint

A Go analyzer for `testing/synctest`. It checks misuse on known test call paths
and can suggest concurrent tests to consider migrating to synctest.

## Project status

This is an initial implementation. Rule tests, race checks, and custom
golangci-lint integration checks have passed. A subsequent source review found
two issues that need to be addressed before upstream integration:

- A reassigned function parameter can be resolved to its later value, causing
  a false misuse report for an earlier call.
- Memoization retains ancestor test bindings and can repeat analysis across
  branching helper graphs, causing excessive CPU and memory use.

The declared minimum Go version and false positives on real projects still
need validation. The project currently has no license or CI workflow.

The analyzer uses the concepts in [Testing concurrent code with
testing/synctest](https://go.dev/blog/synctest). That article describes the Go 1.24
experiment's `Run` API. Examples here use the stable Go 1.25+
[`synctest.Test`](https://pkg.go.dev/testing/synctest@go1.25.0) API.

## Run locally

The analyzer requires Go 1.25 or later. From this checkout:

```sh
make build
./bin/bubblelint -suggest ./...
```

To check another module, run the built binary from that module's directory:

```sh
cd /path/to/your/module
/path/to/bubblelint/bin/bubblelint -suggest ./...
```

Misuse checks are enabled by default. Migration suggestions are opt-in:

```sh
bubblelint ./...                               # Misuse only.
bubblelint -suggest ./...                      # Both groups.
bubblelint -check-misuse=false -suggest ./...   # Migration suggestions only.
```

## Rules

| Code | Default | Reports |
| --- | --- | --- |
| ST001 | Enabled | `synctest.Wait` reached outside a bubble on a known test call path. |
| ST002 | Enabled | Starting a synctest bubble from inside another bubble. |
| ST003 | Enabled | Calling `Run`, `Parallel`, or `Deadline` on the `*testing.T` supplied by `synctest.Test`. |
| ST100 | Opt-in | A positive or nonconstant `time.Sleep` after a visible async start on a test call path outside a bubble. |
| ST101 | Opt-in | A select receiving from both `time.After(...)` and another channel outside a bubble. |

ST100 recognizes `go` statements, `context.AfterFunc`, `time.AfterFunc`, and
`sync.WaitGroup.Go`, including starts in directly called same-package helpers.
Worker sleeps are skipped. ST101 recognizes receive statements and receive
assignments. Sleep-only tests and selects containing only timers are skipped.
Suggestions are skipped for packages declaring a Go version before 1.25.

Migration rules identify candidates. A sleep may intentionally exercise a
timer, and a channel may depend on external I/O. The checks do not establish
that an entire test can run within one bubble. No automatic edits are offered.

## Migration example

This test uses a wall-clock timeout to check that a callback has not run:

```go
func TestAfterFunc(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    called := make(chan struct{})
    context.AfterFunc(ctx, func() { close(called) })
    select {
    case <-called:
        t.Fatal("callback ran before cancellation")
    case <-time.After(10 * time.Millisecond): // ST101
    }
    cancel()
}
```

After checking that the test has no external dependencies, it can use a bubble
and wait for the callback goroutine to become durably blocked or finish:

```go
func TestAfterFunc(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        ctx, cancel := context.WithCancel(t.Context())
        defer cancel()
        called := false
        context.AfterFunc(ctx, func() { called = true })
        synctest.Wait()
        if called {
            t.Fatal("callback ran before cancellation")
        }
        cancel()
        synctest.Wait()
        if !called {
            t.Fatal("callback did not run after cancellation")
        }
    })
}
```

`time.Sleep` inside a bubble advances virtual time and is valid. `synctest.Wait`
provides synchronization; it does not advance virtual time to the next timer.
Replacing every sleep with `Wait` would change timer tests' behavior. Network
sockets and mutex locks are not durably blocking operations; network tests may
need an in-memory connection such as `net.Pipe` before migration.

## golangci-lint integration

The project implements golangci-lint's official [module plugin
system](https://golangci-lint.run/docs/plugins/module-plugins/). This builds a
custom binary containing bubblelint. The ordinary golangci-lint binary does not
include this plugin.

The supplied build configuration pins golangci-lint v2.13.0, which requires
Go 1.26 or later. With `golangci-lint` installed, run from this checkout:

```sh
make custom
./bin/golangci-lint-bubble run ./...
```

`.custom-gcl.yml` specifies where the plugin source lives:

```yaml
version: v2.13.0
name: golangci-lint-bubble
destination: ./bin
plugins:
  - module: github.com/rleungx/bubblelint
    import: github.com/rleungx/bubblelint/plugin
    path: .
```

When building from another project's directory, replace `path: .` with the path
to this checkout. Keep the `import` field: registration lives in the `plugin`
subpackage.

Add this configuration to the project being checked:

```yaml
version: "2"
run:
  tests: true
linters:
  default: none
  enable:
    - bubblelint
  settings:
    custom:
      bubblelint:
        type: module
        description: Check synctest usage and find concurrent tests to migrate.
        settings:
          check-misuse: true
          suggest: true
```

If the project already has linters enabled, merge `bubblelint` and its settings
into the existing configuration. Plugin settings reject unknown keys and wrong
value types. Suppress an accepted candidate with `//nolint:bubblelint` and a
reason, or use the project's existing exclusion rules.

## Analysis boundaries

Analysis starts at `TestXxx(*testing.T)` entry points in nongenerated
`_test.go` files. It follows statically resolved same-package free functions,
directly invoked closures, immutable local function aliases, named synctest
callbacks, `T.Run`, `T.Cleanup`, `time.AfterFunc`, and `WaitGroup.Go` callbacks.
Bubble membership follows execution rather than the source position of a
helper definition. The analyzer tracks the specific `*testing.T` passed into
the bubble, so reading the outer test's deadline is allowed.

Cross-package helper bodies, user-defined methods, interface dispatch,
reassigned function values, forwarded callback parameters, arbitrary callback
APIs, and `context.AfterFunc` callback execution contexts are not followed.
Uncalled helpers are not assumed to run outside a bubble. Benchmarks, examples,
fuzz callbacks, and package initializers are not entry points. Syntax traversal
does not prove branch reachability or reset async history after a join.

The analyzer does not prove the absence of races, concurrent `Wait` calls,
channel or WaitGroup ownership violations, goroutine leaks, missing `Wait`
calls, or the suitability of external I/O for synctest. Use `go test -race`
alongside it.

## Development

```sh
make test          # Rule fixtures, plugin settings, race detection.
make integration   # Build the custom binary; verify all rules through golangci-lint.
```

Integration tests require the custom binary and are skipped during ordinary
`go test ./...` runs. They assert diagnostics and exit codes for misuse and
migration fixtures, plus the absence of reports for supported valid patterns
and execution contexts the analyzer deliberately leaves unresolved.
