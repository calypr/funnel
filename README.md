[![Build Status][build-badge]][build]
[![Compliance Tests Status][compliance-tests-badge]][compliance-tests]
[![Gitter][gitter-badge]][gitter]
[![License: MIT][license-badge]][license]
[![Godoc][godoc-badge]][godoc]

[build-badge]: https://img.shields.io/github/actions/workflow/status/ohsu-comp-bio/funnel/tests.yaml
[build]: https://github.com/ohsu-comp-bio/funnel/actions/workflows/tests.yaml

[compliance-tests]: https://github.com/ohsu-comp-bio/funnel/actions/workflows/compliance-test.yaml
[compliance-tests-badge]: https://img.shields.io/github/actions/workflow/status/ohsu-comp-bio/funnel/compliance-test.yaml?label=Compliance%20Tests

[gitter-badge]: https://badges.gitter.im/ohsu-comp-bio/funnel.svg
[gitter]: https://gitter.im/ohsu-comp-bio/funnel

[license-badge]: https://img.shields.io/badge/License-MIT-yellow.svg
[license]: https://opensource.org/licenses/MIT

[godoc-badge]: https://img.shields.io/badge/godoc-ref-blue.svg
[godoc]: http://godoc.org/github.com/ohsu-comp-bio/funnel

<a title="Funnel Homepage" href="https://ohsu-comp-bio.github.io/funnel">
  <img title="Funnel Logo" src="https://github.com/user-attachments/assets/f51cf06b-d802-4e20-bde1-bcd1fc5657e6" />
</a>

Funnel is a toolkit for distributed, batch task execution, including a server, worker, and a set of compute, storage, and database backends. Given a task description, Funnel will find a worker to execute the task, download inputs, run a series of (Docker) containers, upload outputs, capture logs, and track the whole process.

Funnel is an implementation of the [GA4GH Task Execution Schemas](https://github.com/ga4gh/task-execution-schemas), an effort to standardize the APIs used for task execution across many platforms.

Funnel provides an API server, multiple storage backends (local FS, S3, Google Bucket, etc.), multiple compute backends (local, HTCondor, Google Cloud, etc.), and a web dashboard.

https://ohsu-comp-bio.github.io/funnel/

