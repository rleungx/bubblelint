# bubblelint

A linter for Go tests using `testing/synctest`. Find incorrect synctest calls
and get optional suggestions for tests that could use virtual time.

## Get started

Requires Go 1.26 or newer. Use a newer toolchain if your project requires it.

```sh
go install github.com/rleungx/bubblelint/cmd/bubblelint@v0.1.0
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

These checks run by default:

| Code | What it finds |
| --- | --- |
| ST001 | `synctest.Wait` or Go 1.27+ `synctest.Sleep` called outside a bubble. |
| ST002 | A synctest bubble started inside another bubble. |
| ST003 | `Run`, `Parallel`, or `Deadline` called on the `*testing.T` provided by `synctest.Test`. |

To also find tests that may benefit from synctest:

```sh
bubblelint -suggest ./...
```

This adds **ST100** for sleeps after starting asynchronous work and **ST101**
for channel checks that use timeouts. Review these suggestions before applying
them, especially when a test depends on real network I/O or timing behavior.

## Automatic fixes

Automatic fixes are available on `main`. Install the latest code, apply fixes,
then check for any remaining findings:

```sh
go install github.com/rleungx/bubblelint/cmd/bubblelint@main
bubblelint -fix ./...
bubblelint ./...
```

This removes unsupported `t.Parallel()` statements directly inside inline
`synctest.Test` callbacks.

To also migrate simple concurrent tests to `synctest.Test`:

```sh
bubblelint -suggest -fix ./...
bubblelint -suggest ./...
go test ./...
```

Migration fixes cover ST100 tests whose goroutines finish by closing or sending
to local channels, and whose main test receives every result before checking
assertions. The fix adds the import and wraps the test body, preserving its
`Sleep` calls and assertions. Sleeps then use virtual time.

Tests with helpers, external I/O, shared state, or more complex control flow
remain suggestions. ST101 timeout checks still need manual migration. Review
the diff and run your tests after applying fixes.

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
          check-misuse: true # Check incorrect synctest calls.
          suggest: false # Set to true for migration suggestions.
```

Run the custom binary in your project's directory:

```sh
cd /path/to/your/project
/path/to/bubblelint/bin/golangci-lint-bubble run ./...
```

Add `--fix` to apply the same automatic fixes:

```sh
/path/to/bubblelint/bin/golangci-lint-bubble run --fix ./...
```

Set `suggest: true` in the settings above to include migration fixes.

</details>

## License

[Apache-2.0](LICENSE).
