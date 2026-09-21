# Reaction synchronization

The API changes reaction membership in Redis and appends the absolute new state
to `reactions:events`. A background worker projects that ordered log into PostgreSQL.
This is asynchronous: HTTP success acknowledges Redis, **not** a PostgreSQL commit.
Redis data loss before synchronization can still lose accepted reactions.

## Event and SQL storage

Each changed reaction produces `event_id` (UUID), `post_id`, `user_id` and `state`
(`1` = like, `-1` = dislike, `0` = no reaction). Redis supplies a stream ID for ordering.
An unchanged reaction produces no event. Removing a like never removes a dislike,
and vice versa. All four operations use the same Lua script. It validates key types,
records the event, then updates membership without command interleaving. Lua does
not provide rollback after a runtime error: under exceptional storage/memory errors,
stop writes, fix the Redis failure and use offline restore to reconcile from the log.

Migration `00003_add_reactions.sql` creates:

- `post_reactions`: primary key `(post_id, user_id)`, `kind` restricted to `-1/1`,
  and `updated_at`. No row means no reaction. `post_id` references `posts`; physical
  deletion cascades, while normal soft deletion preserves historical reactions.
- `reaction_sync_events`: `event_id` primary key, processing time and outcome
  (`applied` or `post_missing`). These receipts make replay idempotent.

For each event, one PostgreSQL transaction inserts the receipt, locks the post row,
reads its previous user reaction, writes the new membership and updates both post
counters by the difference. Duplicate receipts skip the entire change. A missing
physical post is recorded as `post_missing` and acknowledged; an existing soft-deleted
post still receives synchronization. Post text timestamps are not modified.

## Ordering and failure handling

Every API instance starts a worker. Only one can hold the database-wide PostgreSQL
session advisory lock. The lock owner uses a separate physical connection for **all**
projection transactions; it never reconnects while pretending to retain the lock.
Use a direct PostgreSQL connection or session-mode pooler, not transaction pooling.
Use one dedicated Redis database and one matching PostgreSQL database per deployment.

The consumer group is `postgres-sync`; its fixed consumer name is `ordered-writer`.
Do not add other consumers or change this identity. Starting the group at `0` includes
events written before the worker started. The worker reads its earliest pending
event before requesting a new event, in one non-blocking Lua operation. Even a stale
reader during connection-loss failover cannot reserve a later event ahead of an
unacknowledged one. Fixed identity plus exclusive leadership lets
a new leader resume an old leader's pending events without parallel claims.

`XACK` runs only after SQL commit. Failure before commit leaves the event pending;
failure after commit but before ACK repeats a deduplicated transaction. Each step has
a 10-second deadline. An idle worker waits 250 ms; storage/leadership failures retry
after one second. A malformed event or SQL constraint error stops forward progress
and logs the offending event/error rather than silently discarding data. Investigate
and repair it; manually acknowledging it can lose a reaction or break ordering.

During shutdown, the worker remains active while HTTP handlers drain, then cancels
and releases its connection before Redis/PostgreSQL close. Shutdown does not promise
to drain the entire backlog; pending events remain for the next worker.

## Deployment requirements

Redis is now an event log as well as a cache. Configure persistent storage, backups,
memory alerts and a policy that does not evict reaction membership or the stream.
For example, a starting Redis configuration is:

```conf
appendonly yes
appendfsync everysec
maxmemory-policy noeviction
```

Set a suitable `maxmemory` for your deployment. `everysec` allows roughly a second
of recent writes to be lost on a crash; `always` trades more disk synchronization
for stronger local durability. Neither substitutes for backups or guarantees recovery
from destruction of all Redis copies. PostgreSQL backups/durability settings also
matter. See the official [Redis persistence documentation](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).

The application does not alter server configuration. No automatic stream trimming
or receipt expiration is implemented: never use a blind `MAXLEN` policy that can
delete pending or unread events. Monitor `XLEN reactions:events`,
`XPENDING reactions:events postgres-sync` and `XINFO GROUPS reactions:events`.
Pending count alone is not total backlog; also inspect group lag and the age/ID of
the earliest unprocessed event. Alert on sustained lag, synchronization error logs,
and memory/disk growth. Retention must be designed before unbounded production load;
only safely acknowledged history can be pruned, with receipts retained for every
event that can still be replayed (including backups).

## First deployment / existing Redis reactions

Back up PostgreSQL and Redis and stop **all** API instances and other reaction writers.
Then apply migrations:

```sh
go run ./cmd/migrate up
```

For an existing Redis dataset, import the current membership **before** starting
the new API. This is also required if PostgreSQL has old manually populated reaction
counters. A completely fresh deployment with no reactions/counter snapshots needs
only migrations.

```sh
go run ./cmd/reactions -offline import
go run ./cmd/api
```

The command needs `DATABASE_URL` and `REDIS_URL`, also read from optional `.env`.
It takes the same writer lock, repairs SQL aggregates from existing durable rows,
drains any surviving events, then replaces SQL membership and counters per post with
the Redis snapshot. Both active and soft-deleted posts are included. Conflicting
like/dislike membership aborts instead of guessing. Orphan Redis keys without a
PostgreSQL post are not imported. Repeating a completed import is idempotent while
writers remain stopped.

**Import treats Redis as authoritative and can overwrite durable SQL reactions.**
Never run it against an empty, evicted or partially lost Redis dataset to recover data.

## Restore Redis after data loss

Stop all API instances and other writers, preserve any surviving Redis data and
back up PostgreSQL. Do not restart the API against an empty/stale reaction cache.

```sh
go run ./cmd/reactions -offline -timeout 30m restore
go run ./cmd/api
```

Restore first projects surviving stream events into PostgreSQL. It then deletes
only validated `post:likes:<uuid>` / `post:dislikes:<uuid>` keys and rebuilds membership
from SQL, without publishing new events. The event stream, consumer group, feed queues
and unrelated Redis data are preserved. Lost events that never reached PostgreSQL
and cannot be recovered from Redis persistence/backups cannot be recreated.

Both commands require the explicit `-offline` acknowledgement. They install a
persistent `reactions:maintenance` barrier: new reaction writes fail and API startup
is blocked until the operation finishes. If interrupted, **keep the API stopped and
rerun the same command**. A failed restore may have cleared some Redis membership;
a failed import may have replaced only some SQL snapshots. Per-post SQL replacements
are transactional. Switching import/restore while the other is incomplete is refused.
The marker is intentionally not expired or removed on failure. Do not delete it to
bypass recovery. The barrier supplements, rather than replaces, stopping all writers;
older application versions and direct Redis commands do not honor it.

## Tests

`go test -race ./...` checks Redis publication/no-ops, pending-first replay, lost ACK,
worker cancellation, maintenance barriers and feed/reaction compatibility using
miniredis and controlled worker fakes. Real PostgreSQL tests are opt-in:

```sh
TEST_DATABASE_URL='postgres://postgres:local-password@127.0.0.1:5432/post_service_test?sslmode=disable' \
  go test -race ./internal/adapters/postgresql -run 'TestReaction.*Integration' -v
```

These tests use a temporary isolated schema to verify advisory-lock exclusion,
transactional receipts/counters, late duplicate events, constraint-error rollback,
legacy imports and Redis-to-PostgreSQL-to-Redis round trips.
