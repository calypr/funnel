#!/usr/bin/env bash
set -u
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

usage() {
  printf 'usage: %s snapshot <namespace> | watch <namespace> <interval-seconds> <max-seconds>\n' "$0" >&2
  exit 2
}

capture() {
  local label=$1
  shift
  printf '\n--- %s (%s) ---\n' "$label" "$(date -u +%FT%TZ)"
  "$@" 2>&1 | python3 -u "$script_dir/redact_logs.py" || true
}

snapshot() {
  local service_namespace=$1
  local jobs_namespace=$2
  local include_logs=$3
  local include_csi=$4
  local pod_columns='NAME:.metadata.name,PHASE:.status.phase,REASON:.status.reason,NODE:.spec.nodeName,CONTAINERS:.status.containerStatuses[*].name,READY:.status.containerStatuses[*].ready,RESTARTS:.status.containerStatuses[*].restartCount,WAITING:.status.containerStatuses[*].state.waiting.reason,TERMINATED:.status.containerStatuses[*].state.terminated.reason,EXIT_CODE:.status.containerStatuses[*].state.terminated.exitCode,LAST_TERMINATED:.status.containerStatuses[*].lastState.terminated.reason,LAST_EXIT_CODE:.status.containerStatuses[*].lastState.terminated.exitCode,INIT_WAITING:.status.initContainerStatuses[*].state.waiting.reason,INIT_TERMINATED:.status.initContainerStatuses[*].state.terminated.reason,INIT_EXIT_CODE:.status.initContainerStatuses[*].state.terminated.exitCode,INIT_LAST_TERMINATED:.status.initContainerStatuses[*].lastState.terminated.reason,INIT_LAST_EXIT_CODE:.status.initContainerStatuses[*].lastState.terminated.exitCode'

  printf '\nGen3 task diagnostics %s\n' "$(date -u +%FT%TZ)"
  capture "task pod states in $jobs_namespace" kubectl --request-timeout=3s -n "$jobs_namespace" \
    get pods -o "custom-columns=$pod_columns"
  capture "task warning events in $jobs_namespace" kubectl --request-timeout=3s -n "$jobs_namespace" \
    get events --field-selector type=Warning --sort-by=.lastTimestamp
  capture "service pod states in $service_namespace" kubectl --request-timeout=3s -n "$service_namespace" \
    get pods -o "custom-columns=$pod_columns"
  capture "service warning events in $service_namespace" kubectl --request-timeout=3s -n "$service_namespace" \
    get events --field-selector type=Warning --sort-by=.lastTimestamp

  if [[ "$include_logs" == true ]]; then
    capture "Funnel server logs in $service_namespace" kubectl --request-timeout=3s -n "$service_namespace" logs \
      -l app=funnel --all-containers --prefix --tail=80 --max-log-requests=8
    capture "Gen3 gateway logs in $service_namespace" kubectl --request-timeout=3s -n "$service_namespace" logs \
      -l 'app in (gen3-workflow,gen3-workflow-dpop)' --all-containers --prefix --tail=40 --max-log-requests=8
    capture "Funnel worker logs in $jobs_namespace" kubectl --request-timeout=3s -n "$jobs_namespace" logs \
      -l app=funnel-worker --all-containers --prefix --tail=80 --max-log-requests=8
    capture "Funnel executor logs in $jobs_namespace" kubectl --request-timeout=3s -n "$jobs_namespace" logs \
      -l app=funnel-executor --all-containers --prefix --tail=80 --max-log-requests=8
  fi

  if [[ "$include_csi" == true ]]; then
    capture 'CSI warning events in kube-system' kubectl --request-timeout=3s -n kube-system get events \
      --field-selector type=Warning --sort-by=.lastTimestamp
    capture 'Mountpoint CSI controller logs in kube-system' kubectl --request-timeout=3s -n kube-system logs \
      -l app=s3-csi-controller --all-containers --prefix --tail=80 --max-log-requests=8
    capture 'Mountpoint CSI node logs in kube-system' kubectl --request-timeout=3s -n kube-system logs \
      -l app=s3-csi-node --all-containers --prefix --tail=80 --max-log-requests=8
  fi
}

validate_seconds() {
  local name=$1
  local value=$2
  local minimum=$3
  local maximum=$4
  [[ "$value" =~ ^[0-9]+$ ]] || { printf '%s must be an integer in [%s, %s]\n' "$name" "$minimum" "$maximum" >&2; exit 2; }
  (( value >= minimum && value <= maximum )) || { printf '%s must be an integer in [%s, %s]\n' "$name" "$minimum" "$maximum" >&2; exit 2; }
}

[[ $# -ge 2 ]] || usage
mode=$1
base_namespace=$2
[[ "$base_namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || { printf 'invalid Kubernetes namespace: %s\n' "$base_namespace" >&2; exit 2; }
if [[ "$base_namespace" == workflow-pods-* ]]; then
  jobs_namespace=$base_namespace
  service_namespace=${base_namespace#workflow-pods-}
else
  service_namespace=$base_namespace
  jobs_namespace="workflow-pods-$base_namespace"
fi

case "$mode" in
  snapshot)
    [[ $# == 2 || ( $# == 3 && "$3" == failure ) ]] || usage
    include_csi=false
    [[ $# == 3 ]] && include_csi=true
    snapshot "$service_namespace" "$jobs_namespace" true "$include_csi"
    ;;
  watch)
    [[ $# == 4 ]] || usage
    interval=$3
    maximum=$4
    validate_seconds interval "$interval" 1 120
    [[ "$maximum" =~ ^[0-9]+$ ]] || { printf 'max-seconds must be nonnegative\n' >&2; exit 2; }

    snapshot "$service_namespace" "$jobs_namespace" true false
    start=$SECONDS
    deadline=$((start + maximum))
    sample=0
    sleep_pid=
    stop() {
      if [[ -n "$sleep_pid" ]]; then
        kill "$sleep_pid" 2>/dev/null || true
      fi
      exit 0
    }
    trap stop INT TERM

    while (( maximum == 0 || SECONDS < deadline )); do
      sleep "$interval" &
      sleep_pid=$!
      wait "$sleep_pid" || true
      sleep_pid=
      (( maximum == 0 || SECONDS < deadline )) || break
      sample=$((sample + 1))
      if (( sample % 4 == 0 )); then
        snapshot "$service_namespace" "$jobs_namespace" true false
      else
        snapshot "$service_namespace" "$jobs_namespace" false false
      fi
    done
    ;;
  *)
    usage
    ;;
esac
