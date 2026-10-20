# Kubernetes storage end-to-end test

Run `tests/kubernetes-storage/run.sh` from a machine with Go, Docker, k3d,
kubectl and Python 3. The script creates and deletes its own cluster.
Set `FUNNEL_E2E_OUTPUT_DIR` to retain task JSON, server logs, and cluster
resources at a known path. It uses a separate kubeconfig.

The `Go Tests` workflow runs `go test -race ./...` on pushes.
The `Kubernetes storage E2E` workflow runs on all pull requests and pushes to
`develop`. It runs this script.

The script runs `TestNativeStorageLive` and `TestNativeStoragePermissionsLive`
against its disposable cluster before the TES end-to-end test. It builds the
checked-out Funnel source into a container and imports that image into k3d. It
does not deploy a published Funnel chart or image.

The native test uses a nondefault `standard` StorageClass backed by k3d's
`rancher.io/local-path` provisioner. The script still checks that `local-path`
remains the default class.

## Design precedent

This test follows Argo Workflows' separation of Kubernetes volumes from S3
artifact transfers:

- [k3d-up.sh](https://github.com/argoproj/argo-workflows/blob/main/hack/tilt/k3d-up.sh)
  uses K3s without replacing its default storage provisioner.
- [volume-claim-template-workflow.yaml](https://github.com/argoproj/argo-workflows/blob/main/test/e2e/testdata/volume-claim-template-workflow.yaml)
  omits `storageClassName` and uses the cluster default.
- [artifacts_test.go](https://github.com/argoproj/argo-workflows/blob/main/test/e2e/artifacts_test.go)
  exercises artifacts at mounted paths, including PVCs and `emptyDir`.

The runner uses those conventions, with Funnel's TES API in place of Argo's
Workflow API. The cluster script checks that the default StorageClass uses
`rancher.io/local-path`. MinIO is an S3 HTTP service; no S3 CSI driver is
installed. A small Deployment and Service run MinIO with ephemeral storage and a
pinned image digest, following Argo's native test-fixture approach.

Funnel's worker and executor occupy separate Pods, so they share a PVC.
The executor also has an `emptyDir` for intermediate files. That directory
is local to the executor Pod, not a substitute for the shared workspace.

## Assertions

- Dynamic PVC templates generate task-specific claims and logical volumes.
- An existing PVC works as the task workspace and survives task cleanup.
- An explicit task-owned PV/PVC pair runs through the real TES API under a
  server ServiceAccount with PV create, get, and delete access but no PV list
  access. The test checks S3 outputs and waits for the worker Job, PVC, and PV
  to disappear through normal cleanup.
- A mounted reference selects the correct `subPath`. Its file exists only
  on the PVC, so an accidental S3 fallback cannot satisfy the test.
- An unmatched input downloads through GenericS3 from MinIO.
- A TES executor reads both inputs and uses its native `emptyDir` mount.
- Directory outputs upload through GenericS3, including nested and empty
  files. The test compares S3 contents and per-file TES output logs.
- The worker removes task-directory contents. An empty mount-point directory
  may remain on an existing claim. The reconciler removes the worker
  Job, and Kubernetes garbage-collects the task-owned PVC. Operator claims
  and reference files survive.
- Cancellation removes the running task and its owned workspace claim.
- Upload failure prevents task success.
- Both existing and templated `ReadOnlyMany` output claims fail before a
  worker Job or workspace PVC is created.

The native test covers dynamic claims, operator-managed claims, explicit PV
and PVC pairs, and recovery when the persisted ownership annotation is lost.
This suite tests filesystem mounts and API-backed artifact transfers. It does
not emulate asynchronous mounted S3 publication, certify a CSI driver, or
replace the Gen3 deployment integration test.
