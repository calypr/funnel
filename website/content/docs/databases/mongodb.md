---
title: MongoDB
menu:
  main:
    parent: Databases
---

# MongoDB

Funnel supports storing tasks and scheduler data in MongoDB. A replica set or
sharded cluster is required. Standalone MongoDB servers are rejected at startup
because task assignment and resource release update multiple documents atomically.
A single-node replica set is sufficient for development; it provides no redundancy.

## Connect Funnel to a replica set

Use the replica-set name and member addresses from your deployment.

```yaml
Database: mongodb

MongoDB:
  # Replica-set seed addresses, or one MongoDB connection URI.
  Addrs:
    - "mongodb://mongo-1:27017,mongo-2:27017,mongo-3:27017/?replicaSet=rs0"
  # Database name used within MongoDB to store funnel data.
  Database: "funnel"
  Username: ""
  Password: ""
```

For production, use a replica set with redundant members and authentication.
Follow MongoDB's [replica-set deployment guide](https://www.mongodb.com/docs/manual/tutorial/deploy-replica-set/)
for member networking and authentication. Each advertised member hostname must
be reachable from Funnel. Keep `directConnection` unset so the client can discover
the current primary.

## Start a single-node replica set locally

This Docker example runs MongoDB without authentication and publishes its port
only on the host's loopback address. Run Funnel on the same host. The named volume
preserves MongoDB data and replica-set configuration across container restarts.

1. Start MongoDB with replica-set support:

   ```sh
   docker run -d --name funnel-mongo \
     -p 127.0.0.1:27017:27017 \
     -v funnel-mongo-data:/data/db \
     mongo:7 --replSet rs0 --bind_ip_all
   ```

2. Check that MongoDB accepts connections:

   ```sh
   docker exec funnel-mongo mongosh --quiet --eval 'db.runCommand({ping: 1})'
   ```

   Wait for `{ ok: 1 }` before continuing. If the connection fails during startup,
   repeat the command.

3. Initialize the replica set once for this data volume:

   ```sh
   docker exec funnel-mongo mongosh --quiet --eval \
     'rs.initiate({_id: "rs0", members: [{_id: 0, host: "localhost:27017"}]})'
   ```

   Check that the node has become primary:

   ```sh
   docker exec funnel-mongo mongosh --quiet --eval 'db.hello().isWritablePrimary'
   ```

   Wait for `true` before starting Funnel. Initialization can return before the
   primary election finishes. Repeat the check if it returns `false`.

4. Set Funnel's database configuration:

   ```yaml
   Database: mongodb
   MongoDB:
     Addrs:
       - "mongodb://localhost:27017/?replicaSet=rs0&directConnection=true"
     Database: funnel
   ```

   `directConnection=true` keeps the client on the published Docker port. This
   example advertises `localhost` inside the container and is intended for local
   development. If Funnel also runs in a container, use an address reachable
   from that container instead of its own `localhost`.

To restart this container, run `docker start funnel-mongo`. Do not repeat
`rs.initiate()` for an already initialized volume. This single-node setup supports
transactions but provides no redundancy.

## Upgrade an existing standalone server

Convert the existing server before starting the upgraded Funnel version:

1. Stop the older Funnel servers and back up MongoDB.
2. Stop MongoDB and add the following to its existing `mongod.conf`, preserving
   its data directory and other settings:

   ```yaml
   replication:
     replSetName: rs0
   ```

3. Configure replica-set member authentication if your server uses authentication.
   Follow MongoDB's [standalone conversion guide](https://www.mongodb.com/docs/manual/tutorial/convert-standalone-to-replica-set/)
   for keyfile or X.509 setup. Preserve the existing users and data.
4. Restart MongoDB and initialize the replica set through `mongosh` with
   `rs.initiate()`. Check `rs.conf()` and ensure the advertised member address is
   reachable from Funnel. Wait for `db.hello().isWritablePrimary` to return `true`.
5. Update Funnel's connection URI to include `replicaSet=rs0`, then start the
   upgraded Funnel server. Use your actual replica-set name if it differs.

Changing the connection URI alone does not enable transactions. The MongoDB
server must be configured and initialized as a replica set.

## Funnel's startup migration

When Funnel initializes the MongoDB backend, it ensures a unique `id` index
with simple collation on the existing `nodes` collection. If legacy node
documents contain duplicate IDs, initialization fails and reports that the
duplicates must be resolved before startup. The index migration does not
delete node documents or remove existing indexes.

Funnel also creates a unique task-ID index, a queue index, and an index of active
allocations. At the first upgraded startup it imports active node assignments,
snapshots their reserved resources, and stores heartbeat timestamps separately.
Conflicting task owners abort the migration without committing partial claims.
Stop older Funnel servers before upgrading, and resolve reported conflicts
before restarting submission. Back up the database before upgrading.

Assignment commits the task owner and node capacity together. Terminal state
updates release the original reservation in the same transaction, and repeated
completion events cannot release it twice. Ordinary heartbeats do not rewrite
resource reservations. The scheduler uses the same assignment protocol as
BoltDB and PostgreSQL.
