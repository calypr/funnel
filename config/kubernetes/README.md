# Kubernetes reference templates

These files are historical reference material. Funnel does not embed them or use them as runtime defaults. Configure Kubernetes templates through the deployment's Helm chart.

The reference files originated in the [Funnel Helm chart](https://github.com/ohsu-comp-bio/helm-charts/tree/funnel-0.1.60/charts/funnel/files).

Templates used by Go tests live beside their tests:

- `compute/kubernetes/testdata/worker-job.yaml`
- `compute/kubernetes/resources/testdata/serviceaccount.yaml`
- `worker/testdata/executor-job.yaml`
