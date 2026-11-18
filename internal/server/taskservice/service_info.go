package taskservice

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
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
	errs = append(errs, validateText("Server.ServiceName", name, false))
	if info == nil {
		return errors.Join(errs...)
	}

	errs = append(errs,
		validateText("Server.ServiceInfo.Id", info.Id, false),
		validateText("Server.ServiceInfo.Description", info.Description, false),
		validateText("Server.ServiceInfo.Environment", info.Environment, false),
		validateURL("Server.ServiceInfo.ContactUrl", info.ContactUrl, false, contactSchemes),
		validateURL("Server.ServiceInfo.DocumentationUrl", info.DocumentationUrl, false, webSchemes),
		validateDateTime("Server.ServiceInfo.CreatedAt", info.CreatedAt),
	)

	if org := info.Organization; org.GetName() != "" || org.GetUrl() != "" {
		// The specification requires both name and url in organization.
		errs = append(errs,
			validateText("Server.ServiceInfo.Organization.Name", org.Name, true),
			validateURL("Server.ServiceInfo.Organization.Url", org.Url, true, webSchemes),
		)
	}

	for i, location := range info.Storage {
		errs = append(errs, validateText(fmt.Sprintf("Server.ServiceInfo.Storage[%d]", i), location, true))
	}

	return errors.Join(errs...)
}

// validateText rejects whitespace-only values and, if required, empty ones.
func validateText(field, value string, required bool) error {
	switch {
	case value == "" && required:
		return fmt.Errorf("%s must be set", field)
	case value != "" && strings.TrimSpace(value) == "":
		return fmt.Errorf("%s must not consist only of whitespace", field)
	}
	return nil
}

var (
	// webSchemes are allowed for website and documentation URLs.
	webSchemes = []string{"http", "https"}
	// contactSchemes also allow an email address (RFC 2368/6068), which the
	// specification suggests for contactUrl.
	contactSchemes = []string{"http", "https", "mailto"}
)

// validateURL accepts absolute URIs (RFC 3986) with one of the given
// schemes. Service info is public and clients render these values as links,
// so other schemes (such as javascript: or data:) are rejected. HTTP(S) URLs
// must name a host; mailto: URIs must name an address.
func validateURL(field, value string, required bool, schemes []string) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s must be set", field)
		}
		return nil
	}
	u, err := url.Parse(value)
	valid := err == nil && onlyURICharacters(value) && slices.Contains(schemes, u.Scheme)
	if valid && u.Scheme == "mailto" {
		valid = u.Opaque != ""
	} else if valid {
		valid = u.Hostname() != ""
	}
	if !valid {
		return fmt.Errorf("%s must be an absolute %s URL (RFC 3986), such as https://example.org%s; got %q",
			field, strings.Join(schemes, ", "), mailtoExample(schemes), value)
	}
	return nil
}

func mailtoExample(schemes []string) string {
	if slices.Contains(schemes, "mailto") {
		return " or mailto:admin@example.org"
	}
	return ""
}

// onlyURICharacters reports whether value consists only of characters that
// RFC 3986 allows in a URI: unreserved and reserved characters, and "%" for
// percent-encoding.
func onlyURICharacters(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-._~:/?#[]@!$&'()*+,;=%", r):
		default:
			return false
		}
	}
	return true
}

// validateDateTime accepts RFC 3339 timestamps with a time zone. RFC 3339
// allows lower-case "t" and "z", which Go's parser doesn't, so the value is
// upper-cased before parsing.
func validateDateTime(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.ToUpper(value)); err != nil {
		return fmt.Errorf("%s must be an RFC 3339 timestamp, such as 2026-01-01T00:00:00Z; got %q", field, value)
	}
	return nil
}
