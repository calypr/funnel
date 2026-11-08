# Google Datastore Usage

When Funnel is configured to use the Google Datastore as its database, some
additional configuration steps need to be taken.

## Datastore Access

Authentication to the Google Datastore needs to be configured through Google
Cloud CLI as described here:
https://cloud.google.com/datastore/docs/reference/libraries?hl=en#authentication

## Datastore Indexes

For retrieving a list of tasks, Funnel needs [composite
indexes](https://cloud.google.com/datastore/docs/concepts/indexes?hl=en) to be
defined in the Datastore using the Google Cloud CLI and the
[index.yaml](./index.yaml) file:

```shell
gcloud datastore indexes create path/to/index.yaml --database='funnel'
```

Note that it will take a bit of time before the indexes are ready for accepting
requests. You can see the status of those indexes through the Google Cloud
console: https://console.cloud.google.com/datastore/databases/ (**Indexes**
view under the target database).
