---
title: Mounted inputs
menu:
  main:
    parent: Storage
---

# Mounted inputs

`GenericS3.MountedRead` and `GenericS3.MountedWrite` let a Kubernetes worker read inputs or publish outputs through PVC-backed filesystem mounts. Each setting names a configured storage volume. If that volume has multiple worker mounts, use `SubPath` to select the mount whose static Kubernetes `subPath` matches exactly. Read and write routes are optional and can use separate volumes or mount views.

> **Warning:** `MountedWrite` checks only whether an S3 object exists with the expected size. If an older object at the output URL has the same size, Funnel can report the task as complete before the new output is visible in S3. Use the regular S3 API upload path when output URLs can be reused or when you need confirmation that the new content was published. Omit `MountedWrite` to use API uploads.

> Mounted reads also depend on the storage driver presenting a fresh view of the configured bucket and prefix. Funnel cannot verify that the mount matches the S3 URL.

```yaml
GenericS3:
  - Disabled: false
    Endpoint: https://s3.example.org
    Bucket: analysis-data
    Key: storage-key
    Secret: storage-secret
    Region: us-east-1
    MountedRead:
      Volume: reference-data
      SubPath: genomes
      Prefix: genomes/
    MountedWrite:
      Volume: result-data
      Prefix: results/
      Timeout: 2700s

Kubernetes:
  JobsNamespace: funnel-tasks
  Storage:
    WorkspaceVolume: workspace
    volumes:
      - name: workspace
        persistentVolumeClaim:
          claimName: task-workspace
      - name: reference-data
        persistentVolumeClaim:
          claimName: reference-pvc
      - name: result-data
        persistentVolumeClaim:
          claimName: task-results
    volumeMounts:
      - name: reference-data
        mountPath: /mnt/reference
        readOnly: true
        subPath: genomes
      - name: reference-data
        mountPath: /mnt/annotations
        readOnly: true
        subPath: annotations
      - name: result-data
        mountPath: /mnt/results
```

The two reference mounts use one PVC at different paths. `MountedRead.SubPath: genomes` selects the first view. `MountedWrite` omits `SubPath` because `result-data` has one worker mount. If a volume has several mounts, set `SubPath` to the exact static `volumeMounts[].subPath` value. Set `SubPath: ""` to select a static root mount. A mount with `subPathExpr` can be selected only when it is the unique mount for that volume and `SubPath` is absent.

`Prefix` is relative to the bucket. An empty prefix selects the whole bucket. The selected mount's authored `mountPath` is the route root. For example, `s3://analysis-data/genomes/genome.fa` maps to `/mnt/reference/genome.fa`. Funnel does not append the selected subpath or prefix to `mountPath`. The storage driver and operator must make the mounted view correspond to the configured bucket prefix and provide the required freshness.

`MountedWrite` uses the same volume, subpath, and prefix rules. Its selected mount must be writable, and the underlying PVC must permit writes. Before creating task resources, Funnel checks both existing claims and PVC templates for `ReadWriteOnce`, `ReadWriteMany`, or `ReadWriteOncePod` access. A claim with only `ReadOnlyMany` cannot serve mounted outputs. The workspace still cannot use `ReadWriteOncePod` because worker and executor Pods share it. Funnel appends an output key path relative to the configured prefix when it copies a TES output file into the mount.

For native storage configuration details, including existing claims, StorageClass-provisioned claims, and explicit PV/PVC pairs, see [Kubernetes storage](../../compute/kubernetes-storage/).

## TES behavior

Clients continue to submit ordinary TES inputs. They do not supply PVC names, volume names, worker mount paths, or deployment paths.

```json
{
  "inputs": [
    {
      "url": "s3://analysis-data/genomes/genome.fa",
      "path": "/inputs/genome.fa",
      "type": "FILE"
    }
  ],
  "executors": [
    {
      "image": "alpine:3.21",
      "command": ["cat", "/inputs/genome.fa"]
    }
  ]
}
```

The worker copies each input into the task workspace. For a matching GenericS3 mounted read, it reads the source from the selected filesystem mount. The executor sees the copy at `/inputs/genome.fa`. Changing that copy does not change the mounted source. Directory inputs preserve their relative filenames. Reference mounts remain worker-only. They do not change TES `volumes`, which retain their empty scratch-directory behavior.

## Read routing and safety

The worker activates a mounted read or write only when the submitted storage plan names the exact GenericS3 `Endpoint` and `Bucket`. The plan stores `S3ReadMounts` and `S3WriteMounts` with the endpoint, bucket, prefix, and mount root, without credentials. The worker ignores static `MountedRead` and `MountedWrite` settings, including when the submitted plan has no mounted routes. Configured mounted reads and writes require `Kubernetes.Storage` and its submitted plan. The API server's `NewMux` does not activate filesystem reads or writes.

GenericS3 handles inputs whose bucket-relative key matches its configured prefix. Funnel treats S3 keys literally. Spaces, `#`, and percent signs remain part of the key. For example, `a%20b.txt` names the literal file `a%20b.txt`. Prefix matching uses whole path components, so `genomes/` does not match `genomes-old/file.fa`. Unmatched URLs use the configured storage API path.

A matched file must exist beneath the selected mount root. Funnel rejects traversal, encoded traversal forms, and escaping symlinks through rooted filesystem operations. Regular-file symlinks within the root can be read. Directory listings reject directory symlinks and special files. A missing or unsafe matched input fails the transfer. Funnel does not fall back to the API for that input.

## Configuration ownership

The server resolves each backend's selected worker volume mount and records mounted reads and writes in version 4 storage plans. Workers use the plan directly, even when their static ConfigMap differs. The server does not read worker filesystem paths. Existing Jobs with version 4 plans remain compatible. The version 4 decoder cannot run or clean up Jobs with version 1, 2, or 3 plans. Finish those tasks and clean up their Jobs with a compatible older binary before upgrading.

The broader native storage migration changes the config schema. Replace `MountedRead.WorkerMount` and `MountedWrite.WorkerMount` with `Volume`, and set `SubPath` when one volume has multiple explicit worker mounts. Rebuild Go RPC plugins against the updated config package before exchanging configuration with them. The automatic PVC naming change does not alter the plugin schema or require another rebuild. The schema reserves field 1 and the name `WorkerMount` in `MountedRead` and `MountedWrite`. Other compute backends retain their existing storage clients. No TES schema fields change.

## Output publication

For an output URL that matches a configured `MountedWrite` endpoint, bucket, and prefix, the worker copies each output file to a private temporary file beside its destination. After closing the file, it renames the temporary file over the destination. Concurrent transfers publish complete files instead of copying into the same inode. If the source already is the mounted destination, Funnel skips the copy. It then calls S3 `HeadObject` until the object exists with the same size as the local file. The worker checks zero-byte files too.

Mounted file transfers require Unix. The filesystem must support exclusive temporary-file creation, POSIX file permissions, and atomic replacement through same-directory rename, including across worker Pods. A failed rename fails the transfer. Funnel does not retry through an in-place copy or an API upload. Separate write routes must use nonoverlapping filesystem views. Funnel rejects overlapping roots and overlapping views of the same PVC that it can identify from the storage plan.

After a failed or canceled copy, Funnel attempts to remove its temporary file and reports cleanup errors. A process crash can leave temporary files, and a storage driver can export temporary names to the bucket before Funnel removes them. Account for these files in the deployment's storage cleanup policy. Filesystem replacement does not make the driver's later S3 synchronization atomic. See [S3 Files synchronization](https://docs.aws.amazon.com/AmazonS3/latest/userguide/s3-files-synchronization.html) for that driver's rename and export behavior.

This check proves that S3 reports an object with the expected size. It does not compare file contents, checksums, or object versions. A same-sized object left by an earlier task can satisfy the check. Set `MountedWrite.Timeout` to bound polling. The example's `2700s` timeout is 45 minutes. The default is three hours, and the worker polls every two seconds. An explicit timeout must be positive. Task cancellation also stops the wait.

The GenericS3 credentials must allow `HeadObject` for mounted destinations. They do not need upload permission for an output handled by the mount. An output URL that does not match the configured endpoint, bucket, and prefix follows the regular GenericS3 API upload path, which requires upload permission. Outputs do not use a `MountedRead` setting.

Funnel handles directory outputs as individual files. It records successful files in TES output logs and preserves partial successes when another file fails. A timeout, cancellation, or transfer error prevents successful task completion. Retries retain one successful log entry per file after publication succeeds.
