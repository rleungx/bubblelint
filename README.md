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

ST001–ST003 run by default. ST100 and ST101 suggest constrained migrations;
ST102 identifies cases for manual review:

| Code | What it finds |
| --- | --- |
| ST001 | `synctest.Wait` or Go 1.27+ `synctest.Sleep` called outside a bubble. |
| ST002 | A synctest bubble started inside another bubble. |
| ST003 | `Run`, `Parallel`, or `Deadline` called on the `*testing.T` provided by `synctest.Test`. |
| ST100 | Sleeps used after starting asynchronous work. |
| ST101 | Timeout-based channel checks with verified finite, local producers. |
| ST102 | Timeout-based completion or readiness checks with a local worker. The worker and its dependencies must be reviewed. |

Choose individual checks, or start with all checks and exclude some:

```sh
bubblelint -checks=ST003 ./...
bubblelint -checks=ST100,ST101 ./...
bubblelint -checks=ST102 ./...
bubblelint -checks=all,-ST101 ./...
```

Entries apply from left to right. Use `-checks=` to disable every check.
ST100 and ST101 suggest tests that may benefit from synctest; review how
virtual time affects their assertions and cleanup.
ST102 checks two local patterns after applying the migration barriers below:
a timeout waiting for a worker that calls a timer-using helper, or a timeout
waiting on a local channel before receiving the worker's result or completion
signal. The worker can
signal by closing a channel or sending a result of any type. Immutable channel
aliases and locally initialized struct fields refer to the same channel.
For readiness checks, local assignments must connect the observed channel to
an object or value the worker can access. That connection does not prove that
the worker produces the signal.

ST102 follows reachable statements and checks that the timeout branch reports
a test failure. Negative assertions that expect the timer to fire are skipped.
It currently handles one inline worker followed by a select in a test, subtest,
or local test helper; more complex worker and control-flow patterns can be
missed. Protocols covered by ST101 do not also receive ST102 diagnostics.

ST102 skips unknown calls, clock reads, synchronization primitives whose
lifecycle is not modeled, shared package variables, recursive helpers, and
unbounded loops. Its observed and completion channels must be created inside
the proposed test or subtest boundary. It never offers an automatic fix:
signal origin, assertion semantics, and cleanup still need review. A reachable
timer call in a helper does not establish that every execution path reaches it.
Assertions that depend on distinct timestamps may also need an explicit
virtual-time advance to keep testing the intended ordering.

All migration checks withhold suggestions when visible network operations,
mutex lock calls, unresolved interface or function-value calls, or
`runtime.SetFinalizer`/`runtime.AddCleanup` registrations are reachable through
local helpers or callbacks. Calls without an available body also block
suggestions unless the operation has an explicit standard-library model or an
available effect summary from its package.
This includes opaque calls during setup and cleanup, and function values
returned by external factories. Formatting arguments with unmodeled methods
also block suggestions because formatting may execute user-defined code.
Unknown behavior is never treated as safe. Effects propagate back to callers,
including through recursive helpers, and suppression is inherited by callees.
Standard library dependencies use public API models; unmodeled operations remain
opaque. Their internal implementations do not supply migration summaries.
Unused closures and statically unreachable branches do not suppress suggestions.
An interface with a statically known concrete receiver remains eligible. Function
values in fields or containers that cannot be resolved are treated as unknown.
Immutable function results are followed through local factories with one return
site; results from external factories remain unknown.
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
General effect summaries combine branches whose conditions are unknown.
Bounded decision summaries can specialize simple helpers using their actual
arguments and receiver values. Calls into other
packages without modeled behavior or usable summaries suppress suggestions.
A missing diagnostic can therefore mean that the analysis lacks evidence,
rather than that the
test cannot be migrated. Remaining sleep suggestions need review for bubble
compatibility.

### Explain withheld candidates

For a migration audit, include explanations alongside the selected checks:

```sh
bubblelint -checks=ST100,ST101,ST102 -explain-migration ./...
bubblelint -checks=ST102 -explain-migration -json ./...
```

The `migration` diagnostic category distinguishes two outcomes:

| Outcome | Meaning |
| --- | --- |
| `migration blocked` | A required path contains an external dependency or an API incompatible with the proposed bubble, such as network I/O or `t.Parallel`. |
| `migration unproven` | Analysis lacks evidence, for example an unresolved interface target, unmodeled callback, mutex progress, or clock-dependent assertions. |

Each explanation includes a witness call chain. JSON output includes related
source locations for the available evidence. Explanations never offer fixes;
they also produce a nonzero exit status in the CLI's text mode. They currently
cover a direct worker followed by a completion/readiness timeout or a sleep,
and do not enumerate every possible migration boundary.

Function summaries propagate through local and imported helpers using
`go/analysis` facts. A timer-only helper can remain eligible across package
boundaries, while a wrapper around network I/O retains that effect. Immutable
interface values with known concrete receivers use the concrete method's
summary. Decision summaries retain boolean and nil conditions, short-circuit
expressions, early returns, and calls to known function or literal callbacks.
They also follow fields of single-use local object literals and simple factory
returns. Field writes, aliases, or escape invalidate that field knowledge.
Callbacks registered through modeled APIs such as `t.Cleanup` retain their
effects. Unsupported bodies and analysis work limits use the conservative
effect summary; they never establish eligibility by dropping a path.
Generated functions supply the same effect and decision summaries as handwritten
code, while diagnostics in generated files remain suppressed.

For tests with local workers, an SSA refinement also follows fresh allocations,
field aliases, concrete interface receivers, and public `sync/atomic` load/store
contracts. A field has a known value only when it has no writes or one write
that dominates the read, including writes completed before a worker starts.
Escapes, multiple writes, and writes after worker launch leave the value unknown.
Escape propagation uses a separate graph of possible references through fields,
loads, stores, interfaces, and closure captures. Cycles and overwritten reference
fields retain every possible alias, even when their current value is unknown.
Unknown targets remain in the graph; a write establishes a value only when its
address has exactly one target and no unknown alternatives.
The graph also indexes possible writes, so checking a field does not recursively
resolve every store's address again. If a discovered call target becomes unknown,
its inputs escape and its conservative effects remain; independent state proofs
can still be used.
Public atomic updates have no external blocking effect. Updates whose stored or
returned values are not modeled invalidate their input value facts; a branch
depending on those values remains unknown. Raw atomic loads and stores share the
ordinary memory cell, including initialization before a worker starts.

Concrete interface methods use their package's effect summaries. Promoted methods
use SSA's receiver adjustments rather than assuming that the embedded receiver
matches the outer object. Referenced method facts are cached during their owning
analysis pass; decision payloads are decoded only when a caller needs them.

This refinement does not simulate scheduling or unroll loops. Recursive call
edges and call-context limits invalidate the callee's inputs and retain its
conservative effects. Loops and unmodeled callback registrations retain their
caller's summary without discarding independent state proofs elsewhere in the
test. Related source locations record excluded branches and explain when a
function summary or an analysis work limit prevented refinement.

When refining a test, inline subtests are analyzed as separate proposed bubbles. Their parent
captures remain unknown: an object, channel, or `testing.T.Context` from the
parent cannot establish ownership inside the subtest's bubble. Suggestions for
a subtest concern its callback body, leaving the outer `t.Run` outside the bubble.

Remaining effects on conditional paths are reported as unproven: a network path may be
unreachable for particular arguments. Clearing a finalizer with
`runtime.SetFinalizer(value, nil)` does not register an outside callback. Unknown
calls remain unknown, and successful trial runs do not change that assessment.

For golangci-lint, set `explain-migration: true` alongside `checks` in the
custom linter settings to include the same audit diagnostics.
The plugin includes evidence in each diagnostic's message so automatic fixes and
exclusions apply to the whole issue.

Related information also lists the possible `Test*` entry points for each
diagnostic. One helper location can belong to several tests, and one test can
have several locations. Diagnostic counts therefore do not count tests or
runtime subtest invocations. Entry attribution follows the static call graph;
it does not establish that each path executes for a particular argument.

## Automatic fixes

Preview supported fixes without changing files:

```sh
bubblelint -checks=all -fix -diff ./...
```

Apply supported fixes, check for remaining findings, and run your tests:

```sh
bubblelint -checks=all -fix ./...
bubblelint -checks=all ./...
go test ./...
```

This removes unsupported `t.Parallel()` statements directly inside inline
`synctest.Test` callbacks and migrates eligible concurrent tests to synctest.

Migration fixes cover simple tests whose goroutines finish by closing or sending
to local channels:

- **ST100:** the test receives every result before checking assertions.
- **ST101:** a two-case `select` waits for the final worker or a constant
  `time.After` timeout. The worker must be guaranteed to signal before the timeout.
  A local worker may close its result channel as its final statement or with
  `defer close(ch)` before a sequence of constant-duration sleeps.

The fix adds the import and wraps the test body. Existing `Sleep` calls,
`select` branches, timeout values, and assertions stay intact; time advances
virtually inside the test.

Some tests with helpers, shared state, or more complex control flow can still
receive ST100 suggestions without a fix. ST101 skips tests outside its verified
protocol. ST102 is review-only. Review the diff and run your tests after
applying fixes.

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
          checks: [all] # Optional: also enable migration suggestions.
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
