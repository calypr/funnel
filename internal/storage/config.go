package storage

import (
	"os"

	schema "github.com/calypr/funnel/config"
)

// LocalConfigured reports whether local storage is enabled with allowed directories.
func LocalConfigured(l *schema.LocalStorage) bool {
	return !l.Disabled && len(l.AllowedDirs) > 0
}

// GoogleConfigured reports whether Google storage is enabled.
func GoogleConfigured(g *schema.GoogleCloudStorage) bool {
	return !g.Disabled
}

// AmazonS3Configured reports whether S3 is enabled with complete or implicit credentials.
func AmazonS3Configured(s *schema.AmazonS3Storage) bool {
	creds := s.AWSConfig == nil || (s.AWSConfig.Key != "" && s.AWSConfig.Secret != "") || (s.AWSConfig.Key == "" && s.AWSConfig.Secret == "")
	return !s.Disabled && creds
}

// GenericS3Configured reports whether generic S3 has its required settings.
func GenericS3Configured(s *schema.GenericS3Storage) bool {
	return !s.Disabled && s.Key != "" && s.Secret != "" && s.Endpoint != ""
}

// SwiftConfigured resolves required settings from config and the environment.
func SwiftConfigured(s *schema.SwiftStorage) bool {
	user := s.UserName != "" || os.Getenv("OS_USERNAME") != ""
	password := s.Password != "" || os.Getenv("OS_PASSWORD") != ""
	authURL := s.AuthURL != "" || os.Getenv("OS_AUTH_URL") != ""
	tenantName := s.TenantName != "" || os.Getenv("OS_TENANT_NAME") != "" || os.Getenv("OS_PROJECT_NAME") != ""
	tenantID := s.TenantID != "" || os.Getenv("OS_TENANT_ID") != "" || os.Getenv("OS_PROJECT_ID") != ""
	region := s.RegionName != "" || os.Getenv("OS_REGION_NAME") != ""

	valid := user && password && authURL && tenantName && tenantID && region

	return !s.Disabled && valid
}

// HTTPConfigured reports whether HTTP storage is enabled.
func HTTPConfigured(h *schema.HTTPStorage) bool {
	return !h.Disabled
}

// FTPConfigured reports whether FTP storage is enabled.
func FTPConfigured(h *schema.FTPStorage) bool {
	return !h.Disabled
}
