package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	storageOwnedLabel         = "funnel.calypr.org/storage-owned"
	storageOwnerUIDAnnotation = "funnel.calypr.org/storage-owner-uid"
)

func (b *Backend) validatePersistentVolumePermissions(ctx context.Context, plan *storageplan.Plan) error {
	if plan == nil || len(plan.PersistentVolumes) == 0 {
		return nil
	}

	reviews := b.client.AuthorizationV1().SelfSubjectAccessReviews()
	check := func(verb, name string) error {
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Verb:     verb,
					Version:  "v1",
					Resource: "persistentvolumes",
					Name:     name,
				},
			},
		}
		response, err := reviews.Create(ctx, review, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("request %s authorization for persistentvolumes %q: %w", verb, name, err)
		}
		if response == nil {
			return fmt.Errorf("request %s authorization for persistentvolumes %q returned no status", verb, name)
		}
		if response.Status.Allowed && !response.Status.Denied {
			return nil
		}

		decision := "no opinion"
		if response.Status.Denied {
			decision = "denied"
		}
		return fmt.Errorf("Kubernetes authorization %s %s on persistentvolumes %q (reason=%q, evaluationError=%q)", decision, verb, name, response.Status.Reason, response.Status.EvaluationError)
	}

	if err := check("create", ""); err != nil {
		return err
	}
	for _, volume := range plan.PersistentVolumes {
		for _, verb := range []string{"get", "delete"} {
			if err := check(verb, volume.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Backend) validateExistingStorage(ctx context.Context, plan *storageplan.Plan) error {
	if plan == nil {
		return nil
	}
	owned := make(map[string]bool, len(plan.OwnedClaims))
	for _, name := range plan.OwnedClaims {
		owned[name] = true
	}
	for _, volume := range plan.Volumes {
		name := volume.PersistentVolumeClaim.ClaimName
		if owned[name] {
			continue
		}
		claim, err := b.client.CoreV1().PersistentVolumeClaims(plan.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get existing storage claim %s/%s for volume %q: %w", plan.Namespace, name, volume.Name, err)
		}
		if claim.Namespace != plan.Namespace {
			return fmt.Errorf("existing storage claim %q must belong to namespace %q", name, plan.Namespace)
		}
		if claim.Spec.VolumeMode != nil && *claim.Spec.VolumeMode == corev1.PersistentVolumeBlock {
			return fmt.Errorf("existing storage claim %s/%s must use filesystem volume mode", plan.Namespace, name)
		}
		if err := plan.ValidateMountedWriteClaim(claim); err != nil {
			return err
		}
		if volume.Name != plan.WorkspaceVolume {
			continue
		}
		writable := false
		for _, mode := range claim.Spec.AccessModes {
			if mode == corev1.ReadWriteOncePod {
				return fmt.Errorf("workspace claim %s/%s cannot use ReadWriteOncePod: worker and executor pods share the workspace", plan.Namespace, name)
			}
			if mode == corev1.ReadWriteOnce || mode == corev1.ReadWriteMany {
				writable = true
			}
		}
		if !writable {
			return fmt.Errorf("workspace claim %s/%s must support ReadWriteOnce or ReadWriteMany access", plan.Namespace, name)
		}
	}
	return nil
}

func (b *Backend) releaseStorageFinalizer(ctx context.Context, taskID, namespace string, ownerUID types.UID) error {
	if ownerUID == "" {
		return fmt.Errorf("releasing storage cleanup requires the worker Job UID")
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		job, err := b.client.BatchV1().Jobs(namespace).Get(ctx, taskID, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if job.UID != ownerUID {
			return fmt.Errorf("worker Job %s/%s changed UID while releasing storage cleanup", namespace, taskID)
		}
		updated := job.DeepCopy()
		updated.Finalizers = nil
		removed := false
		for _, finalizer := range job.Finalizers {
			if finalizer == storageplan.Finalizer {
				removed = true
				continue
			}
			updated.Finalizers = append(updated.Finalizers, finalizer)
		}
		if !removed {
			return nil
		}
		_, err = b.client.BatchV1().Jobs(namespace).Update(ctx, updated, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("release storage cleanup finalizer on Job %s/%s: %w", namespace, taskID, err)
	}
	return nil
}

func (b *Backend) createBoundStorage(ctx context.Context, taskID, namespace string, plan *storageplan.Plan, owner metav1.OwnerReference) error {
	if plan == nil || len(plan.PersistentVolumes)+len(plan.Claims) == 0 {
		return nil
	}
	if owner.UID == "" {
		return fmt.Errorf("creating bound storage requires the worker Job UID")
	}
	var createdVolumes []string
	checkOwner := func() error {
		if err := b.validateStorageOwner(ctx, namespace, owner); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			return errors.Join(err, b.deleteBoundVolumes(cleanupCtx, taskID, namespace, createdVolumes, owner.UID))
		}
		return nil
	}
	for _, planned := range plan.PersistentVolumes {
		pv := planned.DeepCopy()
		markBoundStorage(&pv.ObjectMeta, taskID, namespace, owner)
		// A cluster-scoped PV cannot reference a namespaced Job as its owner.
		if len(pv.OwnerReferences) != 0 {
			return fmt.Errorf("persistent volume %q has conflicting owner references", pv.Name)
		}
		if err := checkOwner(); err != nil {
			return err
		}
		_, err := b.client.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			existing, getErr := b.client.CoreV1().PersistentVolumes().Get(ctx, pv.Name, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("get existing persistent volume %q: %w", pv.Name, getErr)
			}
			if !boundStorageIdentity(existing.ObjectMeta, taskID, namespace, owner) || len(existing.OwnerReferences) != 0 || !boundVolumeSpecEqual(pv.Spec, existing.Spec) {
				return fmt.Errorf("persistent volume %q already exists with conflicting ownership or spec", pv.Name)
			}
			err = nil
		}
		if err != nil {
			return fmt.Errorf("create persistent volume %q: %w", pv.Name, err)
		}
		createdVolumes = append(createdVolumes, pv.Name)
		if err := checkOwner(); err != nil {
			return err
		}
	}
	for _, planned := range plan.Claims {
		claim := planned.DeepCopy()
		markBoundStorage(&claim.ObjectMeta, taskID, namespace, owner)
		claim.Namespace = namespace
		if len(claim.OwnerReferences) != 0 && !equality.Semantic.DeepEqual(claim.OwnerReferences, []metav1.OwnerReference{owner}) {
			return fmt.Errorf("persistent volume claim %q has conflicting owner references", claim.Name)
		}
		claim.OwnerReferences = []metav1.OwnerReference{owner}
		if err := checkOwner(); err != nil {
			return err
		}
		_, err := b.client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, claim, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			existing, getErr := b.client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claim.Name, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("get existing persistent volume claim %q: %w", claim.Name, getErr)
			}
			if !boundStorageIdentity(existing.ObjectMeta, taskID, namespace, owner) || !equality.Semantic.DeepEqual(existing.OwnerReferences, claim.OwnerReferences) || !boundClaimSpecEqual(claim.Spec, existing.Spec) {
				return fmt.Errorf("persistent volume claim %q already exists with conflicting ownership or spec", claim.Name)
			}
			err = nil
		}
		if err != nil {
			return fmt.Errorf("create persistent volume claim %q: %w", claim.Name, err)
		}
		if err := checkOwner(); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) validateStorageOwner(ctx context.Context, namespace string, owner metav1.OwnerReference) error {
	job, err := b.client.BatchV1().Jobs(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get worker Job %s/%s while creating storage: %w", namespace, owner.Name, err)
	}
	if job.UID != owner.UID || job.DeletionTimestamp != nil {
		return fmt.Errorf("worker Job %s/%s was replaced or is deleting while creating storage", namespace, owner.Name)
	}
	canceled, err := b.taskCanceled(ctx, owner.Name)
	if err != nil {
		return fmt.Errorf("checking task state while creating storage: %w", err)
	}
	if canceled {
		return fmt.Errorf("task %q was canceled while creating storage", owner.Name)
	}
	return nil
}

func markBoundStorage(meta *metav1.ObjectMeta, taskID, namespace string, owner metav1.OwnerReference) {
	if meta.Labels == nil {
		meta.Labels = make(map[string]string)
	}
	meta.Labels["app"] = "funnel"
	meta.Labels["taskId"] = taskID
	meta.Labels["namespace"] = namespace
	meta.Labels[storageOwnedLabel] = "true"
	if meta.Annotations == nil {
		meta.Annotations = make(map[string]string)
	}
	meta.Annotations[storageOwnerUIDAnnotation] = string(owner.UID)
}

func boundStorageIdentity(meta metav1.ObjectMeta, taskID, namespace string, owner metav1.OwnerReference) bool {
	return meta.DeletionTimestamp == nil && ownedBoundVolume(meta, taskID, namespace) && meta.Annotations[storageOwnerUIDAnnotation] == string(owner.UID)
}

func ownedBoundVolume(meta metav1.ObjectMeta, taskID, namespace string) bool {
	return meta.Labels["app"] == "funnel" && meta.Labels["taskId"] == taskID && meta.Labels["namespace"] == namespace && meta.Labels[storageOwnedLabel] == "true"
}

func boundVolumeSpecEqual(desired, actual corev1.PersistentVolumeSpec) bool {
	desired = *desired.DeepCopy()
	actual = *actual.DeepCopy()
	if desired.VolumeMode == nil && actual.VolumeMode != nil && *actual.VolumeMode == corev1.PersistentVolumeFilesystem {
		desired.VolumeMode = actual.VolumeMode
	}
	if desired.PersistentVolumeReclaimPolicy == "" && actual.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimRetain {
		desired.PersistentVolumeReclaimPolicy = actual.PersistentVolumeReclaimPolicy
	}
	if desired.HostPath != nil && actual.HostPath != nil && desired.HostPath.Type == nil && actual.HostPath.Type != nil && *actual.HostPath.Type == corev1.HostPathUnset {
		desired.HostPath.Type = actual.HostPath.Type
	}
	if desired.ClaimRef != nil && actual.ClaimRef != nil {
		// The binder fills in the claim's identity after creation.
		if desired.ClaimRef.UID == "" {
			actual.ClaimRef.UID = ""
		}
		if desired.ClaimRef.ResourceVersion == "" {
			actual.ClaimRef.ResourceVersion = ""
		}
		if desired.ClaimRef.APIVersion == "" && actual.ClaimRef.APIVersion == "v1" {
			actual.ClaimRef.APIVersion = ""
		}
		if desired.ClaimRef.Kind == "" && actual.ClaimRef.Kind == "PersistentVolumeClaim" {
			actual.ClaimRef.Kind = ""
		}
	}
	return equality.Semantic.DeepEqual(desired, actual)
}

func boundClaimSpecEqual(desired, actual corev1.PersistentVolumeClaimSpec) bool {
	desired = *desired.DeepCopy()
	if desired.VolumeMode == nil && actual.VolumeMode != nil && *actual.VolumeMode == corev1.PersistentVolumeFilesystem {
		desired.VolumeMode = actual.VolumeMode
	}
	if desired.StorageClassName == nil {
		desired.StorageClassName = actual.StorageClassName
	}
	if desired.VolumeName == "" {
		desired.VolumeName = actual.VolumeName
	}
	return equality.Semantic.DeepEqual(desired, actual)
}

func (b *Backend) deleteBoundVolumes(ctx context.Context, taskID, namespace string, names []string, ownerUID types.UID) error {
	if len(names) > 0 && ownerUID == "" {
		return fmt.Errorf("deleting bound storage requires the worker Job UID")
	}
	var errs []error
	for _, name := range names {
		if err := b.deleteOwnedBoundVolume(ctx, taskID, namespace, name, ownerUID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *Backend) deleteBoundVolume(ctx context.Context, taskID, namespace, name string) error {
	return b.deleteOwnedBoundVolume(ctx, taskID, namespace, name, "")
}

func (b *Backend) deleteOwnedBoundVolume(ctx context.Context, taskID, namespace, name string, ownerUID types.UID) error {
	pv, err := b.client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get persistent volume %q for cleanup: %w", name, err)
	}
	if !ownedBoundVolume(pv.ObjectMeta, taskID, namespace) {
		return nil
	}
	if ownerUID != "" && pv.Annotations[storageOwnerUIDAnnotation] != string(ownerUID) {
		return nil
	}
	uid := pv.UID
	err = b.client.CoreV1().PersistentVolumes().Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete persistent volume %q: %w", name, err)
	}
	return nil
}

func (b *Backend) usesPersistentVolumes() bool {
	if b.conf.Kubernetes.Storage == nil {
		return true
	}
	return len(b.conf.Kubernetes.Storage.PersistentVolumeTemplates) > 0
}
