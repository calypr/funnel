---
title: Datastore
menu:
  main:
    parent: Databases
---

# Google Cloud Datastore

Funnel supports storing tasks (but not scheduler data) in Google Cloud Datastore.

This implementation currently doesn't work with Appengine, since Appengine places
special requirements on the context of requests and requires a separate library.

Two entity types are used, "Task" and "TaskPart" (for larger pieces of task content,
such as stdout/err logs).

Funnel will, by default, try to automatically load credentials from the
environment. Alternatively, you may explicitly set the credentials in the config.
You can read more about providing the credentials
[here](https://cloud.google.com/docs/authentication/application-default-credentials).

Config:
```yaml
Database: datastore

Datastore:
  Project: ""
  # Path to account credentials file.
  # Optional. If possible, credentials will be automatically discovered
  # from the environment.
  CredentialsFile: ""
```

Please also import some [composite
indexes](https://cloud.google.com/datastore/docs/concepts/indexes?hl=en)
to support the task-list queries.
This is typically done through command-line by referencing an **index.yaml**
file (do not change the filename) with the following content:

```sh
gcloud datastore indexes create path/to/index.yaml --database='funnel'
```

```yaml
indexes:

- kind: Task
  properties:
  - name: Owner
  - name: State
  - name: TagStrings
  - name: CreationTime
    direction: desc

- kind: Task
  properties:
  - name: Owner
  - name: State
  - name: CreationTime
    direction: desc

- kind: Task
  properties:
  - name: Owner
  - name: TagStrings
  - name: CreationTime
    direction: desc

- kind: Task
  properties:
  - name: Owner
  - name: CreationTime
    direction: desc

- kind: Task
  properties:
  - name: State
  - name: TagStrings
  - name: CreationTime
    direction: desc

- kind: Task
  properties:
  - name: State
  - name: CreationTime
    direction: desc

- kind: Task
  properties:
  - name: TagStrings
  - name: CreationTime
    direction: desc
```