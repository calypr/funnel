package taskservice

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
)

// Built-in service-info values, reported for every field that
// Server.ServiceName or Server.ServiceInfo leaves empty.
const (
	defaultServiceName        = "Funnel"
	defaultServiceID          = "org.ga4gh.funnel"
	defaultServiceDescription = "Funnel is a toolkit for distributed task execution via a simple, standard API."
	defaultOrganizationName   = "CALYPR Data Platform"
	defaultOrganizationURL    = "https://github.com/calypr/"
	defaultContactURL         = "https://github.com/calypr/"
	defaultDocumentationURL   = "https://github.com/calypr/funnel"
	defaultEnvironment        = "development"
	defaultCreatedAt          = "2016-03-21T16:27:49-07:00"
)

func defaultStorage() []string {
	return []string{
		"file:///path/to/local/funnel-storage",
		"s3://ohsu-compbio-funnel/storage",
	}
}

// applyServiceInfo fills the identity fields of resp from the service name and
// the configured overrides, keeping the built-in value for every empty field.
func applyServiceInfo(resp *tes.ServiceInfo, name string, info *config.ServiceInfo) {
	resp.Name = valueOr(name, defaultServiceName)
	resp.Id = valueOr(info.GetId(), defaultServiceID)
	resp.Description = valueOr(info.GetDescription(), defaultServiceDescription)
	resp.ContactUrl = valueOr(info.GetContactUrl(), defaultContactURL)
	resp.DocumentationUrl = valueOr(info.GetDocumentationUrl(), defaultDocumentationURL)
	resp.Environment = valueOr(info.GetEnvironment(), defaultEnvironment)
	resp.CreatedAt = valueOr(info.GetCreatedAt(), defaultCreatedAt)

	org := info.GetOrganization()
	if org.GetName() == "" && org.GetUrl() == "" {
		resp.Organization = map[string]string{"name": defaultOrganizationName, "url": defaultOrganizationURL}
	} else {
		resp.Organization = map[string]string{"name": org.GetName(), "url": org.GetUrl()}
	}

	if storage := info.GetStorage(); len(storage) > 0 {
		resp.Storage = append([]string(nil), storage...)
	} else {
		resp.Storage = defaultStorage()
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ValidateServiceInfo checks the configured service name and service-info
// overrides against the GA4GH service-info specification (v1.0.0) and the
// TES service-info extension. Empty values are valid: they keep Funnel's
// built-in values. All invalid fields are reported together.
func ValidateServiceInfo(name string, info *config.ServiceInfo) error {
	var errs []error
	errs = append(errs, validateNotBlank("Server.ServiceName", name))
	if info == nil {
		return errors.Join(errs...)
	}

	errs = append(errs,
		validateNotBlank("Server.ServiceInfo.Id", info.Id),
		validateNotBlank("Server.ServiceInfo.Description", info.Description),
		validateNotBlank("Server.ServiceInfo.Environment", info.Environment),
		validateURI("Server.ServiceInfo.ContactUrl", info.ContactUrl),
		validateURI("Server.ServiceInfo.DocumentationUrl", info.DocumentationUrl),
		validateDateTime("Server.ServiceInfo.CreatedAt", info.CreatedAt),
	)

	if org := info.Organization; org.GetName() != "" || org.GetUrl() != "" {
		// The specification requires both name and url in organization.
		errs = append(errs,
			validateRequired("Server.ServiceInfo.Organization.Name", org.Name),
			validateNotBlank("Server.ServiceInfo.Organization.Name", org.Name),
			validateRequired("Server.ServiceInfo.Organization.Url", org.Url),
			validateURI("Server.ServiceInfo.Organization.Url", org.Url),
		)
	}

	for i, location := range info.Storage {
		field := fmt.Sprintf("Server.ServiceInfo.Storage[%d]", i)
		errs = append(errs, validateRequired(field, location), validateNotBlank(field, location))
	}

	return errors.Join(errs...)
}

func validateRequired(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must be set", field)
	}
	return nil
}

func validateNotBlank(field, value string) error {
	if value != "" && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not consist only of whitespace", field)
	}
	return nil
}

// validateURI accepts absolute URIs (RFC 3986), including mailto: URIs
// (RFC 2368/6068). HTTP(S) URIs must name a host.
func validateURI(field, value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	valid := err == nil &&
		!strings.ContainsAny(value, " \t\r\n") &&
		u.Scheme != "" &&
		(u.Host != "" || u.Opaque != "" || u.Path != "")
	if valid && (u.Scheme == "http" || u.Scheme == "https") {
		valid = u.Host != ""
	}
	if !valid {
		return fmt.Errorf("%s must be an absolute URI (RFC 3986), such as https://example.org or mailto:admin@example.org; got %q", field, value)
	}
	return nil
}

// validateDateTime accepts RFC 3339 timestamps with a time zone.
func validateDateTime(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("%s must be an RFC 3339 timestamp, such as 2026-01-01T00:00:00Z; got %q", field, value)
	}
	return nil
}
