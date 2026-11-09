#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# < 2 || $# > 3 )); then
  printf 'usage: %s <namespace> <deployment> [timeout-seconds]\n' "$0" >&2
  exit 2
fi
namespace=$1
deployment=$2
timeout_seconds=${3:-120}
kubectl_bin=${KUBECTL_BIN:-kubectl}
start_seconds=$SECONDS
last_failure_signature=
same_failure_count=0

fail_pod() {
  local pod=$1 container=$2 reason=$3
  printf 'Deployment %s failed in current ReplicaSet: pod=%s container=%s reason=%s\n' \
    "$deployment" "$pod" "$container" "$reason" >&2
  "$kubectl_bin" -n "$namespace" describe pod "$pod" >&2 || true
  "$kubectl_bin" -n "$namespace" logs "$pod" -c "$container" --tail=100 >&2 || true
  "$kubectl_bin" -n "$namespace" logs "$pod" -c "$container" --previous --tail=100 >&2 || true
  return 1
}

while (( SECONDS - start_seconds < timeout_seconds )); do
  if ! deployment_json=$("$kubectl_bin" -n "$namespace" get deployment "$deployment" -o json 2>/dev/null); then
    printf 'Deployment %s not found in namespace %s\n' "$deployment" "$namespace" >&2
    exit 1
  fi

  desired=$(printf '%s' "$deployment_json" | jq -r '.spec.replicas // 0')
  generation=$(printf '%s' "$deployment_json" | jq -r '.metadata.generation // 0')
  observed=$(printf '%s' "$deployment_json" | jq -r '.status.observedGeneration // 0')
  deployment_uid=$(printf '%s' "$deployment_json" | jq -r '.metadata.uid')
  revision=$(printf '%s' "$deployment_json" | jq -r '.metadata.annotations["deployment.kubernetes.io/revision"] // ""')
  if (( desired > 0 && observed >= generation )); then
    updated=$(printf '%s' "$deployment_json" | jq -r '.status.updatedReplicas // 0')
    total=$(printf '%s' "$deployment_json" | jq -r '.status.replicas // 0')
    available=$(printf '%s' "$deployment_json" | jq -r '.status.availableReplicas // 0')
    if (( updated >= desired && total == updated && available >= desired )); then
      printf 'Deployment %s is ready (%s/%s replicas)\n' "$deployment" "$available" "$desired"
      exit 0
    fi
    progress_failed=$(printf '%s' "$deployment_json" | jq -r '
      any(.status.conditions[]?; .type == "Progressing" and .status == "False" and .reason == "ProgressDeadlineExceeded")')
    if [[ "$progress_failed" == true ]]; then
      printf 'Deployment %s exceeded its Kubernetes progress deadline\n' "$deployment" >&2
      "$kubectl_bin" -n "$namespace" describe deployment "$deployment" >&2 || true
      exit 1
    fi
  fi

  selector=$(printf '%s' "$deployment_json" | jq -r \
    '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")')
  replica_set_json=$("$kubectl_bin" -n "$namespace" get replicasets -l "$selector" -o json)
  replica_set=$(printf '%s' "$replica_set_json" | jq -c \
    --arg owner "$deployment_uid" --arg revision "$revision" '
      [.items[]
       | select(any(.metadata.ownerReferences[]?; .uid == $owner))
       | select($revision == "" or .metadata.annotations["deployment.kubernetes.io/revision"] == $revision)]
      | sort_by(.metadata.creationTimestamp)
      | last // empty')

  if [[ -n "$replica_set" ]]; then
    replica_failure=$(printf '%s' "$replica_set" | jq -r '[.status.conditions[]? | select(.type == "ReplicaFailure" and .status == "True") | .message] | first // empty')
    if [[ -n "$replica_failure" ]]; then
      printf 'Deployment %s cannot create Pods: %s\n' "$deployment" "$replica_failure" >&2
      exit 1
    fi
    replica_set_uid=$(printf '%s' "$replica_set" | jq -r '.metadata.uid')
    replica_set_selector=$(printf '%s' "$replica_set" | jq -r \
      '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")')
    pods_json=$("$kubectl_bin" -n "$namespace" get pods -l "$replica_set_selector" -o json)
    failure=$(printf '%s' "$pods_json" | jq -c --arg owner "$replica_set_uid" '
      [ .items[]
        | select(.metadata.deletionTimestamp == null)
        | select(any(.metadata.ownerReferences[]?; .uid == $owner))
        | . as $pod
        | ((.status.initContainerStatuses // []) + (.status.containerStatuses // []))[]
        | select(.state.waiting.reason == "ImagePullBackOff"
          or .state.waiting.reason == "CreateContainerConfigError"
          or .state.waiting.reason == "InvalidImageName"
          or .state.waiting.reason == "CreateContainerError"
          or .state.waiting.reason == "RunContainerError"
          or .state.waiting.reason == "CrashLoopBackOff"
          or .state.waiting.reason == "ErrImagePull")
        | {pod: $pod.metadata.name, container: .name, reason: .state.waiting.reason,
           restartCount: (.restartCount // 0)}
      ] | first // empty')
    if [[ -n "$failure" ]]; then
      failure_pod=$(printf '%s' "$failure" | jq -r '.pod')
      failure_container=$(printf '%s' "$failure" | jq -r '.container')
      failure_reason=$(printf '%s' "$failure" | jq -r '.reason')
      failure_restarts=$(printf '%s' "$failure" | jq -r '.restartCount')
      signature="$replica_set_uid/$failure_pod/$failure_container/$failure_reason"
      case "$failure_reason" in
        ImagePullBackOff|CreateContainerConfigError|InvalidImageName|CreateContainerError|RunContainerError)
          fail_pod "$failure_pod" "$failure_container" "$failure_reason"
          exit 1
          ;;
        CrashLoopBackOff)
          if (( failure_restarts >= 1 )); then
            if [[ "$signature" == "$last_failure_signature" ]]; then
              same_failure_count=$((same_failure_count + 1))
            else
              last_failure_signature=$signature
              same_failure_count=1
            fi
            if (( same_failure_count >= 2 )); then
              fail_pod "$failure_pod" "$failure_container" "$failure_reason"
              exit 1
            fi
          fi
          ;;
        ErrImagePull)
          if [[ "$signature" == "$last_failure_signature" ]]; then
            same_failure_count=$((same_failure_count + 1))
          else
            last_failure_signature=$signature
            same_failure_count=1
          fi
          if (( same_failure_count >= 3 )); then
            fail_pod "$failure_pod" "$failure_container" "$failure_reason"
            exit 1
          fi
          ;;
      esac
    else
      last_failure_signature=
      same_failure_count=0
    fi
  fi

  sleep 1
done

printf 'Deployment %s did not become ready within %ss\n' "$deployment" "$timeout_seconds" >&2
"$kubectl_bin" -n "$namespace" describe deployment "$deployment" >&2 || true
exit 124
