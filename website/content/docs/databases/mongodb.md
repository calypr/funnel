---
title: MongoDB
menu:
  main:
    parent: Databases
---

# MongoDB

Funnel supports storing tasks and scheduler data in MongoDB.

Config:
```yaml
Database: mongodb

MongoDB:
  # Addresses for the seed servers.
  Addrs:
    - "localhost"
  # Database name used within MongoDB to store funnel data.
  Database: "funnel"
  Username: ""
  Password: ""
```

When Funnel initializes the MongoDB backend, it ensures a unique `id` index
with simple collation on the existing `nodes` collection. If legacy node
documents contain duplicate IDs, initialization fails and reports that the
duplicates must be resolved before startup. The index migration does not
delete node documents or remove existing indexes.
