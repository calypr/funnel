# Unreleased

### Added

* CLI configuration loading supports `FUNNEL_CONFIG` for the default config file path and `FUNNEL_LOG_LEVEL` to override the configured log level. Explicit flags take precedence.

### Fixed

* The installer supports destination paths containing spaces, extracts in a temporary directory, and fails on HTTP download errors with bounded retries.

* Release packaging uses a pinned GoReleaser executable, includes the license and upgrade guide, and can be verified with the manual Release packaging workflow. Manual release dispatch supports a dry run with downloadable archives and validates the requested tag.

* PostgreSQL initialization cancels stalled network operations when its existing five-minute initialization budget expires.
* Net/rpc authorization plugins preserve explicitly declared `FILE` output types and other optional protobuf fields in requests and responses. Rebuild the host and plugin binaries together.
* Manual release dispatch checks out the requested tag instead of the selected branch.

### Breaking changes and upgrade preparation

See the [Funnel upgrade guide](docs/upgrading.md) for import replacements, plugin
rebuilds, configuration changes, and deployment preparation.

* Public Go contracts now live in `api/tes`, `api/config`, `api/logger`, and `api/plugin`; the REST client lives in `client/tes`. Application implementations are private under `internal/`. Update old imports and rebuild external consumers and plugins. No forwarding packages retain the old APIs.
* The TES marshaler globals and wrappers have been removed. Use protobuf `protojson` calls instead. The Go REST client and CLI retain support for `FUNNEL_SERVER_*` and `FUNNEL_ALLOW_INSECURE_CREDENTIALS` environment variables.
* Plugin configuration responses can now clear inherited empty values. Plugins must return the complete edited host configuration rather than partial configuration objects.
* Configurations using retired Kubernetes storage keys must migrate to native objects under `Kubernetes.Storage`. Update mounted routes, rebuild plugins, and finish and clean up Jobs with version 1, 2, or 3 storage plans using a compatible older binary before upgrading. Legacy PV, PVC, and Job templates remain supported when native storage configuration is absent. See the [Kubernetes storage migration](docs/compute/kubernetes-storage.md#ownership-and-upgrades).
* HPC backends require private, persistent `HPCStateDir` submission records. FTP deployments using configured credentials must set `FTPStorage.Endpoint`. Custom runtime templates using `index .Env` or `index .Tags` must migrate to the documented argument helpers or supported iteration patterns. See the [upgrade guide](docs/upgrading.md#migrate-deployment-configuration).
* Bundled deployment automation has been removed, including GCE provisioning scripts, VM installers, and systemd service examples. Operators must maintain their own deployment code. Container images and runtime backends remain available.
* Elasticsearch support has been removed. Remove the entire `Elastic` section from existing configuration files before upgrading, even when another database backend is selected. Older full configurations can contain this unused section; leaving it present causes startup to fail with `unknown field "Elastic"`. Deployments using Elasticsearch must migrate their data to a supported backend before upgrading.
* MongoDB now requires a replica set or sharded cluster for atomic task assignment and resource release. Standalone servers are rejected at startup. Convert existing standalone deployments before upgrading; a single-node replica set is sufficient for development. See the [MongoDB upgrade guide](docs/databases/mongodb.md#upgrade-an-existing-standalone-server).
* Authenticated remote plaintext gRPC connections are now rejected by default. Use a secure tunnel terminating on loopback, or explicitly set `FUNNEL_ALLOW_INSECURE_CREDENTIALS=true` in connecting processes for a trusted private deployment. Authenticated Kubernetes workers using a service DNS address need the tunnel or the explicit setting in their worker template. The setting also permits authenticated HTTP downgrades. See the [remote server guide](docs/integrations/remote-server.md).
* Node listings are now paginated. An empty `ListNodes` request or `GET /v1/nodes` returns at most 500 nodes by default. Follow `next_page_token` by supplying `page_token` until the returned token is empty. Set `page_size` from 1 through 1000 to request an explicit page size. Upgrade or regenerate older gRPC clients to access these fields; consumers that issue only one request can miss nodes in larger deployments.

# [0.12.0](https://github.com/ohsu-comp-bio/funnel/compare/0.11.0...0.12.0) (2025-08-27)


### Bug Fixes

* add AmazonS3 SSE in default config and values file ([e3a9bfd](https://github.com/ohsu-comp-bio/funnel/commit/e3a9bfdf83bf1ed6792ada336fceedcd3fad4111))
* add AmazonS3 SSE in default config and values file ([beb26b9](https://github.com/ohsu-comp-bio/funnel/commit/beb26b9ae22b2a2e8f2adcfc8bc5a3e837650922))
* add missing preemptionPolicy to Priority Class ([0d676fe](https://github.com/ohsu-comp-bio/funnel/commit/0d676fea6a53bd40bf078faaa0cf9e00f1f2917e))
* add nil check for JSON formatter config ([cb5c911](https://github.com/ohsu-comp-bio/funnel/commit/cb5c911d970921ff6cfeecd58ae532c2e6ecaacc))
* base64 encoding in utils_test.go ([ea895a7](https://github.com/ohsu-comp-bio/funnel/commit/ea895a7582f3d364d1b34f3d4fca90c36dfc146a))
* base64 encoding in utils_test.go ([fad3ca5](https://github.com/ohsu-comp-bio/funnel/commit/fad3ca500a90f9cdf2c066a25370253ee8cff6fa))
* call to plugin client ([1406437](https://github.com/ohsu-comp-bio/funnel/commit/140643784632fed3cba863ba86b485ab777f59c4))
* deps ([dc27c87](https://github.com/ohsu-comp-bio/funnel/commit/dc27c87e6a61e4e41c2613c953f655170eb99450))
* generic_s3: `function not implemented` error when calling `Put()` ([ae59798](https://github.com/ohsu-comp-bio/funnel/commit/ae5979839f88b868833c436c0bac8212cab87fdb))
* generic_s3: `function not implemented` error when calling `Put()` ([83bebfb](https://github.com/ohsu-comp-bio/funnel/commit/83bebfb474a2f304991b703559915c39b1ae5f50))
* linting fix in tests ([3da1a04](https://github.com/ohsu-comp-bio/funnel/commit/3da1a04af556f4d11938b3010458c9dc8a0de3e9))
* replace deprecated `EventsOptions` with `ListOptions` ([72c96f1](https://github.com/ohsu-comp-bio/funnel/commit/72c96f1a096a3d0de832adc5aeeca82019edb022))
* run `go mody tidy` ([07c6070](https://github.com/ohsu-comp-bio/funnel/commit/07c60704a3ec295e48d17603941dfd7eeb3c7d65))
* update call to kubernetes.NewBackend ([d6960cd](https://github.com/ohsu-comp-bio/funnel/commit/d6960cd915881f0bde323ec6f50fa7814333a8ad))
* update docker.yaml ([9d11df9](https://github.com/ohsu-comp-bio/funnel/commit/9d11df94d2465b3974fa931142cd93ed498beb84))
* update Minio's `GetObject` in generic_s3.go ([bdc1256](https://github.com/ohsu-comp-bio/funnel/commit/bdc1256ceb8fa4c637efa66b914f3fbdb151be5c))
* update tag format in .releaserc.yaml ([26acb72](https://github.com/ohsu-comp-bio/funnel/commit/26acb72122d279db7066d4595f7100a937d314ec))
* update tests ([0bd425e](https://github.com/ohsu-comp-bio/funnel/commit/0bd425e1619881f5fd015eee2c24a1e75c2d9ea2))
* update tests to only create K8s backend if using K8s as compute ([9a4cf03](https://github.com/ohsu-comp-bio/funnel/commit/9a4cf03338547bb3cdc3469526aa0b5021ac7960))


### Features

* add additional debug logging for s3 storage ([dd027ce](https://github.com/ohsu-comp-bio/funnel/commit/dd027cea2ffdcb659dcad4a497fa2a82de350884))
* add additional debugging logging to generic S3 storage ([2f2257d](https://github.com/ohsu-comp-bio/funnel/commit/2f2257ddcb4108a642e13293405278902f793572))
* add initial SemVer release workflow ([cf23fb0](https://github.com/ohsu-comp-bio/funnel/commit/cf23fb07112d6c1b666cc93f6bc88ded9e61cdc0))
* add stack trace to logging to output panic information ([d0742bb](https://github.com/ohsu-comp-bio/funnel/commit/d0742bbecf8ed61b51260888d9f8c01282632c90))
* generic_s3: add object path + bucket to error logs ([22f1d54](https://github.com/ohsu-comp-bio/funnel/commit/22f1d545bf8544e65282b8ae2f9839fa364dfba4))
* s3: add support for custom S3 endpoints with suffixes ([ed12139](https://github.com/ohsu-comp-bio/funnel/commit/ed12139fdf066c1b698f0c62eecd3ad0279ac0f6))
* update Mongo image registry to AWS ECR (avoid docker.io rate limit) ([38d0e3c](https://github.com/ohsu-comp-bio/funnel/commit/38d0e3c2b0c31445ae12e7898b45f50c5559c997))
* update plugin response to include error message ([e7479a8](https://github.com/ohsu-comp-bio/funnel/commit/e7479a8542e7ccc8eff0443b33eb63d4a4b03636))

# 1.0.0 (2025-08-25)


### Bug Fixes

* add AmazonS3 SSE in default config and values file ([e3a9bfd](https://github.com/ohsu-comp-bio/funnel/commit/e3a9bfdf83bf1ed6792ada336fceedcd3fad4111))
* add AmazonS3 SSE in default config and values file ([beb26b9](https://github.com/ohsu-comp-bio/funnel/commit/beb26b9ae22b2a2e8f2adcfc8bc5a3e837650922))
* add missing preemptionPolicy to Priority Class ([0d676fe](https://github.com/ohsu-comp-bio/funnel/commit/0d676fea6a53bd40bf078faaa0cf9e00f1f2917e))
* add nil check for JSON formatter config ([cb5c911](https://github.com/ohsu-comp-bio/funnel/commit/cb5c911d970921ff6cfeecd58ae532c2e6ecaacc))
* base64 encoding in utils_test.go ([ea895a7](https://github.com/ohsu-comp-bio/funnel/commit/ea895a7582f3d364d1b34f3d4fca90c36dfc146a))
* base64 encoding in utils_test.go ([fad3ca5](https://github.com/ohsu-comp-bio/funnel/commit/fad3ca500a90f9cdf2c066a25370253ee8cff6fa))
* call to plugin client ([1406437](https://github.com/ohsu-comp-bio/funnel/commit/140643784632fed3cba863ba86b485ab777f59c4))
* deps ([dc27c87](https://github.com/ohsu-comp-bio/funnel/commit/dc27c87e6a61e4e41c2613c953f655170eb99450))
* generic_s3: `function not implemented` error when calling `Put()` ([ae59798](https://github.com/ohsu-comp-bio/funnel/commit/ae5979839f88b868833c436c0bac8212cab87fdb))
* generic_s3: `function not implemented` error when calling `Put()` ([83bebfb](https://github.com/ohsu-comp-bio/funnel/commit/83bebfb474a2f304991b703559915c39b1ae5f50))
* linting fix in tests ([3da1a04](https://github.com/ohsu-comp-bio/funnel/commit/3da1a04af556f4d11938b3010458c9dc8a0de3e9))
* replace deprecated `EventsOptions` with `ListOptions` ([72c96f1](https://github.com/ohsu-comp-bio/funnel/commit/72c96f1a096a3d0de832adc5aeeca82019edb022))
* run `go mody tidy` ([07c6070](https://github.com/ohsu-comp-bio/funnel/commit/07c60704a3ec295e48d17603941dfd7eeb3c7d65))
* update call to kubernetes.NewBackend ([d6960cd](https://github.com/ohsu-comp-bio/funnel/commit/d6960cd915881f0bde323ec6f50fa7814333a8ad))
* update Minio's `GetObject` in generic_s3.go ([bdc1256](https://github.com/ohsu-comp-bio/funnel/commit/bdc1256ceb8fa4c637efa66b914f3fbdb151be5c))
* update tests ([0bd425e](https://github.com/ohsu-comp-bio/funnel/commit/0bd425e1619881f5fd015eee2c24a1e75c2d9ea2))
* update tests to only create K8s backend if using K8s as compute ([9a4cf03](https://github.com/ohsu-comp-bio/funnel/commit/9a4cf03338547bb3cdc3469526aa0b5021ac7960))


### Features

* add additional debug logging for s3 storage ([dd027ce](https://github.com/ohsu-comp-bio/funnel/commit/dd027cea2ffdcb659dcad4a497fa2a82de350884))
* add additional debugging logging to generic S3 storage ([2f2257d](https://github.com/ohsu-comp-bio/funnel/commit/2f2257ddcb4108a642e13293405278902f793572))
* add initial SemVer release workflow ([cf23fb0](https://github.com/ohsu-comp-bio/funnel/commit/cf23fb07112d6c1b666cc93f6bc88ded9e61cdc0))
* add stack trace to logging to output panic information ([d0742bb](https://github.com/ohsu-comp-bio/funnel/commit/d0742bbecf8ed61b51260888d9f8c01282632c90))
* generic_s3: add object path + bucket to error logs ([22f1d54](https://github.com/ohsu-comp-bio/funnel/commit/22f1d545bf8544e65282b8ae2f9839fa364dfba4))
* s3: add support for custom S3 endpoints with suffixes ([ed12139](https://github.com/ohsu-comp-bio/funnel/commit/ed12139fdf066c1b698f0c62eecd3ad0279ac0f6))
* update Mongo image registry to AWS ECR (avoid docker.io rate limit) ([38d0e3c](https://github.com/ohsu-comp-bio/funnel/commit/38d0e3c2b0c31445ae12e7898b45f50c5559c997))
* update plugin response to include error message ([e7479a8](https://github.com/ohsu-comp-bio/funnel/commit/e7479a8542e7ccc8eff0443b33eb63d4a4b03636))
