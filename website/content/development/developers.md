---
title: Funnel Developers

menu:
  main:
    parent: Development
    weight: 30
---

# Developers

This page contains a rough collection of notes for people wanting to build Funnel from source and/or edit the code.

### Building the Funnel source

1. Install [Go 1.21+][go]. Check the version with `go version`.
2. Ensure GOPATH is set. See [the docs][gopath] for help. Also, you probably want to add `$GOPATH/bin` to your `PATH`.
3. Clone funnel and build

	```shell
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

### Go Tests

Run all tests: `make test`   
Run the worker tests: `go test ./worker/...`  
Run the worker tests with "Cancel" in the name: `go test ./worker -run Cancel`  

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

- Update Makefile, edit `FUNNEL_VERSION` and `LAST_PR_NUMBER`
  - `LAST_PR_NUMBER` can be found by looking at the previous release notes
    from the previous release.
- Run `make website`, which updates the download links and other content.
  - Check the website locally by running `make website-dev`
- Commit these changes.
  - Because goreleaser requires a clean working tree in git
  - This is a special case where it's easiest to commit to master.
- Create a git tag: `git tag X.Y.Z`
- Run `make release`
  - This will build cross-platform binaries, build release notes,
    and draft an unpublished GitHub release.
  - Check the built artifacts by downloading the tarballs from the GitHub draft release
    and running `funnel version`.
- `git push origin master` to push your website and release changes.
- A tagged docker image for the release will be built automatically on [dockerhub](https://hub.docker.com/repository/docker/quay.io/ohsu-comp-bio/funnel).
- Publish the draft release on GitHub.
- Copy `build/release/funnel.rb` to the `ohsu-comp-bio/homebrew-formula/Formula/funnel.rb` Homebrew formula repo, and push those changes to master.
