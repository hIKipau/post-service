<div align="center">

# Post Service

**Posts, conversations and reactions for a social network.**

A Go HTTP API backed by PostgreSQL and Redis, with JWT authentication and explicit application layers.

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-storage-4169E1?style=flat-square&logo=postgresql&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-reactions_%26_feed-DC382D?style=flat-square&logo=redis&logoColor=white)
![Status](https://img.shields.io/badge/status-in_development-E5A00D?style=flat-square)
[![License: MIT](https://img.shields.io/badge/license-MIT-64748B?style=flat-square)](LICENSE)

[Features](#features) · [Architecture](#architecture) · [Database](docs/database.md) · [Getting started](#getting-started) · [API](#http-api) · [Development](#development)

</div>

---

> [!NOTE]
> The first version is under development. The HTTP API, database migrations and basic feed ranking are implemented; durable reaction storage and personalized recommendations remain future work. See [Current boundaries](#current-boundaries).

## Features

- **Posts** — create, edit and soft-delete posts, with owner checks on mutations.
- **Threaded replies** — reply to posts or other replies while preserving the original thread root.
- **Reactions** — add or remove likes and dislikes; a Lua script atomically switches between them.
- **Feed** — ranked PostgreSQL candidates, Redis queues, seen-post tracking and consistent pages of up to 20 posts.
- **Authentication** — Bearer JWT verification using an RSA public key fetched from an external JWKS endpoint.
- **HTTP validation** — UUID checks, bounded JSON bodies, text validation and reply pagination.
- **Application lifecycle** — startup connection checks, configurable timeouts, structured logs and graceful shutdown.
- **Database migrations** — versioned PostgreSQL SQL, embedded in a standalone Goose migrator with migration locking.

## Architecture

```mermaid
flowchart LR
    Client["Client"] --> Auth["JWT middleware"]
    Auth --> Router["HTTP router"]
    Router --> Handler["Handlers"]
    Handler --> Usecase["Usecases"]
    Usecase --> PG[("PostgreSQL")]
    Usecase --> Redis[("Redis")]
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

**Data ownership:** PostgreSQL stores posts, thread relationships and reply counts. Redis holds reaction membership, feed queues and seen-post sets. Reply counts are updated in PostgreSQL transactions; displayed like/dislike counts are read from Redis.

<details>
<summary><strong>Project layout</strong></summary>

```text
cmd/
├── api/                    # HTTP entry point
└── migrate/                # Standalone Goose migration command
migrations/                 # Embedded SQL: posts table, constraints and indexes
docs/database.md            # Schema, relationships and migration workflow
internal/
├── app/                    # Application wiring and lifecycle
├── config/                 # Environment configuration
├── domain/                 # Models and errors
├── logger/                 # Structured JSON logging
├── migrator/               # Goose provider and migration operations
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

### 1. Prepare dependencies

You will need:

- Go **1.26 or newer**.
- A reachable PostgreSQL database using UTF-8 encoding.
- A reachable Redis instance.
- An external authentication service exposing an RSA JWKS endpoint and issuing signed access tokens.

The database itself must already exist. The included [Goose migrations](docs/database.md)
create the `posts` table, constraints and indexes before you start the API.

### 2. Get the code

```sh
git clone https://github.com/hIKipau/post-service.git
cd post-service
go mod download
```

### 3. Configure the service

For local development, create a `.env` file in the repository root:

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

### 4. Apply migrations

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

### 5. Run

```sh
go run ./cmd/api
```

The application checks PostgreSQL and Redis connectivity and fetches the JWT public key before starting HTTP. Failed initialization aborts startup.

On `SIGINT` or `SIGTERM`, the server stops accepting requests, waits for active requests within the shutdown deadline, then releases its storage connections. Connections are forcibly closed if the deadline expires.

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
Redis tests use **miniredis**, an in-memory emulator with Lua support. The test suite
does not require external PostgreSQL, Redis or authentication services, but some
tests open temporary loopback ports. PostgreSQL migration/repository integration
tests are opt-in with `TEST_DATABASE_URL` and run in a temporary isolated schema;
see [database testing](docs/database.md#integration-tests).

## Current boundaries

- **Infrastructure is external.** Provision PostgreSQL/Redis yourself and run the included migrations before starting the API; container-based setup is not included.
- **Redis is required for reactions.** Reaction membership is not persisted or synchronized to PostgreSQL by this service; clearing Redis loses those reactions.
- **Reply ordering uses PostgreSQL counters.** Displayed like counts come from Redis, so current ordering can differ from the live reaction counts.
- **Feed pages are bounded, not guaranteed full.** Responses contain at most 20 posts and may be shorter or empty when the bounded scan finds no eligible candidates or queued posts were deleted. A later request continues the saved scan; an empty page does not necessarily mean the database has no older posts. Ranking is a basic freshness/engagement heuristic, not personalization. `HEAD /feed` is rejected to avoid consuming entries.
- **JWT keys are loaded at startup.** The current fetcher selects the first JWKS key; automatic key refresh and rotation are not implemented.

## License

Distributed under the [MIT License](LICENSE).
