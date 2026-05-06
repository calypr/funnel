# Overview

With the exception of `funnel-server.yaml` (which is expected to be handled by Helm), the following files are pulled directly from the `charts/funnel/files` directory in the Helm Charts repo:

Helm Chart files:
> https://github.com/ohsu-comp-bio/helm-charts/tree/funnel-0.1.60/charts/funnel/files

Default Config:
> https://pkg.go.dev/github.com/ohsu-comp-bio/funnel/config#DefaultConfig

After updating any of these files, run `make bundle-examples` to compile them into the `config` package (`config/internal/bundle.go`) to make them "available" as calls to `intern.MustAsset(example.yaml)`.
