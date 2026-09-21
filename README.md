<div align="center">

# Post Service

**Posts, conversations and reactions for a social network.**

A Go HTTP API backed by PostgreSQL and Redis, with JWT authentication and explicit application layers.

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-storage-4169E1?style=flat-square&logo=postgresql&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-reactions_%26_feed-DC382D?style=flat-square&logo=redis&logoColor=white)
![Status](https://img.shields.io/badge/status-in_development-E5A00D?style=flat-square)
[![License: MIT](https://img.shields.io/badge/license-MIT-64748B?style=flat-square)](LICENSE)

[Features](#features) · [Architecture](#architecture) · [Database](docs/database.md) · [Getting started](#getting-started) · [Deployment](#deployment) · [API](#http-api) · [Development](#development)

</div>

---

> [!NOTE]
> Under development. Implemented: HTTP API, PostgreSQL migrations, basic feed ranking, asynchronous reaction persistence, and a local Docker Compose stack. Interest-based personalized recommendations remain future work. Authentication is provided by an external service. See [Current boundaries](#current-boundaries).

## Features

- **Posts** — create, edit and soft-delete posts, with owner checks on mutations.
- **Threaded replies** — reply to posts or other replies while preserving the original thread root.
- **Reactions** — Lua-based switching with a Redis event log and an idempotent, ordered PostgreSQL synchronization worker.
- **Feed** — ranked PostgreSQL candidates, Redis queues, seen-post tracking and consistent pages of up to 20 posts.
- **Authentication** — Bearer JWT verification using an RSA public key fetched from an external JWKS endpoint.
- **HTTP validation** — UUID checks, bounded JSON bodies, text validation and reply pagination.
- **Application lifecycle** — startup connection checks, configurable timeouts, structured logs and graceful shutdown.
- **Database migrations** — versioned PostgreSQL SQL, embedded in a standalone Goose migrator with migration locking.
- **Local containers** — multi-stage Docker image and Compose services for API, migrations, PostgreSQL and persistent Redis.

## Architecture

```mermaid
flowchart LR
    Client["Client"] --> Auth["JWT middleware"]
    Auth --> Router["HTTP router"]
    Router --> Handler["Handlers"]
    Handler --> Usecase["Usecases"]
    Usecase --> PG[("PostgreSQL")]
    Usecase --> Redis[("Redis")]
    Redis -. "reaction events" .-> Sync["Reaction sync worker"]
    Sync --> PG
    JWKS["External JWKS endpoint"] -. "public key at startup" .-> Auth
```

| Layer | Responsibility |
| --- | --- |
| `transport/http` | Routes, authentication middleware, request validation and JSON responses |
| `usecase` | Coordinates post, feed, reply and reaction operations |
| `domain` | Post models and shared application errors |
| `adapters` | PostgreSQL queries and Redis operations |
| `security/jwt` | JWKS fetching and RS256 token verification |
| `app` | Component initialization, HTTP startup and shutdown |
| `reactionsync` | Ordered event processing, retries and post-commit acknowledgements |

**Data ownership:** PostgreSQL stores posts, thread relationships, reply counts and asynchronously synchronized per-user reactions. Redis holds live reaction membership, its event log, feed queues and seen-post sets. SQL reaction counters are updated in the same transaction as membership; displayed counts are read from Redis. See [reaction synchronization and recovery](docs/reactions.md).

<details>
<summary><strong>Project layout</strong></summary>

```text
cmd/
├── api/                    # HTTP entry point
├── migrate/                # Standalone Goose migration command
└── reactions/              # Explicit offline import / restore
migrations/                 # Embedded SQL: posts, reactions, receipts and indexes
docs/database.md            # Schema, relationships and migration workflow
docs/reactions.md           # Reaction persistence, offline import and recovery
Dockerfile                  # Non-root image: API, migrator and reaction maintenance
compose.yaml                # Local API + migrations + PostgreSQL + Redis stack
internal/
├── app/                    # Application wiring and lifecycle
├── config/                 # Environment configuration
├── domain/                 # Models and errors
├── logger/                 # Structured JSON logging
├── migrator/               # Goose provider and migration operations
├── reactionsync/           # Ordered background reaction projection
├── security/jwt/           # JWKS client and token verifier
├── adapters/
│   ├── postgresql/         # Post repository
│   └── redis/              # Reactions, feed and seen-post storage
├── usecase/
│   ├── usecase.go          # Dependencies and interfaces
│   ├── post.go
│   ├── feed.go
│   ├── replies.go
│   └── reaction.go
└── transport/http/
    ├── router.go
    ├── middleware/         # Bearer authentication
    └── handler/
        ├── handler.go      # Constructor and service interface
        ├── post.go
        ├── feed.go
        ├── replies.go
        ├── reaction.go
        ├── request.go      # Input parsing and validation
        └── response.go     # DTOs and HTTP responses
```

</details>

## Getting started

Clone the repository and run the commands below from its root:

```sh
git clone https://github.com/hIKipau/post-service.git
cd post-service
```

Choose Docker Compose for a containerized local stack, or run Go directly with
reachable storage services. Both options require a working authentication service.

### Docker Compose (local development)

Install and start Docker with the Compose plugin. Go, PostgreSQL and Redis do
not need to be installed on the host. The stack builds the API and maintenance
commands, creates a PostgreSQL 17 database, starts Redis 7.4, and applies Goose
migrations before starting the API.

| Compose service | Responsibility |
| --- | --- |
| `postgres` | Creates the `post_service` database on first initialization; stores data in `postgres-data` |
| `redis` | Stores reactions, the event log and feed state in `redis-data` |
| `migrate` | Runs `migrate up` against the same database as the API, then exits |
| `api` | Starts after storage health checks and successful migrations; also runs the reaction worker |

These are separate containers on the same Docker network, not nested containers.
An exited migration container with exit code `0` is expected. A failed migration
blocks API startup; inspect `docker compose logs migrate` before retrying.

An **external authentication service is still required**. Set `DOCKER_JWKS_URL`
in `.env` to its reachable RSA JWKS endpoint. The default points to port 8081 on
the host through `host.docker.internal`; inside containers, `127.0.0.1` points
to the container itself. On Linux, the host auth service must listen on an
interface reachable from Docker, not only host loopback. This repository does
not implement a token issuer or silently disable authentication.

For a fresh local database:

```sh
docker compose up --build -d
docker compose ps -a
docker compose logs -f api migrate
```

The API is available at `http://127.0.0.1:8080`. PostgreSQL and Redis are exposed
only on host loopback at ports 5432 and 6379. Override `API_PORT`,
`POSTGRES_PORT` or `REDIS_PORT` in `.env` if these ports are already occupied.
`POSTGRES_PASSWORD` defaults to the **development-only** `local-password`;
use URL-safe characters (letters, digits, `-`, `_`) because it is also used in
the connection URL. Changing it after database initialization does not change
the password stored in PostgreSQL. Do not commit real credentials to `.env`,
which is currently tracked by Git.

Compose supplies internal storage addresses and `HTTP_ADDRESS=0.0.0.0:8080`;
the host-oriented `DATABASE_URL`, `REDIS_URL`, `JWKS_URL` and `HTTP_ADDRESS` in
`.env` remain for `go run`. Other application timeouts are shared. Increase
`DOCKER_STOP_GRACE_PERIOD` if increasing `HTTP_SHUTDOWN_TIMEOUT` so Docker leaves
time for HTTP draining and reaction-worker shutdown.

<details>
<summary><strong>Compose-only configuration</strong></summary>

These variables configure the included `compose.yaml`, not the Go application:

| Variable | Default | Purpose |
| --- | --- | --- |
| `POSTGRES_PASSWORD` | `local-password` | Development PostgreSQL password; also used in the internal database URL |
| `POSTGRES_PORT` | `5432` | PostgreSQL port on host loopback |
| `REDIS_PORT` | `6379` | Redis port on host loopback |
| `API_PORT` | `8080` | API port on host loopback |
| `DOCKER_JWKS_URL` | `http://host.docker.internal:8081/.well-known/jwks.json` | JWKS endpoint reachable from the API container |
| `DOCKER_STOP_GRACE_PERIOD` | `30s` | Docker shutdown deadline before forced termination |

If running Go on the host with Compose storage, keep `DATABASE_URL` and `REDIS_URL`
consistent with any changed host ports or password.

</details>

PostgreSQL and Redis use named volumes. Redis enables AOF with `appendfsync
everysec` and `noeviction`; this reduces, but does not eliminate, the risk of
losing recent unsynchronized reactions. Volumes are not backups. See
[reaction durability and recovery](docs/reactions.md) and the
[Redis persistence documentation](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).

```sh
# Stop containers without deleting their persisted data.
docker compose down

# Alternatively, run only storage in Docker and use Go on the host.
docker compose up -d postgres redis
go run ./cmd/migrate up
go run ./cmd/api

# Inspect migration status using the application image.
docker compose run --rm migrate migrate status
```

Never use `docker compose down -v` unless you intend to delete **all local
database and Redis data**. This stack creates its own volumes; it does not
automatically import existing databases. Before attaching existing data,
review the [database migration procedure](docs/database.md#existing-database)
and [offline reaction import/restore](docs/reactions.md). For maintenance, stop
**all** API instances first, back up both stores, and use
`docker compose run --rm --no-deps api reactions -offline import` (or `restore`
after Redis data loss) with the storage containers running.

The [Dockerfile](Dockerfile) uses a multi-stage build, runs as a non-root user,
and includes `api`, `migrate` and `reactions`; `.env` and private keys are excluded
from the build context. Compose waits for storage health checks and successful
migration completion using [dependency conditions](https://docs.docker.com/compose/how-tos/startup-order/).
This is a local development setup, not a production deployment with TLS,
managed secrets, backups or high availability.

### Run without Docker

#### 1. Prepare dependencies

You will need:

- Go **1.26 or newer**.
- A reachable PostgreSQL database using UTF-8 encoding.
- A reachable Redis instance.
- An external authentication service exposing an RSA JWKS endpoint and issuing signed access tokens.

The database itself must already exist. The included [Goose migrations](docs/database.md)
create `posts`, `post_reactions`, `reaction_sync_events`, constraints and indexes
before you start the API.

#### 2. Download Go dependencies

```sh
go mod download
```

#### 3. Configure the service

For local development, adjust the example `.env` in the repository root:

```dotenv
ENV=local
HTTP_ADDRESS=127.0.0.1:8080

DATABASE_URL=postgres://postgres:local-password@127.0.0.1:5432/post_service?sslmode=disable
REDIS_URL=redis://127.0.0.1:6379/0
JWKS_URL=http://127.0.0.1:8081/.well-known/jwks.json
```

These are example local addresses; replace them with your actual endpoints. Existing environment variables take precedence over `.env`. In production, inject environment variables directly and keep credentials out of version control.

<details>
<summary><strong>All configuration options</strong></summary>

| Variable | Default | Purpose |
| --- | --- | --- |
| `ENV` | Required | `local` / `dev` enable debug logs; other values use info level |
| `DATABASE_URL` | Required | PostgreSQL connection string |
| `REDIS_URL` | Required | Redis connection URL |
| `JWKS_URL` | Required | External public-key endpoint |
| `HTTP_ADDRESS` | Required | HTTP listen address |
| `HTTP_READ_TIMEOUT` | `5s` | Maximum duration for reading a request |
| `HTTP_WRITE_TIMEOUT` | `10s` | Response write timeout |
| `HTTP_IDLE_TIMEOUT` | `60s` | Idle keep-alive timeout |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Request-header read timeout |
| `STARTUP_TIMEOUT` | `10s` | Combined initialization deadline for PostgreSQL, Redis and JWKS |
| `HTTP_SHUTDOWN_TIMEOUT` | `10s` | Time allowed for active requests to finish during shutdown |

Durations use Go notation, such as `500ms`, `10s` or `1m`. Startup and shutdown timeouts must be positive. A missing `.env` is allowed; an unreadable or malformed file is an error.

</details>

#### 4. Apply migrations

```sh
go run ./cmd/migrate status
go run ./cmd/migrate up
```

The migrator only requires `DATABASE_URL` and also reads the optional `.env`.
It uses **Goose v3.28.0** with embedded SQL and runs independently of the API.
Repeated `up` skips applied migrations. Use `version` to inspect the current version
or `-timeout 10m up` to change the default five-minute deadline.

> [!WARNING]
> The initial migration expects a fresh schema. If `posts` already exists, review
> the [existing-database procedure](docs/database.md#existing-database) first.
> `go run ./cmd/migrate down` rolls back one migration; rolling back the initial
> migration permanently removes all posts and replies.

#### 5. Run

If upgrading a deployment with existing Redis reactions, first stop all API
instances, back up both stores and run `go run ./cmd/reactions -offline import`.
Do **not** use import after Redis data loss; use the [restore procedure](docs/reactions.md#restore-redis-after-data-loss).

```sh
go run ./cmd/api
```

The application checks PostgreSQL/Redis connectivity, the reaction schema and maintenance barrier, initializes the reaction stream and fetches the JWT public key before starting HTTP. Failed initialization aborts startup. A single elected background worker synchronizes reactions across all API instances.

On `SIGINT` or `SIGTERM`, the server stops accepting requests, waits for active HTTP requests within the shutdown deadline, stops the reaction worker, then releases storage connections. HTTP connections are forcibly closed if the drain deadline expires; remaining reaction events are processed after restart.

## Deployment

### Separate Compose projects

The included `compose.yaml` manages **Post Service only**, with its own PostgreSQL
and Redis. Compose groups resources by project name, which defaults to the directory
containing the Compose file; `-p`, `COMPOSE_PROJECT_NAME` or a top-level `name` can
override it. Use distinct, stable names for separate deployments. See
[Compose project naming](https://docs.docker.com/compose/how-tos/project-name/).

From this repository root, for a deployment consistently named `posts`:

```sh
docker compose -p posts up --build -d
docker compose -p posts stop api   # Stop only the API; storage stays running.
docker compose -p posts stop       # Stop this project's containers, not all Docker containers.
docker compose -p posts down       # Remove this project's containers/network; retain named volumes.
```

Keep the same project name for later commands. Switching it creates a different
deployment with differently named volumes; it does not migrate existing data.
Distinct projects also need non-conflicting published host ports. By default,
their networks are separate; sharing a server does not automatically connect them.
Cross-project communication requires deliberate network configuration, such as a
shared external network. See [Compose networking](https://docs.docker.com/compose/how-tos/networking/).

Stopping shared infrastructure affects every client using it, even clients in
another project. The [`stop` command](https://docs.docker.com/reference/cli/docker/compose/stop/)
does not remove containers or persisted data.

### One Compose project for multiple microservices

A shared deployment repository can define all services in one `compose.yaml`.
This is a **deployment option, not an included full-system configuration**:
this repository contains neither the auth service nor its database setup.
Containers still run alongside each other; Compose does not run one Compose stack
inside another. The shared file defines the services and their images/build contexts.

One possible mapping with a shared PostgreSQL instance is:

| Component | Database | Database role |
| --- | --- | --- |
| Post API and its migrator | `post_service` | `posts_user` |
| Auth API and its migrator | `auth_service` | `auth_user` |

Provision each database and a non-superuser role with access restricted to that
service's database; each service owns its tables and migrations. Separate names
alone do not enforce permissions. Do not reuse the local Compose `postgres`
superuser across services. A shared PostgreSQL instance saves resources, but its
outage or maintenance affects both databases; separate PostgreSQL containers and
volumes are another option when independent lifecycles are needed.

When assembling the shared deployment:

1. Create databases and roles before running each service's migrator. PostgreSQL
   image initialization scripts in `/docker-entrypoint-initdb.d` run only with an
   empty data directory, not on every restart. Existing volumes need explicit
   provisioning. See the [official PostgreSQL image](https://hub.docker.com/_/postgres).
2. Pass each API and its migrator matching `DATABASE_URL` values. For example, the
   Post Service URL points to host `postgres`, database `post_service` and role
   `posts_user`. Store actual credentials outside the repository.
3. Give Post Service a dedicated Redis database paired with that PostgreSQL database;
   do not let another deployment share its reaction stream and consumer group.
4. Set the API's `JWKS_URL` to the auth service's internal endpoint, for example
   `http://auth-service:8080/.well-known/jwks.json` **if that is its actual endpoint**.
   Wait for auth readiness as well as storage and migrations before starting the API.
5. Use container service names and internal ports for communication. A custom shared
   Compose should pass application variables directly; `DOCKER_JWKS_URL` is only a
   convenience variable interpreted by this repository's local Compose file.

From that deployment directory, `docker compose up -d` starts the configured stack;
`docker compose stop` stops the project's services. To stop only the posts API, use
its service key, for example `docker compose stop post-service` if named that way.
In this repository's local file, that key is `api`.

Before production use, add TLS/reverse-proxy configuration, restricted network
access, secret management, tested backups and monitoring. The local Compose file
does not provide these, automatic reaction-log retention, or high availability.

## HTTP API

### Authentication

Every API route requires:

```http
Authorization: Bearer <access-token>
```

Tokens must use **RS256**, carry a `kid` matching the fetched key and contain a nonzero UUID in the `uid` claim. The service derives authorship from that claim; clients cannot supply an author ID in the request body.

The service verifies tokens but does not issue them. Obtain an access token from your authentication service.

### Routes

| Method | Endpoint | Action | Success |
| --- | --- | --- | --- |
| `GET` | `/feed` | Read the authenticated user's feed | `200` · post array |
| `POST` | `/posts` | Create a root post | `201` · post |
| `PATCH` | `/posts/{postID}` | Edit your post's text | `204` |
| `DELETE` | `/posts/{postID}` | Soft-delete your post | `204` |
| `GET` | `/posts/{postID}/replies` | Read direct replies | `200` · post array |
| `POST` | `/posts/{postID}/replies` | Create a reply | `201` · post |
| `PUT` | `/posts/{postID}/like` | Like a post, replacing your dislike | `204` |
| `DELETE` | `/posts/{postID}/like` | Remove your like | `204` |
| `PUT` | `/posts/{postID}/dislike` | Dislike a post, replacing your like | `204` |
| `DELETE` | `/posts/{postID}/dislike` | Remove your dislike | `204` |

### Request rules

- Create, edit and reply bodies accept only `{"text":"..."}` with `Content-Type: application/json`.
- Text must be nonblank and contain at most **10,000 Unicode code points**.
- JSON request bodies are limited to **64 KiB**.
- IDs, authorship, timestamps and thread relationships are assigned by the server.
- Reply pagination uses `page=0` and `page_size=20` by default; page size must be **1–100**.
- Replies are direct children of the requested post. Fetch each reply's endpoint to navigate deeper.
- Empty collections are `[]`; `204` responses have no body.

### Try it

Set your API address and a real access token:

```sh
API_URL='http://127.0.0.1:8080'
ACCESS_TOKEN='replace-with-your-access-token'
```

**Create a post**

```sh
curl -i -X POST "$API_URL/posts" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"text":"Hello, world!"}'
```

Example `201 Created` response:

```json
{
  "id": "c625b291-a066-4c13-81e1-fd4e5bed66ad",
  "author_id": "d2e85773-4705-413b-b7bc-c2aa4e3b3dd1",
  "text": "Hello, world!",
  "root_id": null,
  "parent_id": null,
  "reply_count": 0,
  "like_count": 0,
  "dislike_count": 0,
  "created_at": "2026-09-21T12:00:00Z",
  "updated_at": "2026-09-21T12:00:00Z"
}
```

Copy the actual `id` from your response:

```sh
POST_ID='replace-with-the-created-post-id'

# Reply to the post
curl -i -X POST "$API_URL/posts/$POST_ID/replies" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"text":"A first reply."}'

# Read its first page of replies
curl -i "$API_URL/posts/$POST_ID/replies?page=0&page_size=20" \
  -H "Authorization: Bearer $ACCESS_TOKEN"

# Like the post
curl -i -X PUT "$API_URL/posts/$POST_ID/like" \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

<details>
<summary><strong>More examples: feed, editing and deletion</strong></summary>

```sh
# Read the feed
curl -i "$API_URL/feed" \
  -H "Authorization: Bearer $ACCESS_TOKEN"

# Edit your post
curl -i -X PATCH "$API_URL/posts/$POST_ID" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"text":"An updated post."}'

# Remove your like
curl -i -X DELETE "$API_URL/posts/$POST_ID/like" \
  -H "Authorization: Bearer $ACCESS_TOKEN"

# Soft-delete your post
curl -i -X DELETE "$API_URL/posts/$POST_ID" \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

</details>

### Errors

Application errors use a consistent JSON shape:

```json
{"error":"post not found"}
```

| Status | Meaning |
| --- | --- |
| `400` | Invalid UUID, JSON, text or pagination |
| `401` | Missing or invalid authentication |
| `404` | Post missing/deleted, or unavailable for an owner-only mutation |
| `405` | Unsupported HTTP method |
| `413` | Request body exceeds the size limit |
| `415` | Unsupported or missing JSON content type |
| `500` | Unexpected application or storage failure |

Internal error details stay in server logs. Unmatched routes and unsupported methods normally use the standard `net/http` responses rather than the application's JSON error format.

## Development

### Feed behavior

`internal/usecase/feed.go` contains both orchestration and the ranking helpers.
When fewer than 20 IDs remain, the service keeps that tail and appends ranked
unseen candidates, excluding IDs already queued. Each refill checks the newest
100 candidates first, then continues older batches using a saved `(created_at, id)`
cursor. Scanning is limited to three batches per attempt; progress is saved even
when a scan finds no new posts. Reaching the end resets the cursor.

```text
activity = max(0, likes + 0.5 * replies - 2 * dislikes)
score    = (1 + ln(1 + activity)) / (1 + ageHours / 24)
```

Likes/dislikes come from Redis, reply counts from PostgreSQL. These are starting
weights, not personalized recommendations. Equal scores use newest creation time
and descending UUID as stable tie breakers. Existing queued posts keep their order;
only newly appended candidates are ranked.

The service reads a queue snapshot, loads up to 20 active foreign posts, then uses
a Redis Lua script to commit the remaining IDs, scan cursor and only the prepared
page's seen IDs together. Deleted/missing posts are replaced from the remaining
queue when possible. A revision token rejects stale concurrent writes; the request
retries from a fresh snapshot up to three times. PostgreSQL/reaction read failures
do not consume the queue. A Redis commit error is returned, not a successful page.

`seen` means **prepared for an API response**, not confirmed delivery or reading:
a lost HTTP response or ambiguous Redis commit can still hide a page from the next
request. Exact delivery acknowledgement is not implemented. Queue/cursor/revision
TTL is 24 hours; seen history expires after three days **without new additions**,
not three days per individual post. The implementation uses a standalone Redis
client; the multi-key scripts are not configured for Redis Cluster.

### Tests

```sh
go test -race ./...
go vet ./...
```

Tests cover HTTP routing, signed JWT authentication, validation, reply creation,
Redis cache misses, concurrent reaction switching and server shutdown. Feed tests
cover ranking, partial pages, preserved queue tails, older-candidate cursors,
read failures, atomic commits and concurrent requests without duplicate pages.
Reaction tests also cover event publication, pending-first replay, duplicate delivery,
lost acknowledgements, worker cancellation and offline maintenance barriers.
Redis tests use **miniredis**, an in-memory emulator with Lua support. The test suite
does not require external PostgreSQL, Redis or authentication services, but some
tests open temporary loopback ports. PostgreSQL migration/repository integration
tests are opt-in with `TEST_DATABASE_URL` and run in a temporary isolated schema;
see [database testing](docs/database.md#integration-tests).

## Current boundaries

- **Local containers are included; full-system deployment is not.** The Compose stack starts Post Service, PostgreSQL, Redis and migrations. Authentication is external; a shared multi-service deployment must be assembled separately. Production TLS, secrets, backups, monitoring and high availability are not configured.
- **Reaction persistence is asynchronous.** PostgreSQL retains synchronized reactions, but losing Redis before projection can lose accepted events. Redis requires persistence, non-evicting storage and backlog/retention monitoring; see [operations and recovery](docs/reactions.md).
- **Reply ordering uses PostgreSQL counters.** Displayed counts come from Redis; SQL ordering can lag behind live reactions until synchronization catches up.
- **Feed pages are bounded, not guaranteed full.** Responses contain at most 20 posts and may be shorter or empty when the bounded scan finds no eligible candidates or queued posts were deleted. A later request continues the saved scan; an empty page does not necessarily mean the database has no older posts. Ranking is a basic freshness/engagement heuristic, not personalization. `HEAD /feed` is rejected to avoid consuming entries.
- **JWT keys are loaded at startup.** The current fetcher selects the first JWKS key; automatic key refresh and rotation are not implemented.

## License

Distributed under the [MIT License](LICENSE).
