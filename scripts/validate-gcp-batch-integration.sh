#!/usr/bin/env bash

#######################################
# GCP Batch Integration Test Validation Script
#
# Tests the GCP Batch path mapping implementation with comprehensive validation:
# - Cleans up old test outputs before/after runs
# - Validates job success on GCP Batch
# - Verifies exact output content matches expected transformations
# - Checks logs for errors
#
# Usage:
#   ./scripts/validate-gcp-batch-integration.sh [--dry-run] [--quiet]
#
# Options:
#   --dry-run    Show what would be done without executing
#   --quiet      Minimal output, show only pass/fail summary
#
# Environment Variables:
#   GCP_PROJECT     GCP project ID (default: tes-batch-integration-test)
#   GCP_REGION      GCP region (default: us-central1)
#   FUNNEL_CONFIG   Path to Funnel config (default: gcp-batch-w-db.yaml)
#   BUCKET_PRIMARY  Primary test bucket (default: tes-batch-integration)
#   BUCKET_2        Secondary bucket (default: tes-batch-integration-2)
#   BUCKET_3        Tertiary bucket (default: tes-batch-integration-3)
#   MAX_WAIT_TIME   Max seconds to wait for jobs (default: 300)
#######################################

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration with defaults
GCP_PROJECT="${GCP_PROJECT:-tes-batch-integration-test}"
GCP_REGION="${GCP_REGION:-us-central1}"
FUNNEL_CONFIG="${FUNNEL_CONFIG:-gcp-batch-w-db.yaml}"
BUCKET_PRIMARY="${BUCKET_PRIMARY:-tes-batch-integration}"
BUCKET_2="${BUCKET_2:-tes-batch-integration-2}"
BUCKET_3="${BUCKET_3:-tes-batch-integration-3}"
MAX_WAIT_TIME="${MAX_WAIT_TIME:-300}"

# Script state
DRY_RUN=false
QUIET=false
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEST_DIR="${PROJECT_ROOT}/examples/gcp"
FUNNEL_BIN="${PROJECT_ROOT}/funnel"

# Test results tracking
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0
declare -a FAILED_TESTS
declare -a SUBMITTED_JOB_IDS
declare -a SUBMITTED_JOB_NAMES

#######################################
# Logging functions
#######################################
log_info() {
    if [[ "$QUIET" == "false" ]]; then
        echo -e "${BLUE}[INFO]${NC} $*"
    fi
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $*"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $*" >&2
}

log_warn() {
    if [[ "$QUIET" == "false" ]]; then
        echo -e "${YELLOW}[WARN]${NC} $*"
    fi
}

log_dry_run() {
    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${YELLOW}[DRY-RUN]${NC} $*"
    fi
}

#######################################
# Execute command (respects dry-run mode)
#######################################
execute() {
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "$*"
        return 0
    else
        "$@"
    fi
}

#######################################
# Check prerequisites
#######################################
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    local all_good=true
    
    # Check required tools
    for tool in gcloud gsutil; do
        if ! command -v "$tool" &> /dev/null; then
            log_error "Required tool not found: $tool"
            all_good=false
        else
            log_info "✓ Found $tool"
        fi
    done
    
    # Check Funnel binary
    if [[ ! -f "$FUNNEL_BIN" ]]; then
        log_error "Funnel binary not found at: $FUNNEL_BIN"
        log_info "Run: go build -o ./funnel ."
        all_good=false
    else
        log_info "✓ Found funnel binary"
    fi
    
    # Check Funnel config
    if [[ ! -f "${PROJECT_ROOT}/${FUNNEL_CONFIG}" ]]; then
        log_error "Funnel config not found: ${FUNNEL_CONFIG}"
        all_good=false
    else
        log_info "✓ Found funnel config"
    fi
    
    # Check test files
    local test_files=("read-write-gcp.json" "multiple-executors.json" "command-construction-test.json" "multiple-buckets.json" "multiple-inputs-outputs.json")
    for test_file in "${test_files[@]}"; do
        if [[ ! -f "${TEST_DIR}/${test_file}" ]]; then
            log_error "Test file not found: ${TEST_DIR}/${test_file}"
            all_good=false
        fi
    done
    if [[ "${#test_files[@]}" -gt 0 ]]; then
        log_info "✓ Found ${#test_files[@]} test files"
    fi
    
    # Check GCP authentication
    if [[ "$DRY_RUN" == "false" ]]; then
        if ! gcloud auth list --filter=status:ACTIVE --format="value(account)" &> /dev/null; then
            log_error "Not authenticated with GCP. Run: gcloud auth login"
            all_good=false
        else
            local active_account=$(gcloud auth list --filter=status:ACTIVE --format="value(account)" 2>/dev/null | head -1)
            log_info "✓ Authenticated as: $active_account"
        fi
        
        # Verify project access
        if ! gcloud projects describe "$GCP_PROJECT" &> /dev/null; then
            log_error "Cannot access GCP project: $GCP_PROJECT"
            all_good=false
        else
            log_info "✓ Access to project: $GCP_PROJECT"
        fi
    fi
    
    if [[ "$all_good" == "false" ]]; then
        log_error "Prerequisites check failed"
        exit 1
    fi
    
    log_success "All prerequisites satisfied"
}

#######################################
# Setup test environment
#######################################
setup_test_environment() {
    log_info "Setting up test environment..."
    
    # Ensure buckets exist
    for bucket in "$BUCKET_PRIMARY" "$BUCKET_2" "$BUCKET_3"; do
        if execute gsutil ls -b "gs://${bucket}/" &> /dev/null; then
            log_info "✓ Bucket exists: $bucket"
        else
            log_info "Creating bucket: $bucket"
            execute gsutil mb -p "$GCP_PROJECT" -l "$GCP_REGION" "gs://${bucket}/"
        fi
    done
    
    # Create and verify input files
    log_info "Setting up test input files..."
    
    # Primary bucket: sample.txt
    local sample_content="this is a sample"
    if [[ "$DRY_RUN" == "false" ]]; then
        echo "$sample_content" | execute gsutil cp - "gs://${BUCKET_PRIMARY}/input-data/sample.txt"
        local actual_content=$(gsutil cat "gs://${BUCKET_PRIMARY}/input-data/sample.txt" 2>/dev/null || echo "")
        if [[ "$actual_content" == "$sample_content" ]]; then
            log_info "✓ Verified: sample.txt"
        else
            log_error "Content mismatch for sample.txt"
            return 1
        fi
    else
        log_dry_run "Would create: gs://${BUCKET_PRIMARY}/input-data/sample.txt"
    fi
    
    # Secondary bucket: reference.txt
    local reference_content="This is reference data from bucket 2"
    if [[ "$DRY_RUN" == "false" ]]; then
        echo "$reference_content" | execute gsutil cp - "gs://${BUCKET_2}/data/reference.txt"
        local actual_content=$(gsutil cat "gs://${BUCKET_2}/data/reference.txt" 2>/dev/null || echo "")
        if [[ "$actual_content" == "$reference_content" ]]; then
            log_info "✓ Verified: reference.txt"
        else
            log_error "Content mismatch for reference.txt"
            return 1
        fi
    else
        log_dry_run "Would create: gs://${BUCKET_2}/data/reference.txt"
    fi
    
    # Multiple input files for test 5
    for i in 1 2 3; do
        local file_content="content of file $i"
        if [[ "$DRY_RUN" == "false" ]]; then
            echo "$file_content" | execute gsutil cp - "gs://${BUCKET_PRIMARY}/input-data/file${i}.txt"
            local actual_content=$(gsutil cat "gs://${BUCKET_PRIMARY}/input-data/file${i}.txt" 2>/dev/null || echo "")
            if [[ "$actual_content" == "$file_content" ]]; then
                log_info "✓ Verified: file${i}.txt"
            else
                log_error "Content mismatch for file${i}.txt"
                return 1
            fi
        else
            log_dry_run "Would create: gs://${BUCKET_PRIMARY}/input-data/file${i}.txt"
        fi
    done
    
    log_success "Test environment ready"
}

#######################################
# Cleanup test outputs
#######################################
cleanup_outputs() {
    log_info "Cleaning up previous test outputs..."
    
    local output_paths=(
        "gs://${BUCKET_PRIMARY}/output/result.txt"
        "gs://${BUCKET_PRIMARY}/output/executor1-result.txt"
        "gs://${BUCKET_PRIMARY}/output/executor2-result.txt"
        "gs://${BUCKET_PRIMARY}/output/hello-output.txt"
        "gs://${BUCKET_PRIMARY}/output/processed-file1.txt"
        "gs://${BUCKET_PRIMARY}/output/processed-file2.txt"
        "gs://${BUCKET_PRIMARY}/output/combined.txt"
        "gs://${BUCKET_3}/output/result.txt"
    )
    
    for path in "${output_paths[@]}"; do
        if [[ "$DRY_RUN" == "false" ]]; then
            if gsutil ls "$path" &> /dev/null; then
                execute gsutil rm "$path"
                log_info "✓ Removed: $path"
            fi
        else
            log_dry_run "Would remove: $path"
        fi
    done
    
    log_info "Cleanup complete"
}

#######################################
# Wait for multiple jobs to complete (parallel)
#######################################
wait_for_jobs() {
    local timeout="$MAX_WAIT_TIME"
    local elapsed=0
    local poll_interval=10
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Would wait for ${#SUBMITTED_JOB_IDS[@]} jobs"
        return 0
    fi
    
    local total_jobs=${#SUBMITTED_JOB_IDS[@]}
    
    # Track job completion status (indexed arrays for bash 3.x compatibility)
    declare -a job_completed_flags
    for ((i=0; i<total_jobs; i++)); do
        job_completed_flags[$i]=0  # 0=pending, 1=succeeded, 2=failed
    done
    
    log_info "Waiting for $total_jobs jobs to complete (max ${timeout}s)..."
    
    while [[ $elapsed -lt $timeout ]]; do
        local all_done=true
        local completed_count=0
        local failed_count=0
        
        for i in "${!SUBMITTED_JOB_IDS[@]}"; do
            local job_id="${SUBMITTED_JOB_IDS[$i]}"
            local job_name="${SUBMITTED_JOB_NAMES[$i]}"
            local status_flag="${job_completed_flags[$i]}"
            
            # Already processed
            if [[ "$status_flag" -ne 0 ]]; then
                ((completed_count++))
                if [[ "$status_flag" -eq 2 ]]; then
                    ((failed_count++))
                fi
                continue
            fi
            
            all_done=false
            
            local state=$(gcloud batch jobs describe "$job_id" \
                --project="$GCP_PROJECT" \
                --location="$GCP_REGION" \
                --format="value(status.state)" 2>/dev/null || echo "UNKNOWN")
            
            case "$state" in
                SUCCEEDED)
                    job_completed_flags[$i]=1
                    ((completed_count++))
                    log_success "✓ $job_name completed ($job_id)"
                    ;;
                FAILED|DELETION_IN_PROGRESS)
                    job_completed_flags[$i]=2
                    ((completed_count++))
                    ((failed_count++))
                    log_error "✗ $job_name failed ($job_id)"
                    ;;
                QUEUED|SCHEDULED|RUNNING)
                    # Still running
                    ;;
                *)
                    if [[ "$QUIET" == "false" ]]; then
                        log_warn "Unknown state for $job_name: $state"
                    fi
                    ;;
            esac
        done
        
        if [[ "$all_done" == "true" ]]; then
            echo ""
            log_info "All jobs completed: $((completed_count - failed_count)) succeeded, $failed_count failed"
            if [[ $failed_count -gt 0 ]]; then
                return 1
            fi
            return 0
        fi
        
        if [[ "$QUIET" == "false" ]]; then
            echo -ne "\r  Progress: $completed_count/$total_jobs completed (${elapsed}s/${timeout}s)..."
        fi
        
        sleep $poll_interval
        elapsed=$((elapsed + poll_interval))
    done
    
    echo ""
    log_error "Jobs timed out after ${timeout}s"
    return 1
}

#######################################
# Submit a test job (for parallel execution)
#######################################
submit_test() {
    local test_file="$1"
    local test_name="$2"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Would submit: $test_file"
        echo "dry-run-job-id"
        return 0
    fi
    
    # Submit task (send logs to stderr to keep stdout clean for job ID)
    echo "[INFO] Submitting: $test_name ($test_file)" >&2
    local task_id
    task_id=$("$FUNNEL_BIN" task create "${TEST_DIR}/${test_file}" 2>&1)
    
    if [[ -z "$task_id" ]] || [[ "$task_id" =~ "Error:" ]] || [[ "$task_id" =~ "connection refused" ]]; then
        echo "[FAIL] Failed to submit: $test_name - $task_id" >&2
        echo ""
        return 1
    fi
    
    echo "[INFO] ✓ Submitted: $test_name → $task_id" >&2
    echo "$task_id"
    return 0
}

#######################################
# Validate a completed test
#######################################
validate_test() {
    local job_id="$1"
    local test_name="$2"
    local validate_func="$3"
    
    TESTS_RUN=$((TESTS_RUN + 1))
    
    log_info ""
    log_info "=========================================="
    log_info "Validating Test $TESTS_RUN: $test_name"
    log_info "=========================================="
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Would validate with: $validate_func"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    fi
    
    if [[ -z "$job_id" ]]; then
        log_error "No job ID for $test_name (submission failed)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        FAILED_TESTS+=("$test_name: submission failed")
        return 1
    fi
    
    # Validate outputs
    log_info "Validating outputs for job: $job_id"
    if $validate_func "$job_id"; then
        log_success "Test passed: $test_name"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        log_error "Test failed: $test_name (validation failed)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        FAILED_TESTS+=("$test_name: output validation failed")
        return 1
    fi
}

#######################################
# Validation functions for each test
#######################################

validate_read_write() {
    local job_id="$1"
    local expected="THIS IS A SAMPLE"
    local output_path="gs://${BUCKET_PRIMARY}/output/result.txt"
    
    # Check file exists
    if ! gsutil ls "$output_path" &> /dev/null; then
        log_error "Output file not found: $output_path"
        return 1
    fi
    
    # Validate content
    local actual=$(gsutil cat "$output_path" 2>/dev/null | tr -d '\n')
    if [[ "$actual" == "$expected" ]]; then
        log_info "✓ Output content matches expected: $expected"
        return 0
    else
        log_error "Output mismatch. Expected: '$expected', Got: '$actual'"
        return 1
    fi
}

validate_multiple_executors() {
    local job_id="$1"
    
    # Executor 1: uppercase output
    local output1="gs://${BUCKET_PRIMARY}/output/executor1-result.txt"
    local expected1="THIS IS A SAMPLE"
    
    if ! gsutil ls "$output1" &> /dev/null; then
        log_error "Executor 1 output not found: $output1"
        return 1
    fi
    
    local actual1=$(gsutil cat "$output1" 2>/dev/null | tr -d '\n')
    if [[ "$actual1" != "$expected1" ]]; then
        log_error "Executor 1 output mismatch. Expected: '$expected1', Got: '$actual1'"
        return 1
    fi
    log_info "✓ Executor 1 output correct"
    
    # Executor 2: word count output
    local output2="gs://${BUCKET_PRIMARY}/output/executor2-result.txt"
    
    if ! gsutil ls "$output2" &> /dev/null; then
        log_error "Executor 2 output not found: $output2"
        return 1
    fi
    
    local actual2=$(gsutil cat "$output2" 2>/dev/null)
    # Should contain "4" (word count of "this is a sample")
    if [[ "$actual2" =~ 4 ]]; then
        log_info "✓ Executor 2 output correct (word count: 4)"
        return 0
    else
        log_error "Executor 2 output unexpected: $actual2"
        return 1
    fi
}

validate_command_construction() {
    local job_id="$1"
    local expected="Hello, World!"
    local output_path="gs://${BUCKET_PRIMARY}/output/hello-output.txt"
    
    if ! gsutil ls "$output_path" &> /dev/null; then
        log_error "Output file not found: $output_path"
        return 1
    fi
    
    local actual=$(gsutil cat "$output_path" 2>/dev/null | tr -d '\n')
    if [[ "$actual" == "$expected" ]]; then
        log_info "✓ Command with quotes executed correctly: $expected"
        return 0
    else
        log_error "Output mismatch. Expected: '$expected', Got: '$actual'"
        return 1
    fi
}

validate_multiple_buckets() {
    local job_id="$1"
    local output_path="gs://${BUCKET_3}/output/result.txt"
    
    if ! gsutil ls "$output_path" &> /dev/null; then
        log_error "Output file not found: $output_path"
        return 1
    fi
    
    local actual=$(gsutil cat "$output_path" 2>/dev/null)
    
    # Should contain content from both bucket 1 and bucket 2
    if [[ "$actual" =~ "this is a sample" ]] && [[ "$actual" =~ "reference data from bucket 2" ]]; then
        log_info "✓ Combined content from 2 buckets written to 3rd bucket"
        return 0
    else
        log_error "Output doesn't contain expected content from both buckets"
        log_error "Actual content: $actual"
        return 1
    fi
}

validate_multiple_inputs_outputs() {
    local job_id="$1"
    
    # Output 1: uppercase
    local output1="gs://${BUCKET_PRIMARY}/output/processed-file1.txt"
    local expected1="CONTENT OF FILE 1"
    
    if ! gsutil ls "$output1" &> /dev/null; then
        log_error "Output 1 not found: $output1"
        return 1
    fi
    
    local actual1=$(gsutil cat "$output1" 2>/dev/null | tr -d '\n')
    if [[ "$actual1" != "$expected1" ]]; then
        log_error "Output 1 mismatch. Expected: '$expected1', Got: '$actual1'"
        return 1
    fi
    log_info "✓ Output 1 correct (uppercase)"
    
    # Output 2: reversed
    local output2="gs://${BUCKET_PRIMARY}/output/processed-file2.txt"
    local expected2="2 elif fo tnetnoc"
    
    if ! gsutil ls "$output2" &> /dev/null; then
        log_error "Output 2 not found: $output2"
        return 1
    fi
    
    local actual2=$(gsutil cat "$output2" 2>/dev/null | tr -d '\n')
    if [[ "$actual2" != "$expected2" ]]; then
        log_error "Output 2 mismatch. Expected: '$expected2', Got: '$actual2'"
        return 1
    fi
    log_info "✓ Output 2 correct (reversed)"
    
    # Output 3: combined
    local output3="gs://${BUCKET_PRIMARY}/output/combined.txt"
    
    if ! gsutil ls "$output3" &> /dev/null; then
        log_error "Output 3 not found: $output3"
        return 1
    fi
    
    local actual3=$(gsutil cat "$output3" 2>/dev/null)
    if [[ "$actual3" =~ "content of file 1" ]] && \
       [[ "$actual3" =~ "content of file 2" ]] && \
       [[ "$actual3" =~ "content of file 3" ]]; then
        log_info "✓ Output 3 correct (combined)"
        return 0
    else
        log_error "Output 3 doesn't contain all expected content"
        return 1
    fi
}

#######################################
# Run all tests (in parallel)
#######################################
run_all_tests() {
    log_info "Starting integration tests (parallel execution)..."
    echo ""
    
    # Define test configurations
    declare -a test_files=("read-write-gcp.json" "multiple-executors.json" "command-construction-test.json" "multiple-buckets.json" "multiple-inputs-outputs.json")
    declare -a test_names=("Read-Write-GCP" "Multiple Executors" "Command Construction" "Multiple Buckets" "Multiple Inputs/Outputs")
    declare -a validate_funcs=(validate_read_write validate_multiple_executors validate_command_construction validate_multiple_buckets validate_multiple_inputs_outputs)
    
    # Submit all jobs in parallel
    log_info "=========================================="
    log_info "Phase 1: Submitting all tests"
    log_info "=========================================="
    
    for i in "${!test_files[@]}"; do
        local job_id=$(submit_test "${test_files[$i]}" "${test_names[$i]}")
        SUBMITTED_JOB_IDS+=("$job_id")
        SUBMITTED_JOB_NAMES+=("${test_names[$i]}")
    done
    
    echo ""
    log_info "Submitted ${#SUBMITTED_JOB_IDS[@]} jobs"
    
    # Wait for all jobs to complete
    log_info ""
    log_info "=========================================="
    log_info "Phase 2: Waiting for completion"
    log_info "=========================================="
    
    if ! wait_for_jobs; then
        log_warn "Some jobs failed during execution"
    fi
    
    # Validate all outputs
    log_info ""
    log_info "=========================================="
    log_info "Phase 3: Validating outputs"
    log_info "=========================================="
    
    for i in "${!test_files[@]}"; do
        validate_test "${SUBMITTED_JOB_IDS[$i]}" "${test_names[$i]}" "${validate_funcs[$i]}"
    done
}

#######################################
# Print summary
#######################################
print_summary() {
    echo ""
    echo "=========================================="
    echo "TEST SUMMARY"
    echo "=========================================="
    echo "Total tests: $TESTS_RUN"
    echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
    
    if [[ $TESTS_FAILED -gt 0 ]]; then
        echo -e "${RED}Failed: $TESTS_FAILED${NC}"
        echo ""
        echo "Failed tests:"
        for test in "${FAILED_TESTS[@]}"; do
            echo -e "  ${RED}✗${NC} $test"
        done
    fi
    
    echo "=========================================="
    
    if [[ $TESTS_FAILED -eq 0 ]]; then
        log_success "All tests passed! 🎉"
        return 0
    else
        log_error "Some tests failed"
        return 1
    fi
}

#######################################
# Main execution
#######################################
main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --dry-run)
                DRY_RUN=true
                shift
                ;;
            --quiet)
                QUIET=true
                shift
                ;;
            -h|--help)
                grep "^#" "$0" | grep -v "#!/usr/bin/env" | sed 's/^# \?//'
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                echo "Use --help for usage information"
                exit 1
                ;;
        esac
    done
    
    echo "=========================================="
    echo "GCP Batch Integration Test Validation"
    echo "=========================================="
    echo "Project: $GCP_PROJECT"
    echo "Region: $GCP_REGION"
    echo "Primary Bucket: $BUCKET_PRIMARY"
    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${YELLOW}Mode: DRY RUN${NC}"
    fi
    echo "=========================================="
    echo ""
    
    # Run validation steps
    check_prerequisites
    setup_test_environment
    cleanup_outputs
    run_all_tests
    cleanup_outputs
    
    # Print summary and exit with appropriate code
    if print_summary; then
        exit 0
    else
        exit 1
    fi
}

# Run main function
main "$@"
