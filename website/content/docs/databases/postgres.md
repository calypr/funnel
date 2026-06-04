---
title: Postgres
menu:
  main:
    parent: Databases
---

# Postgres

> [!WARNING]
>
> Postgres support is in early development.
>
> Production deployments are recommended to use the alternative storage backends available, such as MongoDB, ElasticSearch, DynamoDB, etc.

## Configuration

```yaml
Database: postgres

Postgres:
  Host: localhost
  Database: funnel
  User: funnel
  Password: example
```

## Default Values

```go
		Postgres: &Postgres{
			Host:     "localhost:5432",
			Database: "funnel",
			User:     "funnel",
			Password: "example",
			Timeout: &TimeoutConfig{
				TimeoutOption: &TimeoutConfig_Duration{
					Duration: durationpb.New(time.Second * 30),
				},
			},
		},
```
