---
title: Troubleshooting information
menu:
  main:
    parent: Development
---

# Troubleshooting information

Run `funnel debug` to print the Funnel version and configuration as JSON.
Pass the configuration file and any server flags used by your deployment:

```sh
funnel debug --config /path/to/config.yaml
```

The command reads the local file and flags. It does not query a running server,
so use the same configuration and flags as the process you are investigating.
The report redacts known credentials, URL user information, paths and query strings,
environment and metadata values, commands, and templates. It retains other
configuration settings, such as endpoints and paths. Arbitrary values can still
contain sensitive information, so review the report and any logs before
sharing them. The report does not include logs or stack traces.
