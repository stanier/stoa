# Podman Quick Start Guide

This guide shows how to use Stoa with Podman instead of Docker.

## Why Podman?

- **Rootless**: No daemon, no root required
- **Drop-in replacement**: Compatible with Docker commands and APIs
- **Better security**: Rootless by default, no single point of failure
- **Systemd integration**: Native service management on Linux

## Quick Setup

### 1. Install Podman

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

### 2. Enable Podman Socket

```bash
# Start Podman API service
systemctl --user enable --now podman.socket

# Verify
systemctl --user status podman.socket
```

### 3. Set Environment Variable

Add to your `~/.bashrc` or `~/.zshrc`:

```bash
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock
```

Then reload:
```bash
source ~/.bashrc
```

## Usage

### Build Images

```bash
# Use Podman-specific Make targets
make podman-build-all

# Or use podman directly
podman build --target mcp-filesystem -t stoa-mcp-filesystem:latest .
```

### Run with Podman Compose

```bash
# Start services
make podman-compose-up
# Or: podman-compose up -d

# View logs
podman-compose logs -f agentd

# Stop services
make podman-compose-down
# Or: podman-compose down
```

### Run Stoa Binary

```bash
# Build binary
make build

# Run with Podman backend
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock
./bin/debug/chat
```

## Configuration

Use the same config files as Docker, just ensure `DOCKER_HOST` points to Podman socket:

```yaml
# config.yaml (works with both Docker and Podman)
mcp:
  enabled: true
  servers:
    - name: filesystem
      transport: docker  # Works with Podman!
      image: stoa-mcp-filesystem:latest
      volumes:
        - type: bind
          source: /tmp/workspace
          target: /mcp/root
      args: ["-root", "/mcp/root"]
```

## Systemd Integration (Linux)

Create `~/.config/systemd/user/stoa.service`:

```ini
[Unit]
Description=Stoa Server
After=podman.socket

[Service]
Environment=DOCKER_HOST=unix://%t/podman/podman.sock
Environment=ANTHROPIC_API_KEY=sk-ant-your-key-here
ExecStart=/usr/local/bin/agentd
Restart=on-failure

[Install]
WantedBy=default.target
```

Enable and start:
```bash
systemctl --user enable --now stoa.service
systemctl --user status stoa.service
journalctl --user -u stoa.service -f
```

## Differences from Docker

| Feature | Docker | Podman |
|---------|--------|--------|
| Daemon | Required | No daemon |
| Root | Needs root or group | Rootless by default |
| Socket path | `/var/run/docker.sock` | `$XDG_RUNTIME_DIR/podman/podman.sock` |
| Socket proxy | Recommended | Not needed (already rootless) |
| Compose tool | `docker-compose` | `podman-compose` |
| Service management | Manual | Native systemd support |

## Troubleshooting

### Socket not found

```bash
# Check if socket exists
ls -la $XDG_RUNTIME_DIR/podman/podman.sock

# Start socket if needed
systemctl --user start podman.socket
```

### Permission errors

```bash
# Enable user namespaces
sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 $USER

# Restart Podman
podman system migrate
```

### "Cannot connect to Podman socket"

```bash
# Check Podman service is running
systemctl --user status podman.socket

# Restart if needed
systemctl --user restart podman.socket

# Verify DOCKER_HOST is set correctly
echo $DOCKER_HOST
# Should show: unix:///run/user/1000/podman/podman.sock (or similar)
```

### SELinux issues (Fedora/RHEL)

```bash
# If containers can't access volumes, adjust SELinux
# Option 1: Disable SELinux labels (development only)
podman run --security-opt label=disable ...

# Option 2: Add correct labels
chcon -Rt svirt_sandbox_file_t /tmp/workspace
```

## Performance Tips

1. **Use pasta networking** (faster than slirp4netns):
   ```bash
   podman run --network=pasta ...
   ```

2. **Pre-pull images**:
   ```bash
   podman pull stoa-mcp-filesystem:latest
   ```

3. **Use volumes instead of bind mounts** when possible:
   ```bash
   podman volume create mcp-workspace
   ```

4. **Enable keep-id for file permissions**:
   ```yaml
   userns_mode: keep-id  # In podman-compose.yml
   ```

## Migration from Docker

If you're switching from Docker to Podman:

1. **Export Docker images**:
   ```bash
   docker save stoa-mcp-filesystem:latest | podman load
   ```

2. **Or rebuild with Podman**:
   ```bash
   make podman-build-all
   ```

3. **Update environment**:
   ```bash
   # Add to ~/.bashrc
   export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock
   ```

4. **Test**:
   ```bash
   make podman-compose-up
   ```

## Compatibility Summary

Stoa works out of the box with Podman with just one environment variable:

```bash
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock
```

No code changes are required. The Go code already works because:
- `client.FromEnv()` respects `DOCKER_HOST`
- Docker SDK is compatible with Podman's Docker-compatible API
- No Docker-specific features used (all OCI-standard)

### What Works

| Component | Status | Notes |
|-----------|--------|-------|
| **Docker Transport (Go code)** | ✅ | Uses `client.FromEnv()` — respects `DOCKER_HOST` |
| **Container creation** | ✅ | Same API calls as Docker |
| **Exec stdio attachment** | ✅ | `docker exec` equivalent works identically |
| **Volume mounts** | ✅ | Bind mounts and named volumes supported |
| **Network modes** | ✅ | Bridge, host, none all work |
| **Image builds** | ✅ | `podman build` is drop-in replacement |
| **Multi-stage Dockerfile** | ✅ | Works without changes |
| **Resource limits** | ✅ | Memory/CPU limits enforced |
| **Environment variables** | ✅ | Passed to containers normally |

### Makefile Targets

```bash
make podman-build-all          # Build all images
make podman-build-mcp-all      # Build MCP servers
make podman-compose-up         # Start with podman-compose
make podman-compose-down       # Stop services
```

### Testing

```bash
./scripts/test-container-transport.sh
```

### Production Use

Podman is production-ready and used by Red Hat OpenShift, Fedora/RHEL (default runtime), and many enterprise Linux deployments. Recommended for rootless security benefits on Linux servers.

## Resources

- [Podman Documentation](https://docs.podman.io/)
- [Podman Compose](https://github.com/containers/podman-compose)
- [Podman vs Docker](https://docs.podman.io/en/latest/markdown/podman-docker.1.html)
- [Full Containerization Guide](CONTAINERIZATION.md#podman-support)

---

**Quick Command Reference**:
```bash
# Setup
systemctl --user enable --now podman.socket
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock

# Build
make podman-build-all

# Run
make podman-compose-up

# Monitor
podman-compose logs -f

# Stop
make podman-compose-down
```
