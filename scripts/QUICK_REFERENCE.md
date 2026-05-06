# Quick Reference: GCP Batch Integration Tests

## One-Liners

```bash
# Standard run
./scripts/validate-gcp-batch-integration.sh

# Dry-run (preview without executing)
./scripts/validate-gcp-batch-integration.sh --dry-run

# Quiet mode (summary only)
./scripts/validate-gcp-batch-integration.sh --quiet

# Custom project
GCP_PROJECT=my-project ./scripts/validate-gcp-batch-integration.sh

# Custom timeout (10 minutes per test)
MAX_WAIT_TIME=600 ./scripts/validate-gcp-batch-integration.sh
```

## File Locations

| File | Purpose |
|------|---------|
| `scripts/validate-gcp-batch-integration.sh` | Main validation script |
| `scripts/README.md` | Full documentation |
| `examples/gcp/*.json` | Test task definitions |
| `gcp-batch-w-db.yaml` | Funnel server config |

## Test Coverage

1. **read-write-gcp.json** - Basic I/O
2. **multiple-executors.json** - Per-executor symlinks
3. **command-construction-test.json** - Shell escaping
4. **multiple-buckets.json** - Cross-bucket operations
5. **multiple-inputs-outputs.json** - Multiple I/O handling

## Expected Runtime

- **Dry-run**: ~2 seconds
- **Full validation**: ~15-20 minutes (5 tests × 3-4 minutes each)

## Environment Variables

```bash
GCP_PROJECT=tes-batch-integration-test    # GCP project ID
GCP_REGION=us-central1                     # GCP region
FUNNEL_CONFIG=gcp-batch-w-db.yaml         # Funnel config file
BUCKET_PRIMARY=tes-batch-integration      # Primary bucket
BUCKET_2=tes-batch-integration-2          # Secondary bucket
BUCKET_3=tes-batch-integration-3          # Tertiary bucket
MAX_WAIT_TIME=300                          # Max wait per job (seconds)
```

## Exit Codes

- `0` = All tests passed
- `1` = One or more tests failed or prerequisites not met

## Common Issues

### "Not authenticated with GCP"
```bash
gcloud auth login
gcloud auth application-default login
```

### "Bucket not found"
Buckets are auto-created by the script. If you get this error, check permissions:
```bash
gcloud projects get-iam-policy tes-batch-integration-test
```

### "Job timed out"
Increase timeout:
```bash
MAX_WAIT_TIME=600 ./scripts/validate-gcp-batch-integration.sh
```

### "Funnel binary not found"
Build Funnel:
```bash
go build -o ./funnel .
```

## Manual Cleanup

If needed, clean up all test outputs:
```bash
gsutil -m rm gs://tes-batch-integration/output/**
gsutil -m rm gs://tes-batch-integration-3/output/**
```

## GitHub Actions Example

```yaml
- name: Run GCP Batch Integration Tests
  run: ./scripts/validate-gcp-batch-integration.sh
  env:
    GCP_PROJECT: ${{ secrets.GCP_PROJECT }}
```

## What Gets Validated

For each test:
1. ✅ Job submission succeeds
2. ✅ GCP Batch job reaches SUCCEEDED state
3. ✅ Output files exist in GCS
4. ✅ Output content matches expected transformations
5. ✅ No errors in job logs

## Support

See full docs: `scripts/README.md`
