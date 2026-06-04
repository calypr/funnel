[![Tests][tests-badge]][tests]
[![Compliance Tests Status][compliance-tests-badge]][compliance-tests]
[![License: MIT][license-badge]][license]
[![Godoc][godoc-badge]][godoc]
[![GitHub Release][release-badge]][release]
[![Docker][docker-badge]][docker]
[![Helm][helm-badge]][helm]

[tests-badge]: https://github.com/calypr/funnel/actions/workflows/tests.yaml/badge.svg
[tests]: https://github.com/calypr/funnel/actions/workflows/tests.yaml

[compliance-tests]: https://github.com/ohsu-comp-bio/funnel/actions/workflows/compliance.yaml
[compliance-tests-badge]: https://img.shields.io/github/actions/workflow/status/ohsu-comp-bio/funnel/compliance.yaml?label=TES%20Compliance

[license-badge]: https://img.shields.io/badge/License-MIT-yellow.svg
[license]: https://opensource.org/licenses/MIT

[godoc-badge]: https://img.shields.io/badge/godoc-ref-blue.svg
[godoc]: http://godoc.org/github.com/ohsu-comp-bio/funnel

[release-badge]: https://img.shields.io/github/v/release/ohsu-comp-bio/funnel
[release]: https://github.com/ohsu-comp-bio/funnel/releases

[docker-badge]: https://img.shields.io/badge/Docker%20Repo-Quay.io-blue?logo=docker
[docker]: https://quay.io/repository/ohsu-comp-bio/funnel?tab=tags&tag=latest

[helm-badge]: https://img.shields.io/badge/Helm-0F1689?logo=helm&logoColor=fff
[helm]: https://github.com/ohsu-comp-bio/helm-charts

<a title="Funnel Homepage" href="https://calypr.org/tools/funnel/">
  <img title="Funnel Logo" src="https://github.com/user-attachments/assets/f51cf06b-d802-4e20-bde1-bcd1fc5657e6" />
</a>

Funnel is a toolkit for distributed, batch task execution, including a server, worker, and a set of compute, storage, and database backends. Given a task description, Funnel will find a worker to execute the task, download inputs, run a series of (Docker) containers, upload outputs, capture logs, and track the whole process.

Funnel is an implementation of the [GA4GH Task Execution Schemas](https://github.com/ga4gh/task-execution-schemas), an effort to standardize the APIs used for task execution across many platforms.

Funnel provides an API server, multiple storage backends (local FS, S3, Google Bucket, etc.), multiple compute backends (local, HTCondor, Google Cloud, etc.), and a web dashboard.

https://calypr.org/tools/funnel/
