<div align="center">

# Post Service

**Posts, conversations and reactions for a social network.**

A Go HTTP API backed by PostgreSQL and Redis, with JWT authentication and explicit application layers.

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-storage-4169E1?style=flat-square&logo=postgresql&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-reactions_%26_feed-DC382D?style=flat-square&logo=redis&logoColor=white)
![Status](https://img.shields.io/badge/status-in_development-E5A00D?style=flat-square)
[![License: MIT](https://img.shields.io/badge/license-MIT-64748B?style=flat-square)](LICENSE)

[Features](#features) · [Architecture](#architecture) · [Getting started](#getting-started) · [API](#http-api) · [Development](#development)

</div>

---

> [!NOTE]
> The first version is under development. The HTTP API is implemented; database migrations, feed ranking and reaction persistence still need work. See [Current boundaries](#current-boundaries).

## Features

- **Posts** — create, edit and soft-delete posts, with owner checks on mutations.
- **Threaded replies** — reply to posts or other replies while preserving the original thread root.
- **Reactions** — add or remove likes and dislikes; a Lua script atomically switches between them.
- **Feed** — Redis-backed feed queues and seen-post tracking, with candidates selected from PostgreSQL.
- **Authentication** — Bearer JWT verification using an RSA public key fetched from an external JWKS endpoint.
- **HTTP validation** — UUID checks, bounded JSON bodies, text validation and reply pagination.
- **Application lifecycle** — startup connection checks, configurable timeouts, structured logs and graceful shutdown.

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
cmd/api/
└── main.go                 # Entry point
internal/
├── app/                    # Application wiring and lifecycle
├── config/                 # Environment configuration
├── domain/                 # Models and errors
├── logger/                 # Structured JSON logging
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
- A reachable PostgreSQL database with a compatible `posts` table.
- A reachable Redis instance.
- An external authentication service exposing an RSA JWKS endpoint and issuing signed access tokens.

> [!IMPORTANT]
> SQL migrations and automatic schema creation are not included yet. Provision the database schema before using the API. Expected columns and queries are defined in [Repo.go](internal/adapters/postgresql/Repo.go).

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

### 4. Run

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

```sh
go test -race ./...
go vet ./...
```

Tests cover HTTP routing, signed JWT authentication, validation, reply creation,
Redis cache misses, concurrent reaction switching and server shutdown.
Redis tests use **miniredis**, an in-memory emulator with Lua support. The test suite
does not require external PostgreSQL, Redis or authentication services, but some
tests open temporary loopback ports.

## Current boundaries

- **Schema setup is manual.** Database migrations and container-based setup are not included.
- **Redis is required for reactions.** Reaction membership is not persisted or synchronized to PostgreSQL by this service; clearing Redis loses those reactions.
- **Reply ordering uses PostgreSQL counters.** Displayed like counts come from Redis, so current ordering can differ from the live reaction counts.
- **Feed behavior is still evolving.** Cached reads consume up to 20 IDs; regeneration can return up to 100 candidates. The API has no feed pagination parameters, and ranking is unfinished. `HEAD /feed` is rejected to avoid consuming entries.
- **JWT keys are loaded at startup.** The current fetcher selects the first JWKS key; automatic key refresh and rotation are not implemented.

## License

Distributed under the [MIT License](LICENSE).
