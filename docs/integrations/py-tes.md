# py-tes

> ⚠️ py-tes support is in active development and may be subject to change.

[py-tes](https://github.com/ohsu-comp-bio/py-tes) is a library for interacting with servers implementing the [GA4GH Task Execution Schema](https://github.com/ga4gh/task-execution-schemas).

## Getting Started

### Install

Available on [PyPI](https://pypi.org/project/py-tes/).

```sh
pip install py-tes
```

### Example Python Script

```py
import tes

task = tes.Task(
    executors=[
        tes.Executor(
            image="alpine",
            command=["echo", "hello"]
        )
    ]
)

cli = tes.HTTPClient("http://funnel.example.com", timeout=5)
task_id = cli.create_task(task)
res = cli.get_task(task_id)
cli.cancel_task(task_id)
```

## Additional Resources

- [py-tes Homepage](https://github.com/ohsu-comp-bio/py-tes)

- [py-tes Documentation](https://ohsu-comp-bio.github.io/py-tes/)

- [py-tes on PyPi](https://pypi.org/project/py-tes/)
