# Security Hardening — Remaining Work

What's already in place: rootless Podman, non-root container users, CapDrop ALL, no-new-privileges, ReadonlyRootfs, IPC isolation, swap disabled, PID limits, network split (internal MCP / agent-external), mcp-shell on isolated `mcp-shell-net` with squid egress, permission policy system, SSRF protection, tool result pagination (50KB cap), filesystem/shell path traversal protection.

---

## Landlock filesystem sandboxing ⚠️ stalled

Previous attempt stalled on `go get github.com/landlock-lsm/go-landlock` timing out.

**Plan** (ready to implement once dep fetches):

1. Add `github.com/landlock-lsm/go-landlock` to `go.mod`
2. Create `internal/mcpserver/sandbox/landlock.go`:
   ```go
   // RestrictToPath applies Landlock to restrict this process's filesystem access.
   // Uses BestEffort() so it degrades silently on kernels < 5.13.
   func RestrictToPath(allowedRW string, extraROPaths ...string) error
   ```
3. `internal/mcpserver/filesystem/server.go` — call at startup, restrict to `s.rootDir` only
4. `internal/mcpserver/shellserver/server.go` — restrict to `s.workspaceRoot` + broad read-only system paths (`/usr`, `/lib`, `/lib64`, `/bin`, `/sbin`, `/etc`, `/proc`, `/tmp`)

Key API:
```go
landlock.V3.BestEffort().RestrictPaths(
    landlock.RWDirs(allowedRW),
    landlock.RODirs("/usr", "/lib", "/etc"),
)
```

`BestEffort()` is required — without it the process fails to start on kernels < 5.13.

---

## Podman secrets for API keys

API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.) are in `environment:` and visible via `podman inspect agentd`.

**Fix**: Podman secrets + `_FILE` env var pattern. Requires:
- `secrets:` section in `docker-compose.yml`
- Small update to `internal/config/config.go` to read `ANTHROPIC_API_KEY_FILE` etc.

---

## `cap_drop` + `no-new-privileges` in compose service definitions

Currently set for MCP containers via `internal/mcp/transport/docker.go`. Not set in `docker-compose.yml` for the agentd, webd, zulip-bridge, authd, local-idp service definitions (those services have only `security_opt: - label=disable`).

**Fix**: Add to each long-running service in `docker-compose.yml`:
```yaml
cap_drop:
  - ALL
security_opt:
  - no-new-privileges:true
```

---

## seccomp for mcp-shell

`exec.CommandContext()` in `internal/mcpserver/shellserver/server.go` runs arbitrary commands with no syscall filtering.

**Fix**: explicit seccomp-bpf filter blocking `ptrace`, `mount`, `keyctl`, `clone` with `CLONE_NEWUSER`. Library option: `github.com/elastic/go-seccomp-bpf`.

---

## Bubblewrap transport

Alternative to Docker for deployments without a container daemon. `bwrap --unshare-net --unshare-pid --ro-bind /usr /usr` gives a clean sandbox for the shell server.

Would be `internal/mcp/transport/bubblewrap.go` implementing the same `Transport` interface as `docker.go`.

---

## Image signing (Sigstore/cosign)

Enforce cosign image verification for non-`:local` images via `~/.config/containers/policy.json`. Not started.
