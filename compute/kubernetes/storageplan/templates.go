// Package storageplan resolves native Kubernetes storage objects for one task.
package storageplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"google.golang.org/protobuf/types/known/structpb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

func parseVolumes(rawVolumes []*structpb.Struct, data map[string]interface{}) ([]corev1.Volume, error) {
	volumes := make([]corev1.Volume, 0, len(rawVolumes))
	seen := make(map[string]bool, len(rawVolumes))
	for i, raw := range rawVolumes {
		volume := corev1.Volume{}
		if err := decodeStruct(raw, data, &volume); err != nil {
			return nil, fmt.Errorf("Kubernetes.Storage.Volumes[%d]: %w", i, err)
		}
		if len(validation.IsDNS1123Label(volume.Name)) != 0 {
			return nil, fmt.Errorf("Kubernetes.Storage.Volumes[%d] has invalid name %q", i, volume.Name)
		}
		if seen[volume.Name] {
			return nil, fmt.Errorf("duplicate storage volume %q", volume.Name)
		}
		seen[volume.Name] = true
		if volume.PersistentVolumeClaim == nil || !validName(volume.PersistentVolumeClaim.ClaimName) {
			return nil, fmt.Errorf("storage volume %q must reference a valid PVC", volume.Name)
		}
		expected := corev1.VolumeSource{PersistentVolumeClaim: volume.PersistentVolumeClaim}
		if !reflect.DeepEqual(volume.VolumeSource, expected) {
			return nil, fmt.Errorf("storage volume %q must have exactly one PVC source", volume.Name)
		}
		volumes = append(volumes, volume)
	}
	return volumes, nil
}

func resolveWorkerMounts(rawMounts []*structpb.Struct, data map[string]interface{}) ([]corev1.VolumeMount, map[string][]corev1.VolumeMount, error) {
	workerMounts := make([]corev1.VolumeMount, 0, len(rawMounts))
	mountsByVolume := make(map[string][]corev1.VolumeMount)
	for i, raw := range rawMounts {
		mount := corev1.VolumeMount{}
		if err := decodeStruct(raw, data, &mount); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeMounts[%d]: %w", i, err)
		}
		if len(validation.IsDNS1123Label(mount.Name)) != 0 {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeMounts[%d] has invalid volume name %q", i, mount.Name)
		}
		if err := validateMount(mount); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeMounts[%d]: %w", i, err)
		}
		mountsByVolume[mount.Name] = append(mountsByVolume[mount.Name], mount)
		workerMounts = append(workerMounts, mount)
	}
	return workerMounts, mountsByVolume, nil
}

func parseClaimTemplates(rawClaims []*structpb.Struct, data map[string]interface{}, namespace string, volumes []corev1.Volume, workspaceVolume, taskID string) ([]*corev1.PersistentVolumeClaim, []corev1.Volume, error) {
	claims := make([]*corev1.PersistentVolumeClaim, 0, len(rawClaims))
	templateVolumes := make([]corev1.Volume, 0, len(rawClaims))
	explicitVolumes := make(map[string]bool, len(volumes))
	externalClaims := make(map[string]bool, len(volumes))
	logicalNames := make(map[string]bool, len(rawClaims))
	for _, volume := range volumes {
		explicitVolumes[volume.Name] = true
		externalClaims[volume.PersistentVolumeClaim.ClaimName] = true
	}
	for i, raw := range rawClaims {
		if raw == nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeClaimTemplates[%d]: native object must not be null", i)
		}
		metadata, ok := raw.AsMap()["metadata"].(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("PVC template %d requires metadata.name as a static DNS1123 label", i)
		}
		logicalName, ok := metadata["name"].(string)
		if !ok || strings.Contains(logicalName, "{{") || strings.Contains(logicalName, "}}") || len(validation.IsDNS1123Label(logicalName)) != 0 {
			return nil, nil, fmt.Errorf("PVC template %d metadata.name must be a static DNS1123 label; remove task templating because claim names are generated from the task ID", i)
		}
		if logicalNames[logicalName] {
			return nil, nil, fmt.Errorf("duplicate PVC template logical name %q", logicalName)
		}
		logicalNames[logicalName] = true
		if explicitVolumes[logicalName] {
			return nil, nil, fmt.Errorf("PVC template logical name %q collides with an explicit storage volume", logicalName)
		}
		claim := &corev1.PersistentVolumeClaim{}
		if err := decodeStruct(raw, data, claim); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.VolumeClaimTemplates[%d]: %w", i, err)
		}
		if claim.APIVersion == "" {
			claim.APIVersion = "v1"
		}
		if claim.Kind == "" {
			claim.Kind = "PersistentVolumeClaim"
		}
		if claim.APIVersion != "v1" || claim.Kind != "PersistentVolumeClaim" {
			return nil, nil, fmt.Errorf("PVC template %d must be a v1 PersistentVolumeClaim", i)
		}
		if claim.Namespace == "" {
			claim.Namespace = namespace
		}
		if claim.Namespace != namespace {
			return nil, nil, fmt.Errorf("PVC template %q namespace must be %q", logicalName, namespace)
		}
		if len(claim.OwnerReferences) != 0 {
			return nil, nil, fmt.Errorf("PVC template %q must not contain owner references", logicalName)
		}
		if claim.Spec.VolumeMode != nil && *claim.Spec.VolumeMode == corev1.PersistentVolumeBlock {
			return nil, nil, fmt.Errorf("PVC template %q must use filesystem volume mode", logicalName)
		}
		if claim.GenerateName != "" {
			return nil, nil, fmt.Errorf("PVC template %q must not contain generateName", logicalName)
		}
		claim.Name = taskID + "-" + logicalName
		if !validName(claim.Name) {
			return nil, nil, fmt.Errorf("generated PVC name %q is invalid or exceeds the Kubernetes DNS subdomain limit", claim.Name)
		}
		if externalClaims[claim.Name] {
			return nil, nil, fmt.Errorf("explicit storage volume references generated task-owned PVC %q; use logical volume name %q", claim.Name, logicalName)
		}
		if claim.Spec.VolumeName != "" && !validName(claim.Spec.VolumeName) {
			return nil, nil, fmt.Errorf("PVC template %q has invalid volumeName %q", logicalName, claim.Spec.VolumeName)
		}
		if logicalName == workspaceVolume && len(claim.Spec.AccessModes) > 0 {
			writable := false
			for _, mode := range claim.Spec.AccessModes {
				if mode == corev1.ReadWriteOncePod {
					return nil, nil, fmt.Errorf("workspace PVC cannot use ReadWriteOncePod: worker and executor run in separate Pods")
				}
				if mode == corev1.ReadWriteOnce || mode == corev1.ReadWriteMany {
					writable = true
				}
			}
			if !writable {
				return nil, nil, fmt.Errorf("workspace PVC must support writable access from worker and executor Pods")
			}
		}
		claims = append(claims, claim)
		templateVolumes = append(templateVolumes, corev1.Volume{
			Name: logicalName,
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: claim.Name,
			}},
		})
	}
	return claims, templateVolumes, nil
}

func parsePersistentVolumeTemplates(rawVolumes []*structpb.Struct, data map[string]interface{}, taskID string) ([]*corev1.PersistentVolume, map[string]*corev1.PersistentVolume, error) {
	volumes := make([]*corev1.PersistentVolume, 0, len(rawVolumes))
	byName := make(map[string]*corev1.PersistentVolume, len(rawVolumes))
	for i, raw := range rawVolumes {
		volume := &corev1.PersistentVolume{}
		if err := decodeStruct(raw, data, volume); err != nil {
			return nil, nil, fmt.Errorf("Kubernetes.Storage.PersistentVolumeTemplates[%d]: %w", i, err)
		}
		if volume.APIVersion != "v1" || volume.Kind != "PersistentVolume" {
			return nil, nil, fmt.Errorf("PV template %d must be a v1 PersistentVolume", i)
		}
		if volume.Namespace != "" || volume.GenerateName != "" || !validName(volume.Name) || !strings.Contains(volume.Name, taskID) {
			return nil, nil, fmt.Errorf("PV template %d requires a cluster-scoped explicit task-specific name", i)
		}
		if len(volume.OwnerReferences) != 0 {
			return nil, nil, fmt.Errorf("PV %q must not contain owner references", volume.Name)
		}
		if volume.Spec.VolumeMode != nil && *volume.Spec.VolumeMode == corev1.PersistentVolumeBlock {
			return nil, nil, fmt.Errorf("PV %q must use filesystem volume mode", volume.Name)
		}
		if persistentVolumeSourceCount(volume.Spec.PersistentVolumeSource) != 1 {
			return nil, nil, fmt.Errorf("PV %q must have exactly one native volume source", volume.Name)
		}
		if _, exists := byName[volume.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate PV template name %q", volume.Name)
		}
		byName[volume.Name] = volume
		volumes = append(volumes, volume)
	}
	return volumes, byName, nil
}

func persistentVolumeSourceCount(source corev1.PersistentVolumeSource) int {
	value := reflect.ValueOf(source)
	count := 0
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Pointer && !field.IsNil() {
			count++
		}
	}
	return count
}

func linkResources(plan *Plan, volumes map[string]*corev1.PersistentVolume) error {
	linkedPVClaims := make(map[string]string, len(volumes))
	for _, claim := range plan.Claims {
		pv, managedPV := volumes[claim.Spec.VolumeName]
		if !managedPV {
			continue
		}
		if previous := linkedPVClaims[pv.Name]; previous != "" {
			return fmt.Errorf("PV template %q is referenced by multiple PVC templates %q and %q", pv.Name, previous, claim.Name)
		}
		linkedPVClaims[pv.Name] = claim.Name
		if pv.Spec.ClaimRef == nil {
			pv.Spec.ClaimRef = &corev1.ObjectReference{}
		}
		ref := pv.Spec.ClaimRef
		if (ref.Name != "" && ref.Name != claim.Name) || (ref.Namespace != "" && ref.Namespace != claim.Namespace) || ref.UID != "" || (ref.Kind != "" && ref.Kind != "PersistentVolumeClaim") || (ref.APIVersion != "" && ref.APIVersion != "v1") {
			return fmt.Errorf("PV %q claimRef conflicts with PVC %q", pv.Name, claim.Name)
		}
		ref.Name, ref.Namespace = claim.Name, claim.Namespace
	}
	for name := range volumes {
		if linkedPVClaims[name] == "" {
			return fmt.Errorf("PV template %q is not referenced by exactly one PVC template", name)
		}
	}
	return nil
}

func decodeStruct(raw *structpb.Struct, data map[string]interface{}, target interface{}) error {
	if raw == nil {
		return fmt.Errorf("native object must not be null")
	}
	value, err := renderStringValues(raw.AsMap(), data)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func renderStringValues(value interface{}, data map[string]interface{}) (interface{}, error) {
	switch value := value.(type) {
	case string:
		t, err := template.New("storage-string").Option("missingkey=error").Parse(value)
		if err != nil {
			return nil, err
		}
		var rendered bytes.Buffer
		if err := t.Execute(&rendered, data); err != nil {
			return nil, err
		}
		return rendered.String(), nil
	case []interface{}:
		for i, item := range value {
			rendered, err := renderStringValues(item, data)
			if err != nil {
				return nil, err
			}
			value[i] = rendered
		}
		return value, nil
	case map[string]interface{}:
		for key, item := range value {
			rendered, err := renderStringValues(item, data)
			if err != nil {
				return nil, err
			}
			value[key] = rendered
		}
		return value, nil
	default:
		return value, nil
	}
}
