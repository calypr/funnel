---
title: Remote Funnel server
menu:
  main:
    parent: Integrations
---

# Connect to a remote Funnel server

By default, the Funnel CLI connects to `http://localhost:8000`. Use `-S` to
select another server for a command:

```sh
funnel -S https://funnel.example.com task list
```

To use the same server for multiple commands, set `FUNNEL_SERVER`:

```sh
export FUNNEL_SERVER=https://funnel.example.com
funnel task list
```

The `-S` flag takes precedence when both are set.
