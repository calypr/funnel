---
title: Tasks
menu:
  main:
    identifier: tasks
    weight: -70
---

# Tasks

A task defines a unit of work:

- metadata
- input files to download
- a sequence of Docker containers + commands to run,
- output files to upload
- state
- logs

The example task below downloads a file named `hello.txt` from S3 and calls `cat hello.txt` using the [alpine][alpine] container. This task also writes the executor's stdout to a file, and uploads the stdout to s3.

```
{
  "name": "Hello world",
  "inputs": [{
    # URL to download file from.
    "url": "s3://funnel-bucket/hello.txt",
    # Path to download file to.
    "path": "/inputs/hello.txt"
  }],
  "outputs": [{
    # URL to upload file to.
    "url": "s3://funnel-bucket/output.txt",
    # Local path to upload file from.
    "path": "/outputs/stdout"
  }],
  "executors": [{
      # Container image name.
      "image": "alpine",
      # Command to run (argv).
      "command": ["cat", "/inputs/hello.txt"],
      # Capture the stdout of the command to /outputs/stdout
      "stdout": "/outputs/stdout"
  }]
}
```

Tasks have multiple "executors"; containers and commands run in a sequence. 
Funnel runs executors via Docker.

Tasks also have state and logs:
```
{
  "id": "b85khc2rl6qkqbhg8vig",
  "state": "COMPLETE",
  "name": "Hello world",
  "inputs": [
    {
      "url": "s3://funnel-bucket/hello.txt",
      "path": "/inputs/hello.txt"
    }
  ],
  "outputs": [
    {
      "url": "s3://funnel-bucket/output.txt",
      "path": "/outputs/stdout"
    }
  ],
  "executors": [
    {
      "image": "alpine",
      "command": [
        "cat",
        "/inputs/hello.txt"
      ],
      "stdout": "/outputs/stdout"
    }
  ],
  "logs": [
    {
      "logs": [
        {
          "startTime": "2017-11-14T11:49:05.127885125-08:00",
          "endTime": "2017-11-14T11:49:08.484461502-08:00",
          "stdout": "Hello, Funnel!\n"
        }
      ],
      "startTime": "2017-11-14T11:49:04.433593468-08:00",
      "endTime": "2017-11-14T11:49:08.487707039-08:00"
    }
  ],
  "creationTime": "2017-11-14T11:49:04.427163701-08:00"
}
```

There are logs for each task attempt and each executor. Notice that the stdout is
conveniently captured by `logs[0].logs[0].stdout`.

## Output types and patterns

An output can declare `"type": "FILE"` or `"type": "DIRECTORY"`. The Funnel worker
fails output collection if the declared type does not match the produced path. If
`type` is omitted, Funnel keeps its existing behavior and inspects the path to
determine whether to upload a file or the files within a directory.

An output path containing `*` must match at least one path. An unmatched pattern,
including `abc/*` when `abc` is a file, fails output collection. If the executor
succeeded, these collection errors put the task in `SYSTEM_ERROR`. An existing
empty directory still produces a warning and uploads no files. These checks apply
to backends that run the Funnel worker. GCP Batch uses a separate output path.

## Container paths and output mounts

Funnel's Docker and Kubernetes workers use filesystem mounts to make task files
available to executor containers. A file output normally mounts its parent
directory. A directory output mounts the directory itself. Choose a dedicated
task directory, such as `/task` or `/outputs`, that does not contain files the
container image needs or overlap mounts in your executor template.

Mounting over the container root `/` is unsupported. This includes a volume at
`/`, a directory output at `/`, and ordinary file outputs such as `/result.json`
whose parent is `/`. Root-level input files can also require a root mount when
Funnel groups their input directories. Funnel rejects unsupported mappings before
starting the executor. Move these paths beneath a dedicated directory and update
the task's commands and path declarations together. Funnel does not rewrite paths
inside commands or scripts.

For example, capture stdout at `/task/stdout` and publish that same path:

```json
{
  "executors": [{
    "image": "alpine",
    "command": ["echo", "hello"],
    "stdout": "/task/stdout"
  }],
  "outputs": [{
    "path": "/task/stdout",
    "url": "s3://results/stdout.txt",
    "type": "FILE"
  }]
}
```

Docker has a limited exception for root-level file outputs that exactly match an
executor's `stdout` or `stderr` path. Funnel mounts those individual files so later
executors can read them. These paths are taken from the task, not from fixed names
such as `/stdout`. A container can read and write these files, but cannot unlink
or replace their file mount points. Input and directory conflicts are rejected.

Kubernetes does not use this exception. Its shared storage can require the
executor to create stream files itself, so pre-creating a file for a Kubernetes
file mount is not safe for every supported storage driver. Use stream paths under
a dedicated directory for tasks that must run on both backends.

These are Funnel storage limitations, not TES requirements. TES does not require
parent-directory mounts. Rejecting `/` mounts also does not prevent a non-root
mount from hiding files already present in the container image.

### Task API

The API lets you create, get, list, and cancel tasks.

### Create
```
POST /v1/tasks
{
  "name": "Hello world",
  "inputs": [{
    "url": "s3://funnel-bucket/hello.txt",
    "path": "/inputs/hello.txt"
  }],
  "outputs": [{
    "url": "s3://funnel-bucket/output.txt",
    "path": "/outputs/stdout"
  }],
  "executors": [{
      "image": "alpine",
      "command": ["cat", "/inputs/hello.txt"],
      "stdout": "/outputs/stdout"
  }]
}


# The response is a task ID:
b85khc2rl6qkqbhg8vig
```

### Get
```
GET /v1/tasks/b85khc2rl6qkqbhg8vig

{"id": "b85khc2rl6qkqbhg8vig", "state": "COMPLETE"}
```

By default, the minimal task view is returned which describes only the ID and state.
In order to get the original task with some basic logs, use the "BASIC" task view:
```
GET /v1/tasks/b85khc2rl6qkqbhg8vig?view=BASIC
{
  "id": "b85khc2rl6qkqbhg8vig",
  "state": "COMPLETE",
  "name": "Hello world",
  "inputs": [
    {
      "url": "gs://funnel-bucket/hello.txt",
      "path": "/inputs/hello.txt"
    }
  ],
  "outputs": [
    {
      "url": "s3://funnel-bucket/output.txt",
      "path": "/outputs/stdout"
    }
  ],
  "executors": [
    {
      "image": "alpine",
      "command": [
        "cat",
        "/inputs/hello.txt"
      ],
      "stdout": "/outputs/stdout",
    }
  ],
  "logs": [
    {
      "logs": [
        {
          "startTime": "2017-11-14T11:49:05.127885125-08:00",
          "endTime": "2017-11-14T11:49:08.484461502-08:00",
        }
      ],
      "startTime": "2017-11-14T11:49:04.433593468-08:00",
      "endTime": "2017-11-14T11:49:08.487707039-08:00"
    }
  ],
  "creationTime": "2017-11-14T11:49:04.427163701-08:00"
}
```

The "BASIC" doesn't include some fields such as stdout/err logs, because these fields may be potentially large.
In order to get everything, use the "FULL" view:
```
GET /v1/tasks/b85khc2rl6qkqbhg8vig?view=FULL
{
  "id": "b85khc2rl6qkqbhg8vig",
  "state": "COMPLETE",
  "name": "Hello world",
  "inputs": [
    {
      "url": "gs://funnel-bucket/hello.txt",
      "path": "/inputs/hello.txt"
    }
  ],
  "executors": [
    {
      "image": "alpine",
      "command": [
        "cat",
        "/inputs/hello.txt"
      ],
      "stdout": "/outputs/stdout",
    }
  ],
  "logs": [
    {
      "logs": [
        {
          "startTime": "2017-11-14T11:49:05.127885125-08:00",
          "endTime": "2017-11-14T11:49:08.484461502-08:00",
          "stdout": "Hello, Funnel!\n"
        }
      ],
      "startTime": "2017-11-14T11:49:04.433593468-08:00",
      "endTime": "2017-11-14T11:49:08.487707039-08:00"
    }
  ],
  "creationTime": "2017-11-14T11:49:04.427163701-08:00"
}
```

### List
```
GET /v1/tasks
{
  "tasks": [
    {
      "id": "b85l8tirl6qkqbhg8vj0",
      "state": "COMPLETE"
    },
    {
      "id": "b85khc2rl6qkqbhg8vig",
      "state": "COMPLETE"
    },
    {
      "id": "b85kgt2rl6qkpuptua70",
      "state": "SYSTEM_ERROR"
    },
    {
      "id": "b857gnirl6qjfou61fh0",
      "state": "SYSTEM_ERROR"
    }
  ]
}
```

List has the same task views as Get: MINIMAL, BASIC, and FULL.

The task list is paginated:
```
GET /v1/tasks?page_token=1h123h12j2h3k
{
  "next_page_token": "1n3n1j23k12n3k123",
  "tasks": [
    {
      "id": "b85l8tirl6qkqbhg8vj0",
      "state": "COMPLETE"
    },
    # ... more tasks here ...
  ]
}
```

### Cancel 

Tasks cannot be modified by the user after creation, with one exception – they can be canceled.
```
POST /v1/tasks/b85l8tirl6qkqbhg8vj0:cancel
```


### Docker resource limits

By default, Funnel does not enforce task CPU and RAM requests on Docker
containers. To apply them as limits, set `Worker.Container.EnforceResources`
to `true`. Each specified CPU or RAM request is applied independently. A RAM
limit also disables container swap. A process that exceeds its RAM limit may
be killed by the kernel. Existing custom `Worker.Container.RunCommand`
templates must include the resource flags themselves.

### Full task spec

Here's a more detailed description of a task.  
For a full, in-depth spec, read the TES standard's [task_execution.proto](https://github.com/ga4gh/task-execution-schemas/blob/master/task_execution.proto).

```
{
    # The task's ID. Set by the server.
    # Output only.
    "id": "1234567",

    # The task's state. Possible states:
    #   QUEUED
    #   INITILIZING
    #   RUNNING
    #   PAUSED
    #   COMPLETE
    #   EXECUTOR_ERROR
    #   SYSTEM_ERROR
    #   CANCELED
    #
    # Output only.
    "state": "QUEUED",

    # Metadata
    "name":        "Task name.",
    "description": "Task description.",
    "tags": {
      "custom-tag-1": "tag-value-1",
      "custom-tag-2": "tag-value-2",
    },

    # Resource requests
    "resources": {
      # Number of CPU cores requested.
      "cpuCores": 1,

      # RAM request, in gigabytes.
      "ramGb":    1.0,

      # Disk space request, in gigabytes.
      "diskGb":   100.0,

      # Request preemptible machines,
      # e.g. preemptible VM in Google Cloud, an instance from the AWS Spot Market, etc.
      "preemptible": false,

       # Request that the task run in these compute zones.
       "zones": ["zone1", "zone2"],
    },

    # Input files will be downloaded by the worker.
    # This example uses s3, but Funnel supports multiple filesystems.
    "inputs": [
      {
        "name": "Input file.",
        "description": "Input file description.",

        # URL to download file from.
        "url":  "s3://my-bucket/object/path/file.txt",
        # Path to download file to.
        "path": "/container/input.txt"
      },
      {
        "name": "Input directory.",
        "description": "Directories are also supported.",
        "url":  "s3://my-bucket/my-data/",
        "path": "/inputs/my-data/",
        "type": "DIRECTORY"
      },

      # A task may include the file content directly in the task message.
      # This is sometimes useful for small files such as scripts,
      # which you want to include without talking directly to the filesystem.
      {
        "path": "/inputs/script.py",
        "content": "import socket; print socket.gethostname()"
      }
    ],

    # Output files will be uploaded to storage by the worker.
    "outputs": [
      {
        "name": "Output file.",
        "description": "Output file description.",
        "url":  "s3://my-bucket/output-data/results.txt",
        "path": "/outputs/results.txt"
      },
      {
        "name": "Output directory.",
        "description": "Directories are also supported.",
        "url":  "s3://my-bucket/output-data/output-dir/",
        "path": "/outputs/data-dir/",
        "type": "DIRECTORY"
      }
    ],

    # Executors define a sequence of containers + commands to run.
    # Execution stop on the first non-zero exit code.
    "executors": [
      {
        # Container image name.
        # Funnel supports running executor containers via Docker.
        "image": "ubuntu",

        # Command arguments (argv).
        # The first item is the executable to run.
        "command": ["my-tool-1", "/container/input"],

        # Local file path to read stdin from.
        "stdin": "/inputs/stdin.txt",

        # Local file path to write stdout to.
        "stdout": "/container/output",

        # Local file path to write stderr to.
        "stderr": "/container/stderr",

        # Set the working directory before executing the command.
        "workdir": "/data/workdir",

        # Environment variables
        "env": {
          "ENV1": "value1",
          "ENV2": "value2",
        }
      },

      # Second executor runs after the first completes, on the same machine.
      {
        "image": "ubuntu",
        "command": ["cat", "/container/input"],
        "stdout": "/container/output",
        "stderr": "/container/stderr",
        "workdir": "/tmp"
      }
    ]

    # Date/time the task was created.
    # Set the the server.
    # Output only.
    "creationTime": "2017-11-14T11:49:04.427163701-08:00"

    # Task logs.
    # Output only.
    #
    # If there's a system error, the task may be attempted multiple times,
    # so this field is a list of attempts. In most cases, there will be only
    # one or zero entries here.
    "logs": [

      # Attempt start/end times, in RFC3339 format.
      "startTime": "2017-11-14T11:49:04.433593468-08:00",
      "endTime": "2017-11-14T11:49:08.487707039-08:00"

      # Arbitrary metadata set by Funnel.
      "metadata": {
        "hostname": "worker-1",
      },

      # Arbitrary system logs which Funnel thinks are useful to the user.
      "systemLogs": [
        "task was assigned to worker 1",
        "docker command: docker run -v /vol:/data alpine cmd arg1 arg2",
      ],

      # Log of files uploaded to storage by the worker,
      # including all files in directories, with file sizes.
      "outputs": [
        {
          "url": "s3://my-bucket/output-data/results.txt",
          "path": "/outputs/results.txt",
          "sizeBytes": 123
        },
        {
          "url": "s3://my-bucket/output-data/output-dir/file1.txt",
          "path": "/outputs/data-dir/file1.txt",
          "sizeBytes": 123
        },
        {
          "url": "s3://my-bucket/output-data/output-dir/file2.txt",
          "path": "/outputs/data-dir/file2.txt",
          "sizeBytes": 123
        }
        {
          "url": "s3://my-bucket/output-data/output-dir/subdir/file3.txt",
          "path": "/outputs/data-dir/subdir/file3.txt",
          "sizeBytes": 123
        }
      ],

      # Executor logs. One entry per executor.
      "logs": [
        {
          # Executor start/end time, in RFC3339 format.
          "startTime": "2017-11-14T11:49:05.127885125-08:00",
          "endTime": "2017-11-14T11:49:08.484461502-08:00",

          # Executor stdout/err. Only available in the FULL task view.
          #
          # There is a size limit for these fields, which is configurable
          # and defaults to 10KB. If more than 10KB is generated, only the
          # tail will be logged. If the full output is needed, the task
          # may use Executor.stdout and an output to upload the full content
          # to storage.
          "stdout": "Hello, Funnel!",
          "stderr": "",

          # Exit code
          "exit_code": 0,
        },
        {
          "startTime": "2017-11-14T11:49:05.127885125-08:00",
          "endTime": "2017-11-14T11:49:08.484461502-08:00",
          "stdout": "Hello, Funnel!\n"
        }
      ],
    }
  ],
}
```

[alpine]: https://hub.docker.com/_/alpine/
