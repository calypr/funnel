---
title: Badger
menu:
  main:
    parent: Databases
---

# Badger

[Badger](https://github.com/dgraph-io/badger) is an embedded database that stores
Funnel task data in a local directory. It does not require a separate database
server. Funnel uses BoltDB by default; select Badger in your configuration:

```yaml
Database: badger

# The selected database already stores task events. This avoids opening
# another database as an additional event writer.
EventWriters:
  - log

Badger:
  Path: ./funnel-work-dir/funnel.badger.db
```

Save this configuration to a file, then start Funnel with it:

```sh
funnel server run --config /path/to/config.yaml
```

`Badger.Path` is a directory, relative to the server's working directory when
given a relative path. Funnel creates it if it does not exist. Place it on
persistent storage if task data must survive server or container restarts. To
back it up, stop Funnel and copy the entire directory; restore it before
restarting the server.

Use one Funnel server process per Badger directory. Badger does not implement
the scheduler service required by Funnel's `manual` compute backend; use a
different database backend for that configuration.
