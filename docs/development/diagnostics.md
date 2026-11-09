# Troubleshooting information

Run `funnel debug` to print the Funnel version and configuration as JSON.
Pass the configuration file and any server flags used by your deployment:

```sh
funnel debug --config /path/to/config.yaml
```

Commands that load Funnel configuration also accept `FUNNEL_CONFIG` as the
default configuration file path and `FUNNEL_LOG_LEVEL` as a log level override:

```sh
export FUNNEL_CONFIG=/path/to/config.yaml
export FUNNEL_LOG_LEVEL=debug
funnel debug
```

A nonempty `--config` overrides `FUNNEL_CONFIG`. Log level precedence, from
lowest to highest, is the built-in default, configuration file,
`FUNNEL_LOG_LEVEL`, and `--Logger.Level`. Empty environment values are ignored.
An unreadable configuration file selected through `FUNNEL_CONFIG` returns an error.
These variables apply to CLI configuration loading; the public Go schema and
default configuration functions do not read them.

The command reads the local file, environment overrides, and flags. It does not query a running server,
so use the same configuration and flags as the process you are investigating.
The report redacts known credentials, URL user information, paths and query strings,
environment and metadata values, commands, and templates. It retains other
configuration settings, such as endpoints and paths. Arbitrary values can still
contain sensitive information, so review the report and any logs before
sharing them. The report does not include logs or stack traces.
