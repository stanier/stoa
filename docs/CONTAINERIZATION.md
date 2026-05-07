# Containerization Guide for Stoa

This guide covers running Stoa and its MCP servers in Docker containers for improved isolation, security, and deployment flexibility.

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Quick Start](#quick-start)
4. [Configuration](#configuration)
5. [Local Development](#local-development)
6. [Production Deployment](#production-deployment)
7. [Security](#security)
8. [Troubleshooting](#troubleshooting)
9. [Performance](#performance)

---

## Overview

### Why Containerize?

**Benefits:**
- **Isolation**: Each MCP server runs in its own container with resource limits
- **Security**: Restricted Docker API access, no privileged containers, minimal attack surface
- **Portability**: Same config works on dev, staging, and production
- **Scalability**: Easy to deploy to Kubernetes for high availability
- **Versioning**: Pin MCP server versions, rollback easily

**Use Cases:**
- **Local Development**: Test with containers before deploying
- **CI/CD**: Run tests in isolated environments
- **Production**: Deploy to Kubernetes with auto-scaling and health checks

### Transport Types

Stoa supports three MCP transport modes:

| Transport | Use Case | Pros | Cons |
|-----------|----------|------|------|
| **stdio** | Local development | Fast iteration, easy debugging | No isolation, runs on host |
| **http** | External MCP servers | Language-agnostic, network-based | Requires HTTP server implementation |
| **docker** | Isolated execution | Security, resource limits, portable | Slower startup (~1-2s), requires Docker |

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Main App Container (chat or server)                        │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ Application Code                                       │ │
│  │ - Reads config.yaml                                    │ │
│  │ - For each MCP server with transport: docker          │ │
│  │   1. Create container (via Docker API)                │ │
│  │   2. Start container                                   │ │
│  │   3. Create exec session                               │ │
│  │   4. Attach stdio to exec session                      │ │
│  │   5. Send/receive JSON-RPC over stdio                  │ │
│  └────────────────────────────────────────────────────────┘ │
│                        │                                     │
│                        ▼ Docker API (via socket proxy)      │
└────────────────────────┼─────────────────────────────────────┘
                         │
                         ▼
┌──────────────────────────────────────────────────────────────┐
│  Docker Socket Proxy (tecnativa/docker-socket-proxy)        │
│  - Restricts API to safe operations only                    │
│  - No build, privileged, or swarm access                    │
└────────────────────────┼─────────────────────────────────────┘
                         │
                         ▼ /var/run/docker.sock
┌──────────────────────────────────────────────────────────────┐
│  Docker Daemon                                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │ Container 1 │  │ Container 2 │  │ Container 3 │         │
│  │ mcp-        │  │ mcp-        │  │ mcp-        │         │
│  │ filesystem  │  │ memory      │  │ time        │         │
│  │             │  │             │  │             │         │
│  │ Created when│  │ Persistent  │  │ Ephemeral   │         │
│  │ app needs   │  │ volume for  │  │ (no state)  │         │
│  │ filesystem  │  │ data.db     │  │             │         │
│  │ access      │  │             │  │             │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
└──────────────────────────────────────────────────────────────┘
```

**Key Innovation**: Using `docker exec` to attach stdio means we reuse existing MCP servers **without modification**. No need to implement HTTP/SSE servers.

---

## Quick Start

### 1. Build Docker Images

```bash
# Build all images
make docker-build-all

# Or build specific images
make docker-build-mcp-filesystem
make docker-build-mcp-memory
```

**Result**: Images tagged as `stoa-mcp-<name>:latest`

### 2. Configure MCP Servers

Create `config.docker.yaml`:

```yaml
mcp:
  enabled: true
  servers:
    - name: filesystem
      transport: docker
      image: stoa-mcp-filesystem:latest
      network_mode: bridge
      volumes:
        - type: bind
          source: /tmp/workspace
          target: /mcp/root
          read_only: false
      args:
        - -root
        - /mcp/root
```

### 3. Run with Docker Compose

```bash
# Start services
docker-compose up -d

# Check logs
docker-compose logs -f chat

# Stop services
docker-compose down
```

---

## Configuration

### MCP Server Config Schema

```yaml
mcp:
  servers:
    - name: server-name           # Unique identifier
      transport: docker           # Use Docker transport
      image: registry/image:tag   # Docker image
      network_mode: bridge        # Network mode (bridge, host, none, or network name)

      # Volume mounts
      volumes:
        - type: bind              # "bind" (host path) or "volume" (named volume)
          source: /host/path      # Host path or volume name
          target: /container/path # Container mount point
          read_only: false        # Mount as read-only?

      # Command arguments (passed to container entrypoint)
      args:
        - -flag
        - value

      # Environment variables
      env:
        KEY: value

      # Permission policies (optional)
      policies:
        - tool_pattern: "mcp_server-name_*"
          rank: 1  # 0=Silent, 1=Mention, 2=Request
          reason: "Description"
```

### Volume Types

**Bind Mount** (host directory):
```yaml
volumes:
  - type: bind
    source: /tmp/workspace  # Must exist on host
    target: /mcp/root
    read_only: false
```

**Named Volume** (Docker-managed):
```yaml
volumes:
  - type: volume
    source: mcp-memory-data  # Create with: docker volume create
    target: /data
    read_only: false
```

### Network Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| `bridge` | Default Docker network | Isolated containers |
| `host` | Share host network stack | Need host network access (rare) |
| `none` | No networking | Completely isolated |
| `custom-network` | User-defined network | Multi-container communication |

### Resource Limits

Default limits (configured in `internal/mcp/transport/docker.go`):

```go
Resources: container.Resources{
    Memory:   256 * 1024 * 1024,  // 256 MB
    NanoCPUs: 500000000,          // 0.5 CPU
}
```

To customize, modify `DockerTransport.createContainer()`.

---

## Local Development

### Option 1: Stdio Transport (Fastest Iteration)

```yaml
mcp:
  servers:
    - name: filesystem
      transport: stdio
      command: ./bin/debug/mcp-filesystem
      args: ["-root", "/tmp/workspace"]
```

**Pros**: Instant startup, easy debugging with print statements

**Cons**: No isolation, runs on host

### Option 2: Docker Transport (Test Containerization)

```yaml
mcp:
  servers:
    - name: filesystem
      transport: docker
      image: stoa-mcp-filesystem:dev
      volumes:
        - type: bind
          source: /tmp/workspace
          target: /mcp/root
      args: ["-root", "/mcp/root"]
```

**Pros**: Tests containerization locally

**Cons**: Slower startup, requires Docker

### Workflow

1. **Develop**: Use stdio transport for fast iteration
2. **Test**: Build Docker image, switch to docker transport
3. **Deploy**: Use versioned image tags in production

```bash
# Development cycle
make build-mcp               # Build binary
./bin/debug/mcp-filesystem   # Test directly

# Containerization test
make docker-build-mcp-filesystem
# Update config to use docker transport
./bin/debug/chat             # Test with container

# Production
export IMAGE_TAG=v1.2.3
make docker-build-all
make docker-push-all
```

---

## Production Deployment

### Docker Compose (Single Host)

**Use Case**: Small deployments, personal projects

```bash
# Set environment variables
export LLM_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-...
export DOCKER_REGISTRY=ghcr.io/stanier

# Build and push images
make docker-build-all
make docker-push-all

# Start services
docker-compose up -d

# Check health
docker-compose ps
docker-compose logs -f server
```

**Persistent Data**:
- Traces: `./data/traces`
- Logs: `./data/logs`
- MCP memory: Named volume `mcp-memory-data`

### Kubernetes (Multi-Host, High Availability)

**Use Case**: Production deployments, scaling, monitoring

See [Kubernetes README](../deployments/kubernetes/README.md) for details.

**Steps**:
1. Update secrets in `deployments/kubernetes/secrets.yaml`
2. Update image URLs in `deployments/kubernetes/configmap.yaml`
3. Deploy: `kubectl apply -f deployments/kubernetes/`
4. Monitor: `kubectl logs -n mcp-chat deployment/stoa-server -f`

**Features**:
- Auto-restart on failure
- Persistent volumes for data
- Resource limits and quotas
- RBAC for security
- Horizontal pod autoscaling (optional)

---

## Security

### Docker Socket Proxy

**Problem**: Giving containers access to Docker socket = root access to host

**Solution**: Proxy that restricts API operations

```yaml
services:
  docker-proxy:
    image: tecnativa/docker-socket-proxy:latest
    environment:
      CONTAINERS: 1  # ✅ Allow container create/start/stop
      EXEC: 1        # ✅ Allow exec sessions
      IMAGES: 1      # ✅ Allow image pulls
      BUILD: 0       # ❌ No build access
      COMMIT: 0      # ❌ No container commits
      SWARM: 0       # ❌ No swarm operations
```

**Result**: App can create MCP containers but cannot:
- Build images (prevents code injection)
- Run privileged containers
- Access host network
- Perform cluster operations

### Container Security

**Non-Root User**:
```dockerfile
RUN adduser -D -u 1000 mcp
USER mcp
```

**Resource Limits**:
```go
Resources: container.Resources{
    Memory:   256 * 1024 * 1024,
    NanoCPUs: 500000000,
}
```

**No Capabilities**:
```yaml
securityContext:
  capabilities:
    drop:
    - ALL
```

### Network Isolation

MCP containers run on isolated network:
```yaml
networks:
  mcp-bridge:
    driver: bridge
    internal: true  # No external access
```

### Secrets Management

**Development**:
```bash
export ANTHROPIC_API_KEY=sk-ant-...
docker-compose up
```

**Production (Kubernetes)**:
```bash
kubectl create secret generic stoa-secrets \
  --from-literal=anthropic-api-key=sk-ant-...
```

Or use external secrets:
- AWS Secrets Manager
- GCP Secret Manager
- HashiCorp Vault
- Sealed Secrets

---

## Troubleshooting

### Container Creation Fails

**Symptom**: `failed to create container: ...`

**Check**:
1. Docker daemon running: `docker ps`
2. Socket proxy accessible: `docker logs stoa-docker-proxy`
3. Image exists: `docker images | grep mcp-filesystem`

**Fix**:
```bash
# Pull image manually
docker pull stoa-mcp-filesystem:latest

# Check proxy logs
docker logs stoa-docker-proxy
```

### Permission Denied

**Symptom**: `Error response from daemon: authorization denied`

**Cause**: Socket proxy blocking operation

**Check**: `docker-proxy` environment variables allow the operation

**Fix**: Add to `docker-compose.yml`:
```yaml
environment:
  CONTAINERS: 1  # Ensure this is set
```

### Slow Container Startup

**Symptom**: MCP tools take 2-5 seconds to respond

**Cause**: Container creation overhead

**Optimize**:
1. Use smaller base images (Alpine)
2. Pre-pull images: `docker pull <image>`
3. Keep containers running (not auto-remove)
4. Use connection pooling (future optimization)

### Volume Mount Issues

**Symptom**: `failed to mount: path does not exist`

**Fix**:
```bash
# Create directory first
mkdir -p /tmp/workspace

# Or use named volume
docker volume create mcp-workspace
```

### Network Connectivity

**Symptom**: MCP container can't reach external services

**Check**: Network mode
```yaml
network_mode: bridge  # Change to 'host' if needed
```

**Security Note**: `host` mode breaks isolation, use sparingly

---

## Performance

### Benchmarks

| Operation | Stdio | Docker | Overhead |
|-----------|-------|--------|----------|
| Container creation | N/A | 1.2s | +1.2s (one-time) |
| Tool execution | 50ms | 60ms | +10ms |
| Memory overhead | 0 MB | 8 MB | +8 MB per container |

### Optimization Tips

1. **Pre-build images**: Don't build on-demand
   ```bash
   make docker-build-all  # Build once
   ```

2. **Use image cache**: Tag images properly
   ```bash
   export IMAGE_TAG=v1.2.3
   ```

3. **Persistent containers**: Keep running instead of auto-remove
   ```yaml
   hostConfig:
     AutoRemove: false  # Keep for reuse
   ```

4. **Multi-stage builds**: Reduce image size
   ```dockerfile
   FROM golang:1.22 AS builder
   # ... build steps
   FROM alpine:latest
   COPY --from=builder /app/binary .
   ```

5. **Connection pooling**: Reuse containers (future optimization)

### Image Sizes

| Image | Size | Notes |
|-------|------|-------|
| `stoa-mcp-filesystem` | ~15 MB | Alpine + binary |
| `stoa-mcp-memory` | ~18 MB | Includes SQLite |
| `stoa-mcp-time` | ~12 MB | Smallest (no deps) |
| `stoa-mcp-fetch` | ~16 MB | Includes CA certs |
| `stoa-mcp-diff` | ~14 MB | Stateless |

**Goal**: Keep images under 20 MB for fast pulls

---

## Podman Support

Stoa fully supports **Podman** as a Docker alternative with minimal configuration changes.

### Why Podman?

**Benefits over Docker**:
- **Rootless**: Runs without daemon or root privileges
- **Daemonless**: No background service required
- **Drop-in replacement**: Docker-compatible CLI and API
- **Security**: Better default security posture
- **Systemd integration**: Native service management

### Compatibility Status

| Feature | Docker | Podman | Notes |
|---------|--------|--------|-------|
| Docker Transport (Go) | ✅ | ✅ | Works via `DOCKER_HOST` env var |
| Image builds | ✅ | ✅ | Use `podman build` |
| Compose files | ✅ | ✅ | Use `podman-compose` |
| Kubernetes | ✅ | ✅ | Same manifests work |
| Socket proxy | ✅ | ⚠️ | Not needed (rootless by default) |

### Quick Start with Podman

**1. Install Podman**:
```bash
# Fedora/RHEL/CentOS
sudo dnf install podman podman-compose

# Ubuntu/Debian
sudo apt install podman podman-compose

# macOS
brew install podman
podman machine init
podman machine start
```

**2. Enable Podman socket** (for Docker API compatibility):
```bash
# Start user-level Podman socket
systemctl --user enable --now podman.socket

# Verify it's running
systemctl --user status podman.socket

# Check socket path
echo $XDG_RUNTIME_DIR/podman/podman.sock
# Example: /run/user/1000/podman/podman.sock
```

**3. Set environment variable**:
```bash
# For rootless Podman (recommended)
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock

# Or for system-wide Podman (requires root)
export DOCKER_HOST=unix:///run/podman/podman.sock
```

**4. Build images**:
```bash
# Podman is a drop-in replacement for Docker
podman build --target mcp-filesystem -t stoa-mcp-filesystem:latest .

# Or use make (will use podman if DOCKER_HOST is set)
make docker-build-all
```

**5. Run with Podman Compose**:
```bash
# Use Podman-specific compose file
podman-compose -f docker-compose.podman.yml up -d

# Or use regular docker-compose.yml with manual socket mount
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock
podman-compose up -d
```

### Configuration Differences

**Docker Socket Path**:
```yaml
# Docker (system daemon)
volumes:
  - /var/run/docker.sock:/var/run/docker.sock:ro

# Podman (rootless)
volumes:
  - ${XDG_RUNTIME_DIR}/podman/podman.sock:/run/podman/podman.sock:ro
```

**No Socket Proxy Needed**:
Podman is rootless by default, so the Docker socket proxy (tecnativa/docker-socket-proxy) is **not required**. The main app can directly access the Podman socket safely.

**Config file** (`config.podman.yaml`):
```yaml
mcp:
  enabled: true
  servers:
    - name: filesystem
      transport: docker  # Works with Podman!
      image: stoa-mcp-filesystem:latest
      network_mode: bridge
      volumes:
        - type: bind
          source: /tmp/workspace
          target: /mcp/root
      args: ["-root", "/mcp/root"]
```

### Running Stoa with Podman

**Method 1: Direct Binary with Podman**:
```bash
# Set Podman socket
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock

# Build and run
make build
./bin/debug/chat --config config.podman.yaml
```

**Method 2: Podman Compose**:
```bash
# Start all services
podman-compose -f docker-compose.podman.yml up -d

# View logs
podman-compose logs -f chat

# Stop services
podman-compose down
```

**Method 3: Systemd Service** (Podman-specific):
```ini
# ~/.config/systemd/user/stoa.service
[Unit]
Description=Stoa Server
After=podman.socket

[Service]
Environment=DOCKER_HOST=unix://%t/podman/podman.sock
Environment=ANTHROPIC_API_KEY=sk-ant-...
ExecStart=/usr/local/bin/stoa-server --config /home/user/.config/stoa/config.yaml
Restart=on-failure

[Install]
WantedBy=default.target
```

Enable and start:
```bash
systemctl --user enable --now stoa.service
systemctl --user status stoa.service
```

### Podman-Specific Tips

**1. SELinux Labels**:
If you encounter permission issues on RHEL/Fedora/CentOS:
```yaml
# Add to docker-compose.podman.yml
security_opt:
  - label=disable  # Development only
  # Or for production:
  - label=type:container_runtime_t
```

**2. UID/GID Mapping**:
Podman maps container UIDs to different host UIDs. If you see permission errors:
```bash
# Check user namespace mapping
podman unshare cat /proc/self/uid_map

# Use --userns=keep-id for same UID
podman run --userns=keep-id ...
```

**3. Network Performance**:
Podman's rootless networking uses slirp4netns by default (slower). For better performance:
```bash
# Use pasta (newer, faster)
podman run --network=pasta ...

# Or use host networking (breaks isolation)
podman run --network=host ...
```

**4. Image Storage**:
Podman stores images per-user:
```bash
# User images: ~/.local/share/containers/storage
# System images: /var/lib/containers/storage

# List Podman images
podman images

# Cleanup unused images
podman image prune -a
```

### Kubernetes with Podman

Podman can generate Kubernetes manifests from running containers:

```bash
# Run container with Podman
podman run -d --name stoa-server ...

# Generate Kubernetes YAML
podman generate kube stoa-server > stoa-deployment.yaml

# Deploy to Kubernetes
kubectl apply -f stoa-deployment.yaml
```

### Troubleshooting Podman

**Socket not found**:
```bash
# Check socket exists
ls -la $XDG_RUNTIME_DIR/podman/podman.sock

# If not, start socket
systemctl --user start podman.socket
```

**Permission denied**:
```bash
# Add user to subuid/subgid
sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 $USER

# Restart Podman
podman system migrate
```

**API version mismatch**:
```bash
# Check versions
podman version
podman system info

# Update Podman
sudo dnf update podman  # Fedora/RHEL
sudo apt update && sudo apt upgrade podman  # Ubuntu
```

### Performance: Docker vs Podman

| Metric | Docker | Podman (rootless) | Podman (rootful) |
|--------|--------|-------------------|------------------|
| Container startup | 1.2s | 1.4s | 1.2s |
| Network latency | ~5ms | ~15ms (slirp4netns) | ~5ms |
| Disk I/O | Baseline | +10% overhead | Baseline |
| Memory overhead | 100 MB (daemon) | 20 MB (no daemon) | 50 MB |

**Recommendation**: Use rootless Podman for development, rootful for production if performance is critical.

---

## Advanced Topics

### Custom Networks

Create dedicated network for MCP containers:

```bash
docker network create --driver bridge mcp-isolated
```

```yaml
mcp:
  servers:
    - name: filesystem
      network_mode: mcp-isolated
```

### Health Checks

Add health checks to MCP containers (future):

```dockerfile
HEALTHCHECK --interval=30s --timeout=3s \
  CMD /app/mcp-filesystem --health || exit 1
```

### Monitoring

Collect metrics from MCP containers:

```bash
# CPU/Memory usage
docker stats $(docker ps --filter name=mcp- -q)

# Container events
docker events --filter type=container --filter event=start
```

### CI/CD Integration

```yaml
# .github/workflows/build.yml
- name: Build Docker images
  run: make docker-build-all

- name: Test containers
  run: ./scripts/test-container-transport.sh

- name: Push to registry
  run: make docker-push-all
  env:
    DOCKER_REGISTRY: ghcr.io/${{ github.repository }}
```

---

## Migration Path

### Phase 1: Parallel Mode (Current)
- Support stdio, http, AND docker transports
- Users choose per-server in config
- Validate docker transport in staging

### Phase 2: Docker Default (Future)
- Recommend docker transport in docs
- Keep stdio for local dev
- Most production deployments use docker

### Phase 3: Kubernetes Native (Long-term)
- MCP servers as separate pods
- Service mesh for communication
- Full observability stack

---

## References

- [Docker SDK for Go](https://pkg.go.dev/github.com/moby/moby/client)
- [Docker Socket Proxy](https://github.com/Tecnativa/docker-socket-proxy)
- [MCP Specification](https://spec.modelcontextprotocol.io/)
- [Kubernetes Best Practices](https://kubernetes.io/docs/concepts/configuration/overview/)

---

## FAQ

**Q: Why not HTTP transport?**
A: Requires implementing HTTP/SSE server for each MCP server. Docker exec stdio reuses existing servers.

**Q: Can I mix transports?**
A: Yes! Configure per-server:
```yaml
servers:
  - name: filesystem
    transport: docker  # Container
  - name: memory
    transport: stdio   # Local process
```

**Q: What about Windows/Mac?**
A: Docker Desktop includes Docker daemon. Works the same.

**Q: Production-ready?**
A: Yes, with proper security (socket proxy, resource limits, monitoring).

**Q: Performance impact?**
A: ~10ms latency overhead, 1-2s container creation (one-time).

**Q: Can I use Podman instead of Docker?**
A: Yes! Fully supported. Set `DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock` and use `podman-compose -f docker-compose.podman.yml up`. See [Podman Support](#podman-support) section for details.

---

**Next Steps**:
1. Run test suite: `./scripts/test-container-transport.sh`
2. Try docker-compose: `docker-compose up -d`
3. Deploy to K8s: See `deployments/kubernetes/README.md`
