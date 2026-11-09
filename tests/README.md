# Tests

- `integration/`: suites exercising multiple Funnel components, grouped by feature.
- `e2e/`: process, Kubernetes, and Gen3 tests requiring external services.
- `benchmarks/`: server submission benchmarks.
- `testdata/config/`: backend configurations used by Makefile targets.

Unit tests and their fixtures belong beside the package they test. The shared
server harness lives in `tests/internal/testenv`, where Go restricts imports to
the tests tree. Config and MongoDB test support live under their owning internal
packages. Kubernetes resource unit tests run with
`internal/compute/kubernetes/resources`; the real cluster smoke test runs through
`tests/e2e/kubernetes/run.sh`, including in CI.

## Integration test artifacts

The integration harness removes its generated databases, worker directories, and
local storage files after each test or suite. Servers, clients, and database
handles close before their files are removed. Tracked fixtures under `testdata/`
and `fixtures/` are not cleanup targets.

To retain harness artifacts for debugging, pass `-funnel-keep-artifacts` to a test
package that uses the harness:

```sh
go test ./tests/integration/auth -count=1 -v -args -funnel-keep-artifacts
```

The verbose output lists the retained absolute paths under `test_tmp/`. Retention
keeps files only. It still stops servers and closes database handles. The flag
does not change Go's automatic cleanup of directories created with `t.TempDir()`.
An unhandled test panic or forced process termination can bypass shared
`TestMain` teardown. Per-test fixtures use Go's cleanup hooks.

Pass `t` to `testenv.DefaultConfig(t)` and `testenv.NewFunnel(t, conf)` in individual
tests. Both register cleanup immediately. For shared `TestMain` fixtures, call
`testenv.RunSuite` and pass its cleanup scope to the same helpers. Return `false`
from suite setup when the required integration backend is unavailable. Call
`os.Exit` only after `RunSuite` returns so teardown can finish.

Cleanup removes only the unique directories allocated by the harness. It does
not remove the whole `test_tmp/` directory, which another test process may use.
