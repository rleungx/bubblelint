# bubblelint

A linter for Go tests using `testing/synctest`. Find incorrect synctest calls
and tests that could use virtual time.

## Get started

Requires Go 1.26 or newer. Use a newer toolchain if your project requires it.

```sh
go install github.com/rleungx/bubblelint/cmd/bubblelint@main
```

Run in your project's module directory:

```sh
bubblelint ./...
```

## Example

`synctest.Wait` needs to run inside `synctest.Test`. This test gets a warning:

```go
func TestWorker(t *testing.T) {
    synctest.Wait() // ST001: called outside a synctest bubble.
}
```

Move the call into `synctest.Test`:

```go
func TestWorker(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        synctest.Wait()
    })
}
```

## Checks

All five checks run by default:

| Code | What it finds |
| --- | --- |
| ST001 | `synctest.Wait` or Go 1.27+ `synctest.Sleep` called outside a bubble. |
| ST002 | A synctest bubble started inside another bubble. |
| ST003 | `Run`, `Parallel`, or `Deadline` called on the `*testing.T` provided by `synctest.Test`. |
| ST100 | Sleeps used after starting asynchronous work. |
| ST101 | Timeout-based channel checks with verified finite, local producers. |

Choose individual checks, or start with all checks and exclude some:

```sh
bubblelint -checks=ST003 ./...
bubblelint -checks=all,-ST101 ./...
```

Entries apply from left to right. Use `-checks=` to disable every check.
ST100 and ST101 suggest tests that may benefit from synctest; review these
suggestions when a test depends on real network I/O or timing behavior.

Migration suggestions are withheld when visible network operations, unresolved
interface or function-value calls, or `runtime.SetFinalizer`/`runtime.AddCleanup`
registrations are reachable through local helpers or callbacks. Effects propagate back to callers,
including through recursive helpers, and suppression is inherited by callees.
Unused closures and statically unreachable branches do not suppress suggestions.
An interface with a statically known concrete receiver remains eligible. Function
values in fields or containers that cannot be resolved are treated as unknown.
Immutable function results are followed through local factories with one return
site; external factories share the package-boundary limitation described below.
Clock reads propagate through synchronous local helpers. Clock-polling cycles
without a guaranteed positive sleep are also withheld:
a busy goroutine prevents virtual time from advancing. Conditional sleeps and
`Sleep(0)` do not establish that guarantee. Finite ranges over integers, strings,
arrays, and slices do not establish a polling cycle by themselves; nested
unbounded loops and restarting gotos still can.

ST101 uses the bounded producer protocol described under Automatic fixes,
including inside subtests and local helpers. Diagnostic eligibility is independent
of whether the source can be edited automatically. A `select` with `time.After`
alone is not enough: its timeout could be a wall-clock
watchdog for a computation that never blocks, preventing virtual time from
advancing. Unknown producers, producers implemented by helpers, and more complex
protocols are skipped. A direct `t.Helper()` call does not prevent recognition.

This analysis deliberately trades coverage for fewer misleading suggestions.
Local helper summaries combine branches whose conditions are unknown; they do
not specialize a helper for each argument or receiver value. Calls into other
packages may still hide I/O from ST100's heuristic. Remaining sleep suggestions
need review for bubble compatibility.

The former `suggest` and `check-misuse` options are replaced by `checks`.
Use `-checks=ST001,ST002,ST003` instead of `-suggest=false`, or
`-checks=ST100,ST101` instead of `-check-misuse=false`.

## Automatic fixes

Preview supported fixes without changing files:

```sh
bubblelint -fix -diff ./...
```

Apply supported fixes, check for remaining findings, and run your tests:

```sh
bubblelint -fix ./...
bubblelint ./...
go test ./...
```

This removes unsupported `t.Parallel()` statements directly inside inline
`synctest.Test` callbacks and migrates eligible concurrent tests to synctest.

Migration fixes cover simple tests whose goroutines finish by closing or sending
to local channels:

- **ST100:** the test receives every result before checking assertions.
- **ST101:** a two-case `select` waits for the final worker or a constant
  `time.After` timeout. The worker must be guaranteed to signal before the timeout.

The fix adds the import and wraps the test body. Existing `Sleep` calls,
`select` branches, timeout values, and assertions stay intact; time advances
virtually inside the test.

Some tests with helpers, shared state, or more complex control flow can still
receive ST100 suggestions without a fix. ST101 skips tests outside its verified
protocol. Review the diff and run your tests after applying fixes.

## golangci-lint

Use bubblelint with golangci-lint by building a custom binary that includes it.

<details>
<summary>Setup instructions</summary>

With `golangci-lint` installed:

```sh
git clone https://github.com/rleungx/bubblelint.git
cd bubblelint
make custom
```

Add bubblelint to your project's `.golangci.yml`. Merge these settings with
any configuration you already have:

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
          checks: [all] # Optional: all checks run by default.
```

Use the same rule names and exclusions in this list, such as
`checks: [all, -ST101]`. An empty list (`checks: []`) disables all checks.

Run the custom binary in your project's directory:

```sh
cd /path/to/your/project
/path/to/bubblelint/bin/golangci-lint-bubble run ./...
```

Add `--fix` to apply the same automatic fixes:

```sh
/path/to/bubblelint/bin/golangci-lint-bubble run --fix ./...
```

</details>

## License

[Apache-2.0](LICENSE).
