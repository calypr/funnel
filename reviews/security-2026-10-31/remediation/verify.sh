#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."

test_go="${FUNNEL_TEST_GO:-go}"
export GOCACHE="${GOCACHE:-/tmp/funnel-security-gocache}"
export GOPROXY=off

"$test_go" version
python3 reviews/security-2026-10-31/remediation/check_inventory.py
"$test_go" test -count=1 -timeout=3m ./config ./util/fsutil ./database/badger ./database/boltdb ./compute/local ./compute/aws_batch ./compute/gcp_batch ./compute/kubernetes/resources
"$test_go" test -count=1 ./database/postgres -run '^TestRequestContextRetainsUser$'
"$test_go" test -count=1 ./events -run '^Test(LoggerCensorsSensitiveEventPayloads|ErrLoggerCensorsPayloadAndPreservesWriteError|TaskBuilderConcurrentEvents|LogTailWriter.*|StreamLogTailWritersFinishAfterCancellation)$'
"$test_go" test -count=1 ./server -run '^Test(AuthenticationPoliciesAreIndependent|SubmissionTimeoutRecordsTaskFailure|ForbiddenPath.*|Normalize.*|CustomMarshal.*|DetectView.*|MarshalList.*|InitOIDCConfig|ParseJwtSubjectValidatesOIDCTokens|OIDC.*|GatewayRequestBodyLimit|DebugInterceptorLogsOnlyMethodAndStatus|DebugInterceptorPanicLogsTypeWithoutPayload|HandleErrorLogsMetadataAndPreservesResponse|CreateTaskPluginLogOmitsPayloadAndPreservesTask|HandleDoPluginActionErrorOmitsTaskPayload|EventWritesRespectTaskAccess|SchedulerMutationsRequireAdministrator|RPCPanicReturnsInternal|GRPCServerSurvivesHandlerPanic)$'
"$test_go" test -count=1 ./tes ./util/rpc ./cmd/termdash -run '^Test(Credential.*|Dial.*|LoopbackCredentialAddresses|StandaloneCredentialsRequireSecurity|NodesRejectsRemotePlaintextCredentials)$'
"$test_go" test -count=1 ./worker -run '^Test(MapTask.*|IsSubpathUsesPathComponents|ExecutorImagePullPolicy|ExecutorServiceAccountSelection|BundledExecutorTemplatePreservesScalars)$'
"$test_go" test -count=1 ./storage -run '^Test(Local.*|ProcessFile|SameFile|CopyFile.*|FTP.*|HTTPPutRequiresRegularSource|HTTPPutReplayRequiresRegularSource|GoogleCloudPutPropagatesCancellation|OpenRegularFile.*|OpenRootRegularFile.*)$'
"$test_go" test -race -count=1 ./util/fsutil ./compute/local ./database/badger ./database/boltdb
"$test_go" build ./...
printf '%s\n' 'Security remediation checks passed.'
