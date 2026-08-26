---
title: Basic Auth
menu:
  main:
    parent: Security
    weight: 10
---
# Basic Auth

By default, a Funnel server allows open access to its API endpoints, but it
can be configured to require basic password authentication. To enable this,
include users and passwords in your config file:

```yaml
Server:
  BasicAuth:
    - User: admin
      Password: someReallyComplexSecret
      Admin: true
    - User: funnel
      Password: abc123

  TaskAccess: OwnerOrAdmin
```

The `TaskAccess` property configures the visibility and access-mode for tasks:

* `All` (default) - all tasks are visible to everyone
* `Owner` - tasks are visible to the users who created them
* `OwnerOrAdmin` - extends `Owner` by allowing Admin-users (`Admin: true`)
  access everything

As new tasks are created, the username behind the request is recorded as the
owner of the task. Depending on the `TaskAccess` property, if owner-based
acces-mode is enabled, the owner of the task is compared to username of current
request to decide if the user may see and interact with the task.

If you are using BoltDB or Badger, the Funnel worker communicates to the server via gRPC
so you will also need to configure the RPC client.

```yaml
RPCClient:
  Credential:
    User: funnel
    Password: abc123
```

Make sure to properly protect the configuration file so that it's not readable
by everyone:

```bash
$ chmod 600 funnel.config.yml
```

To use the password, set the `FUNNEL_SERVER_USER` and `FUNNEL_SERVER_PASSWORD` environment variables:
```bash
$ export FUNNEL_SERVER_USER=funnel
$ export FUNNEL_SERVER_PASSWORD=abc123
$ funnel task list
```
