// Package storageplan resolves native Kubernetes storage objects for one task.
package storageplan

import (
	"fmt"

	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/storage"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

func hasS3MountedReads(backends []*config.GenericS3Storage) bool {
	for _, backend := range backends {
		if backend != nil && backend.MountedRead != nil {
			return true
		}
	}
	return false
}

func hasS3MountedWrites(backends []*config.GenericS3Storage) bool {
	for _, backend := range backends {
		if backend != nil && backend.MountedWrite != nil {
			return true
		}
	}
	return false
}

func resolveS3ReadMounts(backends []*config.GenericS3Storage, mountsByVolume map[string][]corev1.VolumeMount) ([]storage.S3ReadMount, error) {
	readMounts := make([]storage.S3ReadMount, 0)
	for _, backend := range backends {
		if backend == nil || backend.MountedRead == nil {
			continue
		}
		if !backend.Valid() {
			return nil, fmt.Errorf("S3 mounted read backend %q is disabled or invalid", backend.Bucket)
		}
		if backend.Bucket == "" {
			return nil, fmt.Errorf("S3 mounted read requires a bucket")
		}
		if matches := matchingS3Backends(backends, backend); matches > 1 {
			return nil, fmt.Errorf("duplicate mounted S3 backend %q bucket %q", backend.Endpoint, backend.Bucket)
		}
		var subPath *string
		if backend.MountedRead.MountView != nil {
			value := backend.MountedRead.GetSubPath()
			subPath = &value
		}
		mount, err := selectS3Mount(backend.MountedRead.Volume, subPath, mountsByVolume, "read")
		if err != nil {
			return nil, err
		}
		readMounts = append(readMounts, storage.S3ReadMount{
			Endpoint: backend.Endpoint,
			Bucket:   backend.Bucket,
			Prefix:   backend.MountedRead.Prefix,
			Root:     mount.MountPath,
		})
	}
	if err := storage.ValidateS3ReadMounts(readMounts); err != nil {
		return nil, fmt.Errorf("GenericS3 mounted reads: %w", err)
	}
	return readMounts, nil
}

func resolveS3WriteMounts(backends []*config.GenericS3Storage, mountsByVolume map[string][]corev1.VolumeMount) ([]storage.S3WriteMount, error) {
	writeMounts := make([]storage.S3WriteMount, 0)
	for _, backend := range backends {
		if backend == nil || backend.MountedWrite == nil {
			continue
		}
		if !backend.Valid() {
			return nil, fmt.Errorf("S3 mounted write backend %q is disabled or invalid", backend.Bucket)
		}
		if backend.Bucket == "" {
			return nil, fmt.Errorf("S3 mounted write requires a bucket")
		}
		if matches := matchingS3Backends(backends, backend); matches > 1 {
			return nil, fmt.Errorf("duplicate mounted S3 backend %q bucket %q", backend.Endpoint, backend.Bucket)
		}
		var subPath *string
		if backend.MountedWrite.MountView != nil {
			value := backend.MountedWrite.GetSubPath()
			subPath = &value
		}
		mount, err := selectS3Mount(backend.MountedWrite.Volume, subPath, mountsByVolume, "write")
		if err != nil {
			return nil, err
		}
		if mount.ReadOnly {
			return nil, fmt.Errorf("S3 mounted write volume %q mount %q must be writable", backend.MountedWrite.Volume, mount.MountPath)
		}
		timeout := s3WriteTimeout
		if configured := backend.MountedWrite.Timeout; configured != nil {
			if err := configured.CheckValid(); err != nil {
				return nil, fmt.Errorf("S3 mounted write timeout: %w", err)
			}
			timeout = configured.AsDuration()
			if timeout <= 0 {
				return nil, fmt.Errorf("S3 mounted write timeout must be positive")
			}
		}
		writeMounts = append(writeMounts, storage.S3WriteMount{
			Endpoint: backend.Endpoint,
			Bucket:   backend.Bucket,
			Prefix:   backend.MountedWrite.Prefix,
			Root:     mount.MountPath,
			Timeout:  timeout,
		})
	}
	if err := storage.ValidateS3WriteMounts(writeMounts); err != nil {
		return nil, fmt.Errorf("GenericS3 mounted writes: %w", err)
	}
	return writeMounts, nil
}

func matchingS3Backends(backends []*config.GenericS3Storage, backend *config.GenericS3Storage) int {
	matches := 0
	for _, candidate := range backends {
		if candidate != nil && candidate.Valid() && candidate.Endpoint == backend.Endpoint && candidate.Bucket == backend.Bucket {
			matches++
		}
	}
	return matches
}

func selectS3Mount(volume string, subPath *string, mountsByVolume map[string][]corev1.VolumeMount, direction string) (corev1.VolumeMount, error) {
	if len(validation.IsDNS1123Label(volume)) != 0 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s has invalid volume reference %q", direction, volume)
	}
	candidates := mountsByVolume[volume]
	if len(candidates) == 0 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s references unknown worker volume %q", direction, volume)
	}
	if subPath == nil {
		if len(candidates) != 1 {
			return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s volume %q has %d worker mounts; specify SubPath", direction, volume, len(candidates))
		}
		return candidates[0], nil
	}
	var selected corev1.VolumeMount
	matches := 0
	for _, candidate := range candidates {
		if candidate.SubPath == *subPath && candidate.SubPathExpr == "" {
			selected = candidate
			matches++
		}
	}
	if matches == 0 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s volume %q has no static mount with subPath %q", direction, volume, *subPath)
	}
	if matches > 1 {
		return corev1.VolumeMount{}, fmt.Errorf("S3 mounted %s volume %q has multiple mounts with subPath %q", direction, volume, *subPath)
	}
	return selected, nil
}
