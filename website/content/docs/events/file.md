---
title: Event files
menu:
  main:
    parent: Events
---

# Event files

Use the `file` event writer to save structured task events as newline-delimited JSON (NDJSON). Each
line follows Funnel's `events.Event` schema and can be decoded with
`events.Unmarshal`. Nested system-log fields remain nested. The `log` writer
continues to produce diagnostic output, which is not an event archive.

```yaml
EventWriters:
  - log
  - file
EventFile: /var/log/funnel/events.ndjson
```

The parent directory must exist and be writable by Funnel. New files are created
with permissions `0600`. Existing files are appended to, with their permissions
unchanged. The file is closed when its owning event pipeline closes. Close failures are logged. The
`--EventFile` flag can also supply the path.

Give each process its own output file. In particular, distributed workers must
use distinct paths or separate local filesystems. Writes are serialized within
one writer, but Funnel does not coordinate multiple processes writing the same
file. Files are not automatically rotated.

File errors are returned through the existing event-writer error handling.
This output is not a durable message queue: writes are not individually synced
to disk, and a crash or failed write can leave an incomplete final line. Event
selection and log-level filtering in the configured pipeline still apply.

Schema-compatible output does not by itself provide safe database replay.
Importing events requires handling ordering, duplicates, and partial failures.
