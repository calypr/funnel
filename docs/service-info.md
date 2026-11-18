# Service info

`GET /service-info` (and the gRPC `GetServiceInfo` method) describes the Funnel
deployment following the [GA4GH service-info specification][service-info]
(v1.0.0) and the TES `tesServiceInfo` extension. Service registries and clients
use it to tell deployments apart and to show who runs them.

> [!IMPORTANT]
> Service info is public. Funnel serves it without authentication, even when
> `Server.BasicAuth` or `Server.OidcAuth` is configured. Everything you set here,
> including the storage locations you advertise, is visible to anyone who can
> reach the server. Don't put credentials, internal host names, or bucket names
> you want to keep private into these fields.

## Configuration

`Server.ServiceName` (also available as the `--Server.ServiceName` flag) sets
`name`. The optional `Server.ServiceInfo` section of the configuration file
sets the identity of the deployment; it has no command-line flags. The HTTP and
gRPC endpoints report the same values.

```yaml
Server:
  ServiceName: Example TES
  ServiceInfo:
    Id: org.example.tes
    Description: Task execution service of Example Org
    Organization:
      Name: Example Org
      Url: https://example.org
    ContactUrl: mailto:admin@example.org
    DocumentationUrl: https://example.org/docs/tes
    Environment: prod
    CreatedAt: 2026-01-01T00:00:00Z
    Storage:
      - s3://example-bucket
```

| Key | Response field | Built-in value |
| --- | --- | --- |
| `Server.ServiceName` | `name` | `Funnel` |
| `Id` | `id` | `org.ga4gh.funnel` |
| `Description` | `description` | `Funnel is a toolkit for distributed task execution via a simple, standard API.` |
| `Organization.Name`, `Organization.Url` | `organization.name`, `organization.url` | `CALYPR Data Platform`, `https://github.com/calypr/` |
| `ContactUrl` | `contactUrl` | `https://github.com/calypr/` |
| `DocumentationUrl` | `documentationUrl` | `https://github.com/calypr/funnel` |
| `Environment` | `environment` | `development` |
| `CreatedAt` | `createdAt` | `2016-03-21T16:27:49-07:00` |
| `Storage` | `storage` | `file:///path/to/local/funnel-storage`, `s3://ohsu-compbio-funnel/storage` |

Funnel always reports `type` (`org.ga4gh:tes:1.1.0`), `version` (the Funnel
version), `updatedAt` (the time of the request), and
`tesResources_backend_parameters` (from the compute backend) itself; they can't
be configured.

The built-in values describe the Funnel project, not your deployment, and the
built-in storage locations are only examples. Every deployment that registers
with a service registry should at least set its own `Id`, `Organization`, and
`Environment`. The specification asks for an `id` that is unique for each
service; reverse domain name notation (`org.example.tes`) is recommended.

## Empty values

Every key is optional:

- A key that is missing, or set to an empty string, keeps its built-in value.
  This also applies to `Server.ServiceName`. Existing configurations without a
  `ServiceInfo` section report the same service info as before, except that an
  empty `ServiceName` now reports `Funnel` instead of an empty `name`, which
  the specification requires.
- `Storage` is replaced as a whole: a non-empty list replaces both built-in
  locations, while a missing or empty list (`Storage: []`) keeps them. Funnel
  can't report an empty `storage` list. To avoid advertising the example
  locations, list the locations you want to advertise.
- `Organization` is replaced as a whole as well. Either leave out both `Name`
  and `Url` to keep the built-in organization, or set both: the specification
  requires both, so setting only one of them is an error.

## Validation

`funnel server run` checks the configured values against the service-info
specification at startup and refuses to start if any of them is invalid,
naming every invalid key:

- `ServiceName`, `Id`, `Description`, `Environment`, and `Organization.Name`
  must not consist only of whitespace.
- `Organization.Url`, `ContactUrl`, and `DocumentationUrl` must be absolute
  `http` or `https` URLs with a host, such as `https://example.org`, written
  with the characters [RFC 3986][rfc3986] allows (percent-encode others, and
  use the ASCII form of internationalized domain names). `ContactUrl` may also
  be an email address as a `mailto:` URI (`mailto:admin@example.org`), as the
  specification suggests. Clients show these values as links, so other schemes,
  such as `javascript:` or `data:`, are rejected.
- `CreatedAt` must be an [RFC 3339][rfc3339] timestamp with a time zone, such
  as `2026-01-01T00:00:00Z`. Leap seconds (`:60`) aren't accepted.
- `Storage` entries must not be empty or consist only of whitespace. The TES
  specification allows any string; URIs such as `s3://example-bucket` are the
  convention.

`Environment` is free text. The specification suggests `prod`, `test`, `dev`,
and `staging`, but doesn't enforce them.

[service-info]: https://github.com/ga4gh-discovery/ga4gh-service-info/blob/v1.0.0/service-info.yaml
[rfc3986]: https://www.rfc-editor.org/rfc/rfc3986
[rfc3339]: https://www.rfc-editor.org/rfc/rfc3339
