Backend service for a social network.

This repository contains the **Post Service**, which handles post-related business logic.

### Environment Configuration

For local development, configuration can be loaded from a `.env` file.

**Do not use `.env` files in production.**  
In production, use **environment variables only**.

### HTTP API

The server listens on `HTTP_ADDRESS` and uses the configured `HTTP_*_TIMEOUT`
values. SIGINT/SIGTERM stop accepting requests and allow up to 10 seconds for
active requests to finish before database connections are closed.

All routes require `Authorization: Bearer <JWT>`. The existing verifier validates
RS256 signatures against the key fetched from `JWKS_URL`, checks `kid` and token
claims, and obtains the user UUID from `uid`. The HTTP layer never takes an author
ID from the request body.

| Method | Path | Operation | Success |
| --- | --- | --- | --- |
| GET | `/feed` | Get the authenticated user's feed | 200, array |
| POST | `/posts` | Create a root post | 201, post |
| PATCH | `/posts/{postID}` | Edit your post's text | 204 |
| DELETE | `/posts/{postID}` | Soft-delete your post | 204 |
| GET | `/posts/{postID}/replies` | Read direct replies | 200, array |
| POST | `/posts/{postID}/replies` | Reply to an active post | 201, post |
| PUT / DELETE | `/posts/{postID}/like` | Add / remove your like | 204 |
| PUT / DELETE | `/posts/{postID}/dislike` | Add / remove your dislike | 204 |

Create, reply and edit requests require `Content-Type: application/json` and a
body such as `{"text":"Hello"}`. Only `text` is accepted; it must be nonblank and
at most 10,000 Unicode code points. Request bodies are limited to 64 KiB. The
server generates IDs, authorship, timestamps and the reply's `parent_id`/`root_id`.
Root posts have null `parent_id` and `root_id`; nested replies retain the original
root post's ID.

Replies support zero-based `page` (default 0) and `page_size` (default 20, maximum
100), e.g. `/posts/{postID}/replies?page=1&page_size=20`. Feed selection follows
the existing usecase; it has no HTTP pagination parameters. HEAD `/feed` is
rejected because getting the feed consumes entries from its cached queue.

Post responses use snake_case fields: `id`, `author_id`, `text`, `root_id`,
`parent_id`, `reply_count`, `like_count`, `dislike_count`, `created_at`,
`updated_at`. Empty collections are `[]`.

Application errors have the form `{"error":"message"}`. Invalid input returns
400, invalid/missing credentials 401, missing/deleted posts 404, oversized bodies
413, and unsupported content types 415. Owner-only mutations also return 404
for another user's post. Unexpected storage errors return 500; their details
are recorded in server logs, not sent to clients. Unmatched routes and unsupported
methods use standard `net/http` 404/405 responses.

Run `go test -race ./...` to check authentication, route contracts, Redis behavior
and graceful server shutdown. Tests use a temporary Redis emulator and do not
require external PostgreSQL, Redis or the authentication service.
