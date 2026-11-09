// Package kubernetes contains code for accessing compute resources via the Kubernetes v1 Batch API.
package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// isJobDone reports whether the job has finished — either succeeded
// or permanently failed (backoffLimit exhausted) — as opposed to still
// being retried or in progress.
func (b *Backend) isJobDone(jobStatus v1.JobStatus) bool {
	for _, cond := range jobStatus.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if cond.Type == v1.JobComplete || cond.Type == v1.JobFailed {
			return true
		}
	}
	return false
}

// hasTerminalContainerWaitingError returns true if any pod in pods has a
// container stuck in a waiting state whose reason is known to be permanent
// (e.g. CreateContainerConfigError). These pods will never transition to a
// running state on their own so the task must be failed early rather than
// waiting for the Job's backoff limit to be exhausted.
func hasTerminalContainerWaitingError(pods *corev1.PodList) (bool, string) {
	terminalWaitingReasons := []string{
		"CreateContainerConfigError", // missing secret / configmap
		"InvalidImageName",           // malformed image reference
		"CreateContainerError",       // OCI runtime failed to create container
		"ErrImagePull",               // image not found or pull failed
		"ImagePullBackOff",           // repeated image pull failure
		"RunContainerError",          // runtime failed to start container (e.g. bad entrypoint)
		"StartError",                 // OCI runtime runc create failed
	}
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting == nil {
				continue
			}
			reason := cs.State.Waiting.Reason
			if slices.Contains(terminalWaitingReasons, reason) {
				msg := cs.State.Waiting.Message
				if msg == "" {
					msg = reason
				}
				return true, fmt.Sprintf("%s: %s", reason, msg)
			}
		}
	}
	return false, ""
}

// podWarningEventReasons lists the pod event reasons (from `kubectl describe pod`)
// that are safe to surface to users as system log entries. These are all
// informational failure signals with no risk of leaking sensitive runtime internals.
// FetchPodWarningEvents returns Warning events for the given pods whose reason
// is in podWarningEventReasons. The messages are deduplicated and returned as a
// newline-joined string. An empty string is returned when nothing useful is
// found. This surfaces the human-readable detail that appears in
// `kubectl describe pod` (e.g. "Error: secret \"foo\" not found") into the
// TES task system logs.
func FetchPodWarningEvents(ctx context.Context, clientset kubernetes.Interface, namespace string, pods *corev1.PodList) string {
	seen := make(map[string]struct{})
	var messages []string
	for _, pod := range pods.Items {
		evList, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,type=Warning", pod.Name),
		})
		if err != nil {
			continue
		}
		for _, ev := range evList.Items {
			if !slices.Contains(podWarningEventReasons, ev.Reason) {
				continue
			}
			key := ev.Reason + ":" + ev.Message
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				messages = append(messages, fmt.Sprintf("%s: %s", ev.Reason, ev.Message))
			}
		}
	}
	return strings.Join(messages, "\n")
}

// isJobSchedulingTimedOut returns true if all pods for the given job have been
// stuck in Pending (with a scheduling condition) for longer than timeout.
// It returns false if any pod has been scheduled, or if pod status cannot be determined.
func (b *Backend) isJobSchedulingTimedOut(timeout time.Duration, pods *corev1.PodList) bool {

	if len(pods.Items) == 0 {
		return false
	}
	now := time.Now()
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodPending {
			return false
		}
		// Find the most recent scheduling condition transition time
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				if now.Sub(cond.LastTransitionTime.Time) >= timeout {
					return true
				}
			}
		}
	}
	return false
}

// getFailedPodInfo returns a human-readable summary of why the most recently
// terminated pod for jobName failed: "exit code N (Reason): Message". It is
// best-effort; an empty string is returned when no useful information is found.
func (b *Backend) getFailedPodInfo(ctx context.Context, pods *corev1.PodList) string {

	var latestFinish metav1.Time
	var result string
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			t := cs.State.Terminated
			if t == nil || t.ExitCode == 0 {
				continue
			}
			if latestFinish.IsZero() || t.FinishedAt.After(latestFinish.Time) {
				latestFinish = t.FinishedAt
				reason := t.Reason
				if reason == "" {
					reason = "ExitError"
				}
				result = fmt.Sprintf("exit code %d (%s): %s", t.ExitCode, reason, t.Message)
			}
		}
	}
	return result
}

// failedCreateThreshold is the minimum number of FailedCreate events required
// before the reconciler treats the job as persistently broken.
// hasJobFailedCreateEvent returns (totalCount, message) when the job has
// accumulated enough FailedCreate events spread over enough real time and no
// SuccessfulCreate has occurred after the last failure. Returns (0, "") when
// the failures look transient or have self-resolved.
//
// A FailedCreate event is emitted when the Job controller tried to create a pod
// but was rejected before the pod object was ever persisted (e.g. Pod Security
// Admission enforcement). In that case there are no pod container statuses to
// inspect, so hasTerminalContainerWaitingError cannot detect the failure.
func (b *Backend) hasJobFailedCreateEvent(ctx context.Context, jobName string) (int, string) {
	ns := b.conf.Kubernetes.JobsNamespace

	allEvents, err := b.client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", jobName),
	})
	if err != nil {
		b.log.Error("reconcile: listing events for job", "taskID", jobName, "error", err)
		return 0, ""
	}

	var (
		firstFailed   metav1.Time
		latestFailed  metav1.Time
		latestMsg     string
		totalCount    int
		latestSuccess metav1.Time
	)

	for _, ev := range allEvents.Items {
		last := ev.LastTimestamp
		if last.IsZero() {
			last = metav1.Time{Time: ev.CreationTimestamp.Time}
		}
		switch ev.Reason {
		case "FailedCreate":
			c := int(ev.Count)
			if c < 1 {
				c = 1 // brand-new singleton events have Count=0
			}
			totalCount += c
			// Use FirstTimestamp (the time of the first occurrence in a
			// deduplicated event) to measure how long the failures have been
			// occurring. Fall back to LastTimestamp if FirstTimestamp is unset.
			first := ev.FirstTimestamp
			if first.IsZero() {
				first = last
			}
			if firstFailed.IsZero() || first.Before(&firstFailed) {
				firstFailed = first
			}
			if latestFailed.IsZero() || last.After(latestFailed.Time) {
				latestFailed = last
				latestMsg = ev.Message
			}
		case "SuccessfulCreate":
			if latestSuccess.IsZero() || last.After(latestSuccess.Time) {
				latestSuccess = last
			}
		}
	}

	if totalCount == 0 {
		return 0, ""
	}

	// If a SuccessfulCreate occurred after the last failure the Job controller
	// recovered on its own; do not surface this as an error.
	if !latestSuccess.IsZero() && latestSuccess.After(latestFailed.Time) {
		b.log.Debug("reconcile: FailedCreate resolved by later SuccessfulCreate", "taskID", jobName)
		return 0, ""
	}

	// Require both a minimum count and a minimum span between first and last
	// failure before treating this as persistent.
	failureSpan := latestFailed.Time.Sub(firstFailed.Time)
	if totalCount < failedCreateThreshold || failureSpan < minFailureSpan {
		b.log.Debug("reconcile: FailedCreate below persistence threshold, skipping",
			"taskID", jobName, "count", totalCount, "span", failureSpan)
		return 0, ""
	}

	b.log.Debug("reconcile: persistent unresolved FailedCreate events",
		"taskID", jobName, "count", totalCount, "span", failureSpan, "reason", latestMsg)
	return totalCount, latestMsg
}

// listAllWorkerJobs returns a map of taskID -> Job for all funnel-worker jobs.
