// Package config contains Funnel configuration structures and defaults.
package config

import (
	"io"
	"os"
)

// HTTPAddress returns the HTTP address based on HostName and HTTPPort
func (c *Server) HTTPAddress() string {
	http := ""
	if c.HostName != "" {
		http = "http://" + c.HostName
	}
	if c.HTTPPort != "" {
		http = http + ":" + c.HTTPPort
	}
	return http
}

// RPCAddress returns the RPC address based on HostName and RPCPort
func (c *Server) RPCAddress() string {
	rpc := c.HostName
	if c.RPCPort != "" {
		rpc = rpc + ":" + c.RPCPort
	}
	return rpc
}

// Valid validates the LocalStorage configuration
func (l *LocalStorage) Valid() bool {
	return !l.Disabled && len(l.AllowedDirs) > 0
}

// Valid validates the Storage configuration.
func (g *GoogleCloudStorage) Valid() bool {
	return !g.Disabled
}

// Valid validates the AmazonS3Storage configuration
func (s *AmazonS3Storage) Valid() bool {
	creds := s.AWSConfig == nil || (s.AWSConfig.Key != "" && s.AWSConfig.Secret != "") || (s.AWSConfig.Key == "" && s.AWSConfig.Secret == "")
	return !s.Disabled && creds
}

// Valid validates the S3Storage configuration
func (s *GenericS3Storage) Valid() bool {
	return !s.Disabled && s.Key != "" && s.Secret != "" && s.Endpoint != ""
}

// Valid validates the SwiftStorage configuration.
func (s *SwiftStorage) Valid() bool {
	user := s.UserName != "" || os.Getenv("OS_USERNAME") != ""
	password := s.Password != "" || os.Getenv("OS_PASSWORD") != ""
	authURL := s.AuthURL != "" || os.Getenv("OS_AUTH_URL") != ""
	tenantName := s.TenantName != "" || os.Getenv("OS_TENANT_NAME") != "" || os.Getenv("OS_PROJECT_NAME") != ""
	tenantID := s.TenantID != "" || os.Getenv("OS_TENANT_ID") != "" || os.Getenv("OS_PROJECT_ID") != ""
	region := s.RegionName != "" || os.Getenv("OS_REGION_NAME") != ""

	valid := user && password && authURL && tenantName && tenantID && region

	return !s.Disabled && valid
}

// Valid validates the HTTPStorage configuration.
func (h *HTTPStorage) Valid() bool {
	return !h.Disabled
}

// Valid validates the FTPStorage configuration.
func (h *FTPStorage) Valid() bool {
	return !h.Disabled
}
