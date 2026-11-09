# OAuth2

By default, a Funnel server allows open access to its API endpoints, but in
addition to Basic authentication it can also be configured to require a valid
JWT in the request.

Funnel itself does not redirect users to perform the login.
It just validates that the presented token is issued by a trusted service
(specified in the YAML configuration file) and the token has not expired.
In addition, if the OIDC provides a token introspection endpoint (in its
configuration JSON), Funnel server also calls that endpoint to make sure the
token is still active (i.e., no token invalidation before expiring).

Funnel requires an expiration claim and an explicitly configured API audience.
It can also require a specific scope. Set the audience expected by your API;
this may differ from the OAuth client ID.

To enable JWT authentication, specify `OidcAuth` section in your config file:

```yaml
Server:
  OidcAuth:
    # URL of the OIDC service configuration:
    ServiceConfigURL: "https://my.oidc.service/.well-known/openid-configuration"

    # Client ID and secret are sent with the token introspection request
    # (Basic authentication):
    ClientId: your-client-id
    ClientSecret: your-client-secret

    # Optional: if specified, this scope value must be in the token:
    RequireScope: funnel-id

    # Required: this API audience value must be in the token:
    RequireAudience: tes-api

    # The URL where OIDC should redirect after login (keep the path '/login')
    RedirectURL: "http://localhost:8000/login"

    # List of OIDC subjects promoted to Admin status.
    Admins:
      - user.one@example.org
      - user.two@example.org

  TaskAccess: OwnerOrAdmin
```

The `TaskAccess` property configures the visibility and access-mode for tasks:

* `All` (default) - all tasks are visible to everyone
* `Owner` - tasks are visible to the users who created them
* `OwnerOrAdmin` - extends `Owner` by allowing Admin-users (defined under
  `Admins`) access everything

As new tasks are created, the username behind the request is recorded as the
owner of the task. Depending on the `TaskAccess` property, if owner-based
acces-mode is enabled, the owner of the task is compared to username of current
request to decide if the user may see and interact with the task.

Make sure to properly protect the configuration file so that it's not readable
by everyone:

```bash
$ chmod 600 funnel.config.yml
```

Note that the Funnel UI supports login through an OIDC service. However, OIDC
authentication is not supported at command-line.
