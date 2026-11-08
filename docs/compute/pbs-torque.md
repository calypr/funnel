# PBS/Torque

Funnel can be configured to submit workers to [PBS/Torque][pbs] by making calls
to `qsub`.

The Funnel server needs to run on a submission node.
Configure Funnel to use PBS by including the following config:

It is recommended to update the submit file template so that the
`funnel worker run` command takes a config file as an argument 
(e.g. `funnel worker run --config /opt/funnel_config.yml --taskID {{.TaskId}}`)

Download the [PBS/Torque submit template](../../website/static/funnel-config-examples/pbs-template.txt)
to `/etc/funnel/pbs-template.txt`, then configure Funnel to read it:

```yaml
Compute: pbs
PBS:
  TemplateFile: /etc/funnel/pbs-template.txt
```
The following variables are available for use in the template:

| Variable    |  Description |
|:------------|:-------------|
|TaskId       | funnel task id |
|WorkDir      | funnel working directory |
|Cpus         | requested cpu cores |
|RamGb        | requested ram |
|DiskGb       | requested free disk space |
|Zone         | requested zone (could be used for queue name) |

See https://golang.org/pkg/text/template for information on creating templates.

[pbs]: http://www.adaptivecomputing.com/products/open-source/torque/
