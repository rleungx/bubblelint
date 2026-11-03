# bubblelint

A Go analyzer for `testing/synctest`. It checks misuse on known test call paths
and can suggest concurrent tests to consider migrating to synctest.

## Project status

Misuse checks (ST001–ST003) are enabled by default. Migration suggestions
(ST100–ST101) are optional and need human review. Start with misuse checks in
CI; enable suggestions separately while evaluating migration candidates.

CI covers Go 1.26 and 1.27 with race detection, vet, and builds. It also
tests the standalone CLI and a custom golangci-lint v2.13.0 binary, including
configuration errors, diagnostic locations, exclusions, and suppressions.
Regression tests cover mutable bindings, branching helper graphs, cleanup
execution contexts, direct callbacks, and method expressions.

The analyzer uses the concepts in [Testing concurrent code with
testing/synctest](https://go.dev/blog/synctest). That article describes the Go 1.24
experiment's `Run` API. Examples here use the stable Go 1.25+
[`synctest.Test`](https://pkg.go.dev/testing/synctest@go1.25.0) API.

## Run locally

The analyzer requires Go 1.26 or later. Build it with a Go version at least as
new as the project being analyzed. Install the release with:

```sh
go install github.com/rleungx/bubblelint/cmd/bubblelint@v0.1.0
```

Or build from this checkout:

```sh
make build
./bin/bubblelint ./...
```

To check another module, run the built binary from that module's directory:

```sh
cd /path/to/your/module
/path/to/bubblelint/bin/bubblelint ./...
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
| ST001 | Enabled | `synctest.Wait` or Go 1.27+ `synctest.Sleep` reached outside a bubble on a known test call path. |
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
          suggest: false
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
Standard-library method expressions such as `(*testing.T).Run` are also
recognized, including calls through immutable aliases. Supported callback APIs
also recognize directly supplied `synctest.Wait` and `T.Parallel` function
values. A diagnostic for such a callback points to the callback argument.
Bubble membership follows execution rather than the source position of a
helper definition. The analyzer tracks the specific `*testing.T` passed into
the bubble, so reading the outer test's deadline is allowed. Cleanup callbacks
follow their owning test's execution context, including ordinary subtests
started inside a bubble. Call caching retains lexical captures and parameter
bindings rather than unrelated callers' test objects.

Cross-package helper bodies, user-defined methods, interface dispatch,
reassigned function values, forwarded callback parameters, arbitrary callback
APIs, and `context.AfterFunc` callback execution contexts are not followed.
This includes test methods dispatched by suite frameworks or reflection.
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
make integration   # Build both binaries; verify CLI and golangci-lint behavior.
```

Integration tests require the binaries and are skipped during ordinary
`go test ./...` runs. They assert exact diagnostic locations and exit codes in
default, misuse-only, combined, suggestion-only, and disabled configurations.
They also cover invalid flags/settings, nolint comments, path exclusions,
supported valid patterns, and unresolved execution contexts.

## Compatibility and releases

The standalone analyzer supports the Go versions in the CI matrix above.
The pinned golangci-lint custom build requires Go 1.26 or later; use a newer
toolchain when the target project requires it. The minimum build toolchain was
raised from Go 1.25 to Go 1.26 to use a package loader that understands the
Go 1.27.3 export format. Projects declaring Go 1.25 can still be analyzed with a
supported toolchain. Go 1.27-only fixtures are guarded by build constraints so
Go 1.26 runs its applicable tests.

Pin a release tag for reproducible installation and custom plugin builds.
During v0 development, minor releases may add rules or change analysis coverage;
patch releases fix defects without renaming existing rule codes. Review release
notes before upgrading a required CI check. Suggestions remain opt-in.

Before releasing, run the supported toolchain checks, CLI/plugin integration,
and real-project validation. Review every default diagnostic and record corpus
revisions, analysis boundaries, elapsed time, and peak memory. A clean scan
does not establish correctness in code outside the documented analysis scope.

## Real-project validation

The 2026-11-03 validation used Go 1.27.3 on Linux/amd64 with `GOMAXPROCS=4`
and `GOFLAGS='-p=4 -mod=readonly'`. Each module was scanned with
`bubblelint -json -test=true ./...`. Times are medians of three warm-cache
runs; memory is the largest per-process peak RSS across those runs, rounded to
MiB, rather than the sum of concurrent processes. Both include package loading
and analysis, but exclude dependency downloads and build preparation.

| Corpus | Revision | Default findings | Time | Peak RSS |
| --- | --- | --- | --- | --- |
| PD root | [`1f8ddf819c59`](https://github.com/tikv/pd/tree/1f8ddf819c59a5c2a7df9198930343663fadeb65) | 0 | 2.41 s | 690 MiB |
| PD client | Same revision, separate module | 0 | 0.49 s | 90 MiB |
| grpc-go root | [`c2165f27d8bb`](https://github.com/grpc/grpc-go/tree/c2165f27d8bbc81e75aa14b82d23540882ac5f02) | 0 | 1.75 s | 553 MiB |

PD's documented `make dashboard-ui` prerequisite generated assets from its
pinned `v9.0.0-a2c7919b` release; source files and module dependencies were
unchanged. Other nested tools/example modules were outside this validation.
The corpora contain 2,100 recognized test entry points. PD contains 142
synctest call sites within supported entry/helper paths and four in excluded
suite methods. grpc-go has no synctest calls at this revision and many suite
methods outside the analysis scope, so it serves as a negative and migration
corpus rather than evidence of suite-method coverage.

Enabling suggestions produced 95 hints in PD root, 22 in PD client, and one in
grpc-go. All 118 were inspected against their source context. They match the
documented heuristics, but many involve real network I/O, mutex contention,
runtime finalizers, or deliberate stress/timing tests. These are not verified
migrations; suggestions should remain an optional review aid.

The original package loader could not read Go 1.27.3 export data. Timing
comparisons therefore used commit `4f74a5c` with the same updated module
dependencies as the candidate. Its medians were 2.43 s, 0.49 s, and 1.79 s
respectively; these samples do not establish a significant speedup.

The analyzer-only helper graph benchmark excludes loading and type checking.
At 10, 20, and 40 levels, regression tests require exactly 23, 43, and 83
analysis states. At 40 levels, the median of four paired benchmark samples was
647 microseconds for the dependency-matched control and 591 microseconds for
the candidate. This measures traversal cost, not whole-project scan speed.

## License

Licensed under [Apache-2.0](LICENSE).
