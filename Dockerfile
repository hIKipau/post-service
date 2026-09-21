# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# Download dependencies separately so source edits can reuse this layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ \
    ./cmd/api ./cmd/migrate ./cmd/reactions

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/

# Configuration is injected at runtime; no .env or source code enters this image.
USER 10001:10001
ENV HTTP_ADDRESS=0.0.0.0:8080
EXPOSE 8080
STOPSIGNAL SIGTERM
CMD ["api"]
