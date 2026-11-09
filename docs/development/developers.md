# Developers

This page contains a rough collection of notes for people wanting to build Funnel from source and/or edit the code.

### Building the Funnel source

1. Install [Go 1.21+][go]. Check the version with `go version`.
2. Ensure GOPATH is set. See [the docs][gopath] for help. Also, you probably want to add `$GOPATH/bin` to your `PATH`.
3. Clone funnel and build

	```sh
	$ git clone https://github.com/ohsu-comp-bio/funnel.git
	$ cd funnel
	$ make
	```

4. Funnel is now downloaded and installed. Try `funnel version`.
5. You can edit the code and run `make install` to recompile.

### Developer Tools

A Funnel development environment includes:

- [Go 1.21+][go] for the majority of the code.
- [Task Execution Schemas][tes] for task APIs.
- [Protobuf][protobuf] + [gRPC][grpc] for RPC communication.
- [gRPC Gateway][gateway] for HTTP communication.
- [Angular][angular] and [SASS][sass] for the web dashboard.
- [GNU Make][make] for development tasks.
- [Docker][docker] for executing task containers (tested with v1.12, v1.13).
- [dep][dep] for Go dependency vendoring.
- [Make][make] for development/build commands.
- [NodeJS][node] and [NPM][npm] for web dashboard development.

### Makefile

Most development tasks are run through `make` commands, including build, release, testing, website docs, lint, tidy, webdash dev, and more.  See the [Makefile](https://github.com/ohsu-comp-bio/funnel/blob/master/Makefile) for an up-to-date list of commands.

### Error handling

The Go static-analysis CI job runs `errcheck` for production code and tests,
including errors assigned to `_`. Handle errors that can affect results or
propagate them to the caller. Test setup and assertions must also check errors.

An intentional exception must be local and name the linter, with a concrete
reason. For example, a read-only file close after a completed read may use:

```go
_ = input.Close() //nolint:errcheck // Closing this read-only input cannot invalidate the bytes already read.
```

CI rejects unexplained or nonspecific suppressions. Do not add package-wide
exclusions, generic ignore-error helpers, or log-and-continue code merely to
silence the check. A failed write, upload, parse, or state update normally needs
handling. Expected rollback after commit and best-effort cleanup require an
explicit decision at the call site.

### Unreachable Go code

Run `make deadcode` for the same check used by CI. It runs a pinned analyzer for
Linux/amd64 with tests included. It analyzes test entry points without executing
tests. Generated functions are excluded by the analyzer's default settings.

The existing findings are recorded in `.github/deadcode-baseline.txt`. New
findings fail CI for review. Unreachable code may represent unfinished wiring,
external API contracts, or obsolete functionality. The check never deletes code.

Wire up intended behavior or remove confirmed obsolete code. If a finding is
intentional, explain it in the pull request before adding it to the baseline.
Run `make deadcode-update` to refresh the baseline and review the diff. CI also
rejects stale entries when findings disappear, so the baseline can shrink.

### Go Tests

Run all tests: `make test`
Run the worker tests: `go test ./internal/worker/...`
Run the worker tests with "Cancel" in the name: `go test ./internal/worker -run Cancel`

You get the idea. See the `go test` docs for more.

### Mocking

The [testify][testify] and [mockery][mockery] tools are used to generate and use
mock interfaces in test code, for example, to mock the Google Cloud APIs.

[go]: https://golang.org
[angular]: https://angularjs.org/
[protobuf]: https://github.com/google/protobuf
[grpc]: http://www.grpc.io/
[sass]: http://sass-lang.com/
[make]: https://www.gnu.org/software/make/
[docker]: https://docker.io
[python]: https://www.python.org/
[dep]: https://golang.github.io/dep/
[node]: https://nodejs.org
[npm]: https://www.npmjs.com/
[gateway]: https://github.com/grpc-ecosystem/grpc-gateway
[tes]: https://github.com/ga4gh/task-execution-schemas
[testify]: https://github.com/stretchr/testify
[mockery]: https://github.com/vektra/mockery
[gopath]: https://golang.org/doc/code.html#GOPATH

### Making a release

Releases are published by the [Publish Funnel release workflow](https://github.com/calypr/funnel/actions/workflows/release.yaml).
It runs when a `v*.*.*` tag is pushed, and also supports **Run workflow** or `gh workflow run`.
The workflow checks out the requested tag, rather than publishing the selected branch.

1. Merge the reviewed PR stack into `develop` and confirm its CI passes.
2. Review `CHANGELOG.md` and [the upgrade guide](../upgrading.md). Include import migrations, configuration migrations, and the requirement to rebuild net/rpc plugins with the host. No Makefile version edit is needed: release versions come from tags.
3. Choose an unused version. Check both Git tags and GitHub releases: an existing tag without a release still names older code. Do not move it to new code. In this repository `v0.12.0` already exists.
4. Before tagging, run `make snapshot` or manually dispatch the **Release packaging** workflow on the candidate branch. Packaging is not a pull-request check. This builds archives and checksums without publishing. Extract the native archive into a temporary directory and check `funnel version`, `funnel --help`, and basic task execution. Check that `docs/upgrading.md` is included. Snapshot versions include a development suffix.
5. Create an RC tag on the reviewed commit, for example `git tag -a vNEXT-rc.1 -m 'Release candidate'`, replacing `NEXT` with the chosen numeric version. Pushing the tag triggers publication immediately. RC tags become GitHub prereleases.
6. To validate an existing tag without publishing, select **Run workflow**, enter its tag, and leave **dry_run** enabled. The archives are retained as workflow artifacts. From the CLI:

   ```sh
   gh workflow run release.yaml --repo calypr/funnel --ref develop \
     -f tag=vNEXT-rc.1 -F dry_run=true
   ```

   The workflow must first be available on the repository's default branch for GitHub to offer manual dispatch. For a local tag that has not been pushed, use the local snapshot check instead.
7. Test the RC using `bash install.sh vNEXT-rc.1 /tmp/funnel-rc-bin` from a temporary working directory. Check the reported version and run a representative task with your deployment configuration.
8. Tag the validated commit with the final version and push that tag. The release is published automatically, not saved as a draft. If retrying publication for an existing tag manually, set **dry_run** to false. Inspect existing assets first; do not overwrite a published version with different code.

The separate container workflow publishes multi-architecture images to
`quay.io/ohsu-comp-bio/funnel` on pushes. Stable version tags update `latest`;
RC tags do not. Verify the image workflow as well as the binary release workflow.
Manual binary publication does not rerun the container workflow.

`make release` also publishes from a clean tagged checkout using `GITHUB_TOKEN`.
Prefer the GitHub workflow for publication. Release notes come from the reviewed `Unreleased` section of `CHANGELOG.md`.
Both local and CI packaging use the
GoReleaser version pinned in the Makefile and its explicit executable path.
