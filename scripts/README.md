# GCP Batch Integration Test Validation

Automated validation script for GCP Batch path mapping implementation.

## Quick Start

```bash
# Make script executable
chmod +x scripts/validate-gcp-batch-integration.sh

# Run validation
./scripts/validate-gcp-batch-integration.sh
```

## Prerequisites

### Required Tools
- `gcloud` CLI (authenticated)
- `gsutil` CLI
- Funnel binary built (`./funnel`)
- Funnel config file (`gcp-batch-w-db.yaml`)

### GCP Setup
1. **Authenticate with GCP:**
   ```bash
   gcloud auth login
   gcloud auth application-default login
   ```

2. **Set active project:**
   ```bash
   gcloud config set project tes-batch-integration-test
   ```

3. **Enable required APIs:**
   ```bash
   gcloud services enable batch.googleapis.com
   gcloud services enable compute.googleapis.com
   gcloud services enable storage.googleapis.com
   ```

4. **Buckets** (auto-created by script):
   - `tes-batch-integration` (primary)
   - `tes-batch-integration-2` (secondary)
   - `tes-batch-integration-3` (tertiary)

### Build Funnel
```bash
go build -o ./funnel .
```

## Usage

### Basic Usage
```bash
./scripts/validate-gcp-batch-integration.sh
```

### Dry Run Mode
See what the script would do without executing:
```bash
./scripts/validate-gcp-batch-integration.sh --dry-run
```

### Quiet Mode
Minimal output, show only pass/fail summary:
```bash
./scripts/validate-gcp-batch-integration.sh --quiet
```

### Help
```bash
./scripts/validate-gcp-batch-integration.sh --help
```

## Configuration

Override defaults using environment variables:

```bash
# Custom project and region
GCP_PROJECT=my-project-id \
GCP_REGION=us-west1 \
./scripts/validate-gcp-batch-integration.sh

# Custom bucket names
BUCKET_PRIMARY=my-test-bucket \
BUCKET_2=my-test-bucket-2 \
BUCKET_3=my-test-bucket-3 \
./scripts/validate-gcp-batch-integration.sh

# Custom wait timeout (seconds)
MAX_WAIT_TIME=600 ./scripts/validate-gcp-batch-integration.sh

# Custom Funnel config
FUNNEL_CONFIG=my-config.yaml ./scripts/validate-gcp-batch-integration.sh
```

## What It Tests

The script runs 5 comprehensive integration tests:

| Test | Description | Validates |
|------|-------------|-----------|
| **Read-Write-GCP** | Basic I/O with single input/output | File read, uppercase transform, GCS write |
| **Multiple Executors** | Two independent executors | Per-executor symlink generation |
| **Command Construction** | Commands with quoted strings | Shell escaping with `shellquote.Join()` |
| **Multiple Buckets** | Cross-bucket operations | Reading from 2 buckets, writing to 3rd |
| **Multiple Inputs/Outputs** | 3 inputs → 3 outputs | Uppercase, reverse, combine operations |

## How It Works

1. **Prerequisites Check**
   - Verifies required tools installed
   - Checks GCP authentication
   - Validates project access
   - Confirms test files exist

2. **Environment Setup**
   - Creates GCS buckets if needed
   - Uploads test input files
   - Verifies input file contents

3. **Pre-Test Cleanup**
   - Removes old output files
   - Ensures clean test slate

4. **Test Execution**
   - Submits each test task via Funnel
   - Waits for GCP Batch job completion (max 5 minutes per job)
   - Validates job succeeded
   - Validates output content matches expected transformations

5. **Post-Test Cleanup**
   - Removes test output files
   - Leaves input files and buckets intact

6. **Results Summary**
   - Reports pass/fail for each test
   - Exits with code 0 if all pass, 1 if any fail

## Output Validation

The script validates exact output content for each test:

- **Test 1**: `"THIS IS A SAMPLE"` (uppercase)
- **Test 2**: Executor 1 uppercase + Executor 2 word count (4)
- **Test 3**: `"Hello, World!"` (quoted string preserved)
- **Test 4**: Combined content from 2 source buckets
- **Test 5**: 3 transformed outputs (uppercase, reversed, combined)

## Troubleshooting

### Authentication Issues
```bash
# Re-authenticate
gcloud auth login
gcloud auth application-default login

# Verify active account
gcloud auth list
```

### Bucket Access Issues
```bash
# Check bucket exists
gsutil ls gs://tes-batch-integration/

# Check permissions
gsutil iam get gs://tes-batch-integration/
```

### Job Timeout
Increase wait time:
```bash
MAX_WAIT_TIME=600 ./scripts/validate-gcp-batch-integration.sh
```

### Failed Test Debugging
Check GCP Batch logs:
```bash
# Get job ID from script output
JOB_ID="your-job-id"

# View job details
gcloud batch jobs describe $JOB_ID \
  --project=tes-batch-integration-test \
  --location=us-central1

# View logs
gcloud logging read "resource.type=batch.googleapis.com/Job AND resource.labels.job_uid=$JOB_ID" \
  --project=tes-batch-integration-test \
  --limit=50
```

### Clean Slate
Manually remove all test outputs:
```bash
gsutil rm gs://tes-batch-integration/output/**
gsutil rm gs://tes-batch-integration-3/output/**
```

## CI/CD Integration

### GitHub Actions Example
```yaml
name: GCP Batch Integration Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - uses: google-github-actions/auth@v1
        with:
          credentials_json: ${{ secrets.GCP_SA_KEY }}
      
      - uses: google-github-actions/setup-gcloud@v1
      
      - name: Build Funnel
        run: go build -o ./funnel .
      
      - name: Run Integration Tests
        run: ./scripts/validate-gcp-batch-integration.sh
        env:
          GCP_PROJECT: ${{ secrets.GCP_PROJECT }}
```

## Exit Codes

- `0`: All tests passed
- `1`: One or more tests failed or prerequisites not met

## Support

For issues with the validation script, check:
1. GCP authentication status
2. Project permissions (Batch Admin, Storage Admin)
3. API enablement (Batch, Compute, Storage)
4. Funnel server configuration
5. Test file integrity in `examples/gcp/`

## Development

To add new tests:
1. Create test JSON file in `examples/gcp/`
2. Add validation function (e.g., `validate_my_test()`)
3. Add test case to `run_all_tests()` function
4. Update this README with test description
