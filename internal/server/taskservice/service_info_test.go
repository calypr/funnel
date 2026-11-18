package taskservice

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/api/tes"
	"github.com/calypr/funnel/version"
)

func TestServiceInfoReportsVersionAndOrganization(t *testing.T) {
	previous := version.Version
	version.Version = "v-test"
	t.Cleanup(func() { version.Version = previous })
	service := &Service{Name: "Funnel"}
	info, err := service.GetServiceInfo(context.Background(), &tes.GetServiceInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "v-test" {
		t.Fatalf("service version = %q, want v-test", info.Version)
	}
	if info.Type == nil || info.Type.Version != "1.1.0" {
		t.Fatalf("service type = %+v, want TES version 1.1.0", info.Type)
	}
	if info.Organization["name"] != "CALYPR Data Platform" || info.Organization["url"] != "https://github.com/calypr/" {
		t.Fatalf("organization = %v", info.Organization)
	}
}

// builtinServiceInfo lists the values Funnel reports when nothing is configured.
// It deliberately repeats the literals instead of using the constants, so that
// it pins the values Funnel reported before they became configurable.
var builtinServiceInfo = &tes.ServiceInfo{
	Name:             "Funnel",
	Id:               "org.ga4gh.funnel",
	Description:      "Funnel is a toolkit for distributed task execution via a simple, standard API.",
	Organization:     map[string]string{"name": "CALYPR Data Platform", "url": "https://github.com/calypr/"},
	ContactUrl:       "https://github.com/calypr/",
	DocumentationUrl: "https://github.com/calypr/funnel",
	Environment:      "development",
	CreatedAt:        "2016-03-21T16:27:49-07:00",
	Storage:          []string{"file:///path/to/local/funnel-storage", "s3://ohsu-compbio-funnel/storage"},
}

func getServiceInfo(t *testing.T, service *Service) *tes.ServiceInfo {
	t.Helper()
	info, err := service.GetServiceInfo(context.Background(), &tes.GetServiceInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func assertConfigurableFields(t *testing.T, got, want *tes.ServiceInfo) {
	t.Helper()
	checks := []struct {
		field     string
		got, want any
	}{
		{"name", got.Name, want.Name},
		{"id", got.Id, want.Id},
		{"description", got.Description, want.Description},
		{"organization", got.Organization, want.Organization},
		{"contactUrl", got.ContactUrl, want.ContactUrl},
		{"documentationUrl", got.DocumentationUrl, want.DocumentationUrl},
		{"environment", got.Environment, want.Environment},
		{"createdAt", got.CreatedAt, want.CreatedAt},
		{"storage", got.Storage, want.Storage},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("%s = %#v, want %#v", check.field, check.got, check.want)
		}
	}
}

func TestServiceInfoKeepsBuiltinValuesWithoutConfiguration(t *testing.T) {
	for name, service := range map[string]*Service{
		"no config":              {Name: "Funnel"},
		"no server section":      {Name: "Funnel", Config: &config.Config{}},
		"no service info":        {Name: "Funnel", Config: &config.Config{Server: &config.Server{}}},
		"empty service info":     {Name: "Funnel", Config: &config.Config{Server: &config.Server{ServiceInfo: &config.ServiceInfo{}}}},
		"empty organization":     {Name: "Funnel", Config: &config.Config{Server: &config.Server{ServiceInfo: &config.ServiceInfo{Organization: &config.ServiceInfoOrganization{}}}}},
		"empty service name":     {Name: ""},
		"empty storage override": {Name: "Funnel", Config: &config.Config{Server: &config.Server{ServiceInfo: &config.ServiceInfo{Storage: []string{}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			assertConfigurableFields(t, getServiceInfo(t, service), builtinServiceInfo)
		})
	}
}

func TestServiceInfoReportsConfiguredValues(t *testing.T) {
	previous := version.Version
	version.Version = "v-test"
	t.Cleanup(func() { version.Version = previous })
	service := &Service{
		Name: "Example TES",
		Config: &config.Config{Server: &config.Server{ServiceInfo: &config.ServiceInfo{
			Id:               "org.example.tes",
			Description:      "TES for example.org",
			Organization:     &config.ServiceInfoOrganization{Name: "Example Org", Url: "https://example.org"},
			ContactUrl:       "mailto:admin@example.org",
			DocumentationUrl: "https://example.org/docs",
			Environment:      "prod",
			CreatedAt:        "2026-01-01T00:00:00Z",
			Storage:          []string{"s3://example-bucket"},
		}}},
	}

	info := getServiceInfo(t, service)

	assertConfigurableFields(t, info, &tes.ServiceInfo{
		Name:             "Example TES",
		Id:               "org.example.tes",
		Description:      "TES for example.org",
		Organization:     map[string]string{"name": "Example Org", "url": "https://example.org"},
		ContactUrl:       "mailto:admin@example.org",
		DocumentationUrl: "https://example.org/docs",
		Environment:      "prod",
		CreatedAt:        "2026-01-01T00:00:00Z",
		Storage:          []string{"s3://example-bucket"},
	})
	if info.Version != "v-test" {
		t.Errorf("version = %q, want v-test", info.Version)
	}
	if info.Type == nil || info.Type.Group != "org.ga4gh" || info.Type.Artifact != "tes" || info.Type.Version != "1.1.0" {
		t.Errorf("type = %+v, want org.ga4gh/tes/1.1.0", info.Type)
	}
}

func TestServiceInfoKeepsBuiltinValuesForUnsetFields(t *testing.T) {
	service := &Service{
		Name:   "Funnel",
		Config: &config.Config{Server: &config.Server{ServiceInfo: &config.ServiceInfo{Environment: "prod"}}},
	}

	want := proto.Clone(builtinServiceInfo).(*tes.ServiceInfo)
	want.Environment = "prod"
	assertConfigurableFields(t, getServiceInfo(t, service), want)
}

func TestServiceInfoDoesNotShareConfiguredOrBuiltinValues(t *testing.T) {
	storage := []string{"s3://example-bucket"}
	configured := &Service{
		Name:   "Funnel",
		Config: &config.Config{Server: &config.Server{ServiceInfo: &config.ServiceInfo{Storage: storage}}},
	}
	getServiceInfo(t, configured).Storage[0] = "changed"
	if storage[0] != "s3://example-bucket" {
		t.Fatalf("configured storage changed to %q", storage[0])
	}

	builtin := &Service{Name: "Funnel"}
	first := getServiceInfo(t, builtin)
	first.Storage[0] = "changed"
	first.Organization["name"] = "changed"
	assertConfigurableFields(t, getServiceInfo(t, builtin), builtinServiceInfo)
}

func TestValidateServiceInfoAcceptsValidValues(t *testing.T) {
	for name, info := range map[string]*config.ServiceInfo{
		"nil":   nil,
		"empty": {},
		"complete": {
			Id:               "org.example.tes",
			Description:      "TES for example.org",
			Organization:     &config.ServiceInfoOrganization{Name: "Example Org", Url: "https://example.org"},
			ContactUrl:       "mailto:admin@example.org",
			DocumentationUrl: "https://example.org/docs",
			Environment:      "prod",
			CreatedAt:        "2026-01-01T00:00:00+02:00",
			Storage:          []string{"s3://example-bucket", "file:///data"},
		},
		"https contact":       {ContactUrl: "https://example.org/contact"},
		"http documentation":  {DocumentationUrl: "http://docs.example.org:8080/tes?lang=en#config"},
		"percent-encoded url": {DocumentationUrl: "https://example.org/my%20docs"},
		"ipv6 host":           {Organization: &config.ServiceInfoOrganization{Name: "Example Org", Url: "https://[2001:db8::1]/"}},
		"fractional seconds":  {CreatedAt: "2026-01-01T00:00:00.5Z"},
		"lower-case t and z":  {CreatedAt: "2026-01-01t00:00:00z"},
		"empty organization":  {Organization: &config.ServiceInfoOrganization{}},
		"free environment":    {Environment: "staging-eu"},
		"non-reverse-dns id":  {Id: "example-tes-1"},
		"non-uri storage tag": {Storage: []string{"local scratch"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateServiceInfo("Funnel", info); err != nil {
				t.Fatalf("ValidateServiceInfo() = %v, want nil", err)
			}
		})
	}
}

func TestValidateServiceInfoAcceptsEmptyServiceName(t *testing.T) {
	if err := ValidateServiceInfo("", nil); err != nil {
		t.Fatalf("ValidateServiceInfo() = %v, want nil", err)
	}
}

func TestValidateServiceInfoRejectsInvalidValues(t *testing.T) {
	for name, tc := range map[string]struct {
		serviceName string
		info        *config.ServiceInfo
		field       string
	}{
		"blank service name":        {serviceName: "  ", field: "Server.ServiceName"},
		"blank id":                  {info: &config.ServiceInfo{Id: " "}, field: "Server.ServiceInfo.Id"},
		"blank description":         {info: &config.ServiceInfo{Description: "\t"}, field: "Server.ServiceInfo.Description"},
		"blank environment":         {info: &config.ServiceInfo{Environment: " "}, field: "Server.ServiceInfo.Environment"},
		"organization without url":  {info: &config.ServiceInfo{Organization: &config.ServiceInfoOrganization{Name: "Example Org"}}, field: "Server.ServiceInfo.Organization.Url"},
		"organization without name": {info: &config.ServiceInfo{Organization: &config.ServiceInfoOrganization{Url: "https://example.org"}}, field: "Server.ServiceInfo.Organization.Name"},
		"blank organization name":   {info: &config.ServiceInfo{Organization: &config.ServiceInfoOrganization{Name: " ", Url: "https://example.org"}}, field: "Server.ServiceInfo.Organization.Name"},
		"relative organization url": {info: &config.ServiceInfo{Organization: &config.ServiceInfoOrganization{Name: "Example Org", Url: "example.org"}}, field: "Server.ServiceInfo.Organization.Url"},
		"http url without host":     {info: &config.ServiceInfo{DocumentationUrl: "https:example.org"}, field: "Server.ServiceInfo.DocumentationUrl"},
		"relative documentation":    {info: &config.ServiceInfo{DocumentationUrl: "/docs"}, field: "Server.ServiceInfo.DocumentationUrl"},
		"url with spaces":           {info: &config.ServiceInfo{DocumentationUrl: "https://example.org/my docs"}, field: "Server.ServiceInfo.DocumentationUrl"},
		"bare email contact":        {info: &config.ServiceInfo{ContactUrl: "admin@example.org"}, field: "Server.ServiceInfo.ContactUrl"},
		"empty mailto":              {info: &config.ServiceInfo{ContactUrl: "mailto:"}, field: "Server.ServiceInfo.ContactUrl"},
		"date without time":         {info: &config.ServiceInfo{CreatedAt: "2026-01-01"}, field: "Server.ServiceInfo.CreatedAt"},
		"time without zone":         {info: &config.ServiceInfo{CreatedAt: "2026-01-01T00:00:00"}, field: "Server.ServiceInfo.CreatedAt"},
		"blank storage entry":       {info: &config.ServiceInfo{Storage: []string{"s3://example-bucket", ""}}, field: "Server.ServiceInfo.Storage[1]"},
		"whitespace storage entry":  {info: &config.ServiceInfo{Storage: []string{" "}}, field: "Server.ServiceInfo.Storage[0]"},
		"javascript documentation":  {info: &config.ServiceInfo{DocumentationUrl: "javascript:alert(1)"}, field: "Server.ServiceInfo.DocumentationUrl"},
		"data organization url":     {info: &config.ServiceInfo{Organization: &config.ServiceInfoOrganization{Name: "Example Org", Url: "data:text/html,hi"}}, field: "Server.ServiceInfo.Organization.Url"},
		"mailto documentation":      {info: &config.ServiceInfo{DocumentationUrl: "mailto:admin@example.org"}, field: "Server.ServiceInfo.DocumentationUrl"},
		"ftp contact":               {info: &config.ServiceInfo{ContactUrl: "ftp://example.org/contact"}, field: "Server.ServiceInfo.ContactUrl"},
		"empty host with port":      {info: &config.ServiceInfo{ContactUrl: "https://:443/contact"}, field: "Server.ServiceInfo.ContactUrl"},
		"non-ascii host":            {info: &config.ServiceInfo{DocumentationUrl: "https://exämple.org"}, field: "Server.ServiceInfo.DocumentationUrl"},
		"brace in path":             {info: &config.ServiceInfo{DocumentationUrl: "https://example.org/{x}"}, field: "Server.ServiceInfo.DocumentationUrl"},
	} {
		t.Run(name, func(t *testing.T) {
			serviceName := tc.serviceName
			if serviceName == "" {
				serviceName = "Funnel"
			}
			err := ValidateServiceInfo(serviceName, tc.info)
			if err == nil {
				t.Fatal("ValidateServiceInfo() = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("ValidateServiceInfo() = %q, want it to name %s", err, tc.field)
			}
		})
	}
}

func TestValidateServiceInfoReportsAllInvalidFields(t *testing.T) {
	err := ValidateServiceInfo(" ", &config.ServiceInfo{
		Id:           " ",
		CreatedAt:    "yesterday",
		Organization: &config.ServiceInfoOrganization{Name: " ", Url: "example.org"},
		Storage:      []string{""},
	})
	if err == nil {
		t.Fatal("ValidateServiceInfo() = nil, want an error")
	}
	for _, field := range []string{
		"Server.ServiceName",
		"Server.ServiceInfo.Id",
		"Server.ServiceInfo.CreatedAt",
		"Server.ServiceInfo.Organization.Name",
		"Server.ServiceInfo.Organization.Url",
		"Server.ServiceInfo.Storage[0]",
	} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ValidateServiceInfo() = %q, want it to name %s", err, field)
		}
	}
}
