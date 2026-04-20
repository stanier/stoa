# Multi-stage Dockerfile for Stoa and MCP servers
# Each stage builds a specific binary, with final runtime images optimized for size

# =============================================================================
# Stage 0: Build the Svelte web frontend
# =============================================================================
FROM node:22-alpine AS web-builder
WORKDIR /web
COPY web/package.json ./
RUN npm install --legacy-peer-deps
COPY web/ ./
# Vite outputs to ../internal/webd/dist relative to web/ — redirect to /dist
RUN npx vite build --outDir /dist

# =============================================================================
# Stage 1: Build environment
# =============================================================================
FROM golang:1.26-alpine3.22 AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .
# Inject pre-built frontend assets so go:embed can pick them up.
COPY --from=web-builder /dist ./internal/webd/dist

# =============================================================================
# Stage 2: Build binaries (separate stages for parallel builds)
# =============================================================================
FROM builder AS build-chat
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/chat ./cmd/chat

FROM builder AS build-dump-context
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/dump-context ./cmd/dump-context

FROM builder AS build-mcp-filesystem
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-filesystem ./cmd/mcp-filesystem

FROM builder AS build-mcp-memory
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-memory ./cmd/mcp-memory

FROM builder AS build-mcp-time
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-time ./cmd/mcp-time

FROM builder AS build-mcp-fetch
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-fetch ./cmd/mcp-fetch

FROM builder AS build-mcp-diff
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-diff ./cmd/mcp-diff

FROM builder AS build-mcp-web-search
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-web-search ./cmd/mcp-web-search

FROM builder AS build-mcp-http
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-http ./cmd/mcp-http

FROM builder AS build-mcp-graphql
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-graphql ./cmd/mcp-graphql

FROM builder AS build-mcp-netbox
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-netbox ./cmd/mcp-netbox

FROM builder AS build-mcp-promql
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-promql ./cmd/mcp-promql

FROM builder AS build-mcp-zulip
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-zulip ./cmd/mcp-zulip

FROM builder AS build-mcp-trello
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-trello ./cmd/mcp-trello

FROM builder AS build-mcp-knowledgebase
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-knowledgebase ./cmd/mcp-knowledgebase

FROM builder AS build-mcp-acp
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-acp ./cmd/mcp-acp

FROM builder AS build-mcp-shell
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/mcp-shell ./cmd/mcp-shell

FROM builder AS build-agentd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/agentd ./cmd/agentd

FROM builder AS build-authd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/authd ./cmd/authd

FROM builder AS build-local-idp
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/local-idp ./cmd/local-idp

FROM builder AS build-auth-admin
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/auth-admin ./cmd/auth-admin

FROM builder AS build-webd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/webd ./cmd/webd

FROM builder AS build-zulip-bridge
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/zulip-bridge ./cmd/zulip-bridge

FROM builder AS build-slack-bridge
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/slack-bridge ./cmd/slack-bridge

FROM builder AS build-discord-bridge
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/discord-bridge ./cmd/discord-bridge

FROM builder AS build-matrix-bridge
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/matrix-bridge ./cmd/matrix-bridge

FROM builder AS build-apikeyd
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/apikeyd ./cmd/apikeyd

FROM builder AS build-llm-proxy
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/llm-proxy ./cmd/llm-proxy

# =============================================================================
# Stage 3: Runtime images
# =============================================================================

# chat (interactive terminal application)
FROM alpine:latest AS chat
RUN apk add --no-cache ca-certificates
COPY --from=build-chat /app/chat /app/chat
WORKDIR /app
ENTRYPOINT ["/app/chat"]

# Debug tool for context dumping
FROM alpine:latest AS dump-context
RUN apk add --no-cache ca-certificates
COPY --from=build-dump-context /app/dump-context /app/dump-context
WORKDIR /app
ENTRYPOINT ["/app/dump-context"]

# MCP Filesystem Server (file operations in sandboxed directory)
FROM alpine:latest AS mcp-filesystem
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp && \
    mkdir -p /mcp/root && \
    chown mcp:mcp /mcp/root
USER mcp
COPY --from=build-mcp-filesystem /app/mcp-filesystem /app/mcp-filesystem
WORKDIR /app
ENTRYPOINT ["/app/mcp-filesystem"]

# MCP Memory Server (persistent key-value store)
FROM alpine:latest AS mcp-memory
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp && \
    mkdir -p /data && \
    chown mcp:mcp /data
USER mcp
COPY --from=build-mcp-memory /app/mcp-memory /app/mcp-memory
WORKDIR /app
ENTRYPOINT ["/app/mcp-memory"]

# MCP Time Server (time/date utilities)
FROM alpine:latest AS mcp-time
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-time /app/mcp-time /app/mcp-time
WORKDIR /app
ENTRYPOINT ["/app/mcp-time"]

# MCP Fetch Server (HTTP/REST client)
FROM alpine:latest AS mcp-fetch
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-fetch /app/mcp-fetch /app/mcp-fetch
WORKDIR /app
ENTRYPOINT ["/app/mcp-fetch"]

# MCP Diff Server (text/file diffing)
FROM alpine:latest AS mcp-diff
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-diff /app/mcp-diff /app/mcp-diff
WORKDIR /app
ENTRYPOINT ["/app/mcp-diff"]

# MCP Web Search Server (Brave Search API)
FROM alpine:latest AS mcp-web-search
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-web-search /app/mcp-web-search /app/mcp-web-search
WORKDIR /app
ENTRYPOINT ["/app/mcp-web-search"]

# MCP HTTP Server (generic HTTP client)
FROM alpine:latest AS mcp-http
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-http /app/mcp-http /app/mcp-http
WORKDIR /app
ENTRYPOINT ["/app/mcp-http"]

# MCP GraphQL Server (generic GraphQL client)
FROM alpine:latest AS mcp-graphql
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-graphql /app/mcp-graphql /app/mcp-graphql
WORKDIR /app
ENTRYPOINT ["/app/mcp-graphql"]

# MCP Netbox Server (Netbox DCIM/IPAM via GraphQL)
FROM alpine:latest AS mcp-netbox
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-netbox /app/mcp-netbox /app/mcp-netbox
WORKDIR /app
ENTRYPOINT ["/app/mcp-netbox"]

# MCP PromQL Server (Prometheus/PromQL query interface)
FROM alpine:latest AS mcp-promql
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-promql /app/mcp-promql /app/mcp-promql
WORKDIR /app
ENTRYPOINT ["/app/mcp-promql"]

# MCP Zulip Server (Zulip messaging client)
FROM alpine:latest AS mcp-zulip
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp && \
    mkdir -p /mcp/root/attachments && \
    chown -R mcp:mcp /mcp/root
USER mcp
COPY --from=build-mcp-zulip /app/mcp-zulip /app/mcp-zulip
WORKDIR /app
ENTRYPOINT ["/app/mcp-zulip"]

# MCP Trello Server (Trello board/card management)
FROM alpine:latest AS mcp-trello
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-trello /app/mcp-trello /app/mcp-trello
WORKDIR /app
ENTRYPOINT ["/app/mcp-trello"]

# MCP Knowledgebase Server (semantic + BM25 hybrid search over Markdown documents)
FROM alpine:latest AS mcp-knowledgebase
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp && \
    mkdir -p /data && \
    chown mcp:mcp /data
USER mcp
COPY --from=build-mcp-knowledgebase /app/mcp-knowledgebase /app/mcp-knowledgebase
WORKDIR /app
ENTRYPOINT ["/app/mcp-knowledgebase"]

FROM alpine:latest AS mcp-acp
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 mcp
USER mcp
COPY --from=build-mcp-acp /app/mcp-acp /app/mcp-acp
WORKDIR /app
ENTRYPOINT ["/app/mcp-acp"]

# MCP Shell Server (sandboxed shell command execution)
# Uses debian:bookworm-slim for a richer Unix environment (sh, bash, coreutils, etc.)
FROM debian:bookworm-slim AS mcp-shell
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        bash \
        coreutils \
        curl \
        git \
        python3 \
        && rm -rf /var/lib/apt/lists/* \
    && useradd -r -u 1000 -m mcp \
    && mkdir -p /workspace \
    && chown mcp:mcp /workspace
USER mcp
COPY --from=build-mcp-shell /app/mcp-shell /app/mcp-shell
WORKDIR /workspace
ENTRYPOINT ["/app/mcp-shell"]

# Agent daemon (LLM, MCP, tools, session management over WebSocket)
FROM alpine:latest AS agentd
RUN apk add --no-cache ca-certificates && mkdir -p /app/skills
COPY --from=build-agentd /app/agentd /app/agentd
WORKDIR /app
EXPOSE 7777
# Mount skills at /app/skills (e.g. -v ~/.claude/skills:/app/skills:ro)
VOLUME ["/app/skills"]
ENV SKILLS_DIRS=/app/skills
ENTRYPOINT ["/app/agentd"]

# Web UI BFF (serves Svelte SPA, handles auth cookies, proxies to agentd)
FROM alpine:latest AS webd
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 webd && \
    mkdir -p /tmp/mcp-workspace/web-uploads && \
    chown -R webd:webd /tmp/mcp-workspace
USER webd
COPY --from=build-webd /app/webd /app/webd
WORKDIR /app
EXPOSE 3000
ENTRYPOINT ["/app/webd"]

# Auth admin CLI (schema migrations + user management for authd and local-idp)
# Runs as a one-shot Job in container environments (not a long-running service).
FROM alpine:latest AS auth-admin
RUN apk add --no-cache ca-certificates
COPY --from=build-auth-admin /app/auth-admin /app/auth-admin
ENTRYPOINT ["/app/auth-admin"]

# Local OIDC identity provider (dev + production-ready local accounts)
FROM alpine:latest AS local-idp
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 localidp
USER localidp
COPY --from=build-local-idp /app/local-idp /app/local-idp
WORKDIR /app
EXPOSE 9999
ENTRYPOINT ["/app/local-idp"]

# Auth daemon (OAuth2 identity broker — issues tokens to all internal services)
FROM alpine:latest AS authd
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 authd
USER authd
COPY --from=build-authd /app/authd /app/authd
WORKDIR /app
EXPOSE 8888
ENTRYPOINT ["/app/authd"]

# Zulip bridge (translates between Zulip events and agentd WebSocket protocol)
FROM alpine:latest AS zulip-bridge
RUN apk add --no-cache ca-certificates
COPY --from=build-zulip-bridge /app/zulip-bridge /app/zulip-bridge
WORKDIR /app
ENTRYPOINT ["/app/zulip-bridge"]

# Slack bridge (translates between Slack Socket Mode events and agentd WebSocket protocol)
FROM alpine:latest AS slack-bridge
RUN apk add --no-cache ca-certificates
COPY --from=build-slack-bridge /app/slack-bridge /app/slack-bridge
WORKDIR /app
ENTRYPOINT ["/app/slack-bridge"]

# Discord bridge (translates between Discord Gateway events and agentd WebSocket protocol)
FROM alpine:latest AS discord-bridge
RUN apk add --no-cache ca-certificates
COPY --from=build-discord-bridge /app/discord-bridge /app/discord-bridge
WORKDIR /app
ENTRYPOINT ["/app/discord-bridge"]

# Matrix bridge (translates between Matrix Client-Server API events and agentd WebSocket protocol)
FROM alpine:latest AS matrix-bridge
RUN apk add --no-cache ca-certificates
COPY --from=build-matrix-bridge /app/matrix-bridge /app/matrix-bridge
WORKDIR /app
ENTRYPOINT ["/app/matrix-bridge"]

# apikeyd — API key management service
FROM alpine:latest AS apikeyd
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 apikeyd
USER apikeyd
COPY --from=build-apikeyd /app/apikeyd /app/apikeyd
WORKDIR /app
EXPOSE 8889
ENTRYPOINT ["/app/apikeyd"]

# llm-proxy — bidirectional LLM API proxy
FROM alpine:latest AS llm-proxy
RUN apk add --no-cache ca-certificates
COPY --from=build-llm-proxy /app/llm-proxy /app/llm-proxy
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["/app/llm-proxy"]
