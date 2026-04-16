# MCP Permissions System

## Overview

The MCP Permissions System provides fine-grained control over how and when MCP server tools can execute across all deployment modes:
- **TUI** — Interactive permission prompts in the terminal UI
- **agentd + zulip-bridge** — Permission prompts forwarded to the connected client
- **Headless/server mode** — Auto-approval or auto-deny with audit logging

## Permission Ranks

Tools are assigned permission ranks that control how they execute:

| Rank | Name | Behavior | Example Tools |
|------|------|----------|---------------|
| 0 | **Silent** | Execute without notification | Git status, read-only API calls, documentation search |
| 1 | **Mention** | Execute and notify user | File read, web fetch |
| 2 | **Request** | Require explicit permission | File write, file delete, git push |
| 3 | **Authenticated Request** | Requires user authentication | *Not yet implemented* |
| 4 | **Authorized Request** | Requires delegated permission | *Not yet implemented* |
| 5 | **Approved Request** | Requires approval workflow | *Not yet implemented* |

**Note:** Currently only ranks 0–2 are implemented. Ranks 3–5 will be added in a future phase.

---

## Configuration

### Enabling Permissions

Add to `~/.config/stoa/config.yaml`:

```yaml
permissions:
  enabled: true
  policy_file: "~/.config/stoa/permissions.yaml"
  default_rank: 2  # Conservative default: require permission for unknown tools
  audit_log: true
  audit_log_path: "~/.config/stoa/permissions-audit.jsonl"
  # Headless mode only:
  auto_approve_rank2: false  # Set to true for automatic approval in headless mode
```

### Environment Variables

```bash
export PERMISSIONS_ENABLED=true
export PERMISSIONS_POLICY_FILE=/path/to/custom-permissions.yaml
export PERMISSIONS_DEFAULT_RANK=1
export PERMISSIONS_AUDIT_LOG=false
export PERMISSIONS_AUTO_APPROVE_RANK2=true  # headless auto-approval
```

### Policy File

The policy file (`~/.config/stoa/permissions.yaml`) defines which tools require authorization:

```yaml
policies:
  # File read operations - Rank 1 (mention)
  - tool_pattern: "mcp_*_file_read"
    rank: 1
    reason: "File read operations should be visible for transparency"

  - tool_pattern: "mcp_*_read_file"
    rank: 1
    reason: "File read operations should be visible for transparency"

  # File write operations - Rank 2 (request permission)
  - tool_pattern: "mcp_*_file_write"
    rank: 2
    reason: "File write operations require explicit permission"

  - tool_pattern: "mcp_*_write_file"
    rank: 2
    reason: "File write operations require explicit permission"

  # File delete operations - Rank 2 (request permission)
  - tool_pattern: "mcp_*_file_delete"
    rank: 2
    reason: "File delete operations require explicit permission"

  # Default catch-all
  - tool_pattern: "*"
    rank: 2
    reason: "Default: require permission for unrecognized tools"

# User-granted permissions (populated at runtime)
grants: []
```

On first run, default policies are automatically installed from `config/permissions-default.yaml`, covering file, git, web, database, and Docker operations.

### Getting Started

```bash
cp config/permissions-default.yaml ~/.config/stoa/permissions.yaml
# Edit the file to customize policies, then restart the application
```

---

## Pattern Matching

Tool patterns use glob-style matching:

- `*` matches any sequence of characters
- `?` matches a single character
- `[abc]` matches any character in the set
- `[a-z]` matches any character in the range

### Examples

```yaml
# Match all file write operations across all MCP servers
- tool_pattern: "mcp_*_file_write"
  rank: 2

# Match specific server
- tool_pattern: "mcp_filesystem_*"
  server_name: "filesystem"
  rank: 1

# Match exact tool name
- tool_pattern: "mcp_websearch_search"
  rank: 0

# Match all tools (catch-all)
- tool_pattern: "*"
  rank: 2
```

### Policy Priority

When multiple policies match a tool, the most specific match wins:

1. Exact tool name + server name
2. Pattern match + server name
3. Exact tool name (no server specified)
4. Pattern match (no server specified)

---

## Usage by Deployment Mode

### TUI (Interactive Mode)

**Rank 0 (Silent):** Tools execute immediately without notification.

**Rank 1 (Mention):** Tools execute immediately; user sees:
```
🔧 mcp_filesystem_file_read: Reading file /home/user/data.json
```

**Rank 2 (Request):** Permission prompt overlay appears:

```
╔═══════════════════════════════════════════════════════════════════╗
║  Permission Request                                                ║
║                                                                    ║
║  Tool: mcp_filesystem_file_write                                  ║
║  Server: filesystem                                               ║
║  Description: Write content to a file                             ║
║                                                                    ║
║  Parameters:                                                       ║
║    path: /home/user/document.txt                                  ║
║    content: Hello, World!                                         ║
║                                                                    ║
║  [ Deny (n) ]  [ Allow (y) ]  <- selected                         ║
║                                                                    ║
║  [ ] Remember for this session (space)                            ║
║                                                                    ║
║  Tab: switch  Space: toggle  Enter: confirm  a: always allow      ║
╚═══════════════════════════════════════════════════════════════════╝
```

**Keyboard shortcuts:**
- **Tab** — Switch between Deny and Allow
- **Space** — Toggle "Remember for this session"
- **Enter** — Confirm selection
- **y/Y** — Quick approve (this time only)
- **n/N** — Quick deny
- **a/A** — Always allow (permanent grant saved to config)

**Remember options:**
- *This time only* — Approve/deny for this single request
- *Remember for this session* — Apply decision until application restarts
- *Always allow (a)* — Create permanent grant

### Headless Mode (agentd, server, zulip-bridge)

**Option A: Auto-Approve** (recommended for trusted environments)
```yaml
permissions:
  enabled: true
  auto_approve_rank2: true
  audit_log: true
```
Tools execute immediately; all executions logged to audit file.

**Option B: Auto-Deny** (default — safest)
```yaml
permissions:
  enabled: true
  auto_approve_rank2: false
```
Rank 2 requests timeout and deny after 60 seconds. Logged as a warning.

---

## Audit Log

When `audit_log: true`, all permission decisions are logged to JSONL format.

**File:** `~/.config/stoa/permissions-audit.jsonl`

**Format:**
```json
{
  "timestamp": "2026-02-16T10:30:00Z",
  "tool_name": "mcp_filesystem_file_write",
  "server_name": "filesystem",
  "rank": 2,
  "decision": true,
  "parameters": {"path": "/home/user/file.txt", "content": "Hello..."}
}
```

The audit log automatically redacts sensitive parameter keys: `password`, `api_key`, `secret`, `token`, `credential`, `private_key`.

**Querying with jq:**
```bash
# View all decisions
cat ~/.config/stoa/permissions-audit.jsonl | jq .

# Show denied requests
cat ~/.config/stoa/permissions-audit.jsonl | jq 'select(.decision == false)'

# Count operations by tool
cat ~/.config/stoa/permissions-audit.jsonl | jq -r '.tool_name' | sort | uniq -c

# Show recent decisions (last 10)
tail -10 ~/.config/stoa/permissions-audit.jsonl | jq .
```

---

## Example Configurations

### Development (Relaxed)

```yaml
permissions:
  enabled: true
  default_rank: 1
  auto_approve_rank2: true
  audit_log: true

policies:
  - tool_pattern: "mcp_*_delete*"
    rank: 2  # Still require approval for deletes
```

### Production (Strict)

```yaml
permissions:
  enabled: true
  default_rank: 2
  auto_approve_rank2: false
  audit_log: true
  audit_log_path: "/var/log/stoa/permissions-audit.jsonl"

policies:
  - tool_pattern: "mcp_*_read*"
    rank: 1
  - tool_pattern: "*"
    rank: 2
```

### Headless Server (Trusted Environment)

```yaml
permissions:
  enabled: true
  default_rank: 1
  auto_approve_rank2: true
  audit_log: true
  audit_log_path: "/var/log/stoa/permissions-audit.jsonl"

policies:
  - tool_pattern: "mcp_*"
    rank: 1
```

---

## Troubleshooting

**Tool always requires permission even after granting:**
1. Check if "Remember for this session" was selected — it expires on restart
2. Verify the tool pattern in config matches the actual tool name
3. Check for conflicting policies (more specific policy may override)

**Permission prompt not appearing:**
1. Verify `permissions.enabled: true` in config
2. Check that policy rank is 2 (Request)
3. Confirm a previous session grant isn't still active

**Server denying all Rank 2 tools:**
Set `auto_approve_rank2: true` or disable permissions entirely with `enabled: false`.

**Reset all grants:**
```yaml
grants: []
```
Then restart the application.

---

## Security Considerations

1. **Default Deny** — Unmatched tools default to Rank 2 (Request)
2. **Audit Trail** — All decisions logged for compliance
3. **Parameter Sanitization** — Sensitive data redacted in logs
4. **No Bypass** — Tools cannot bypass the permission system
5. **Auto-approve cautiously** — Only in trusted environments; always enable audit logging

## Future Enhancements

- **Ranks 3–5** — Authentication and approval workflows
- **Zulip Reaction Approval** — React ✅/❌ to permission requests in Zulip
- **Time-based policies** — Auto-approve during business hours
- **Fine-grained parameter filtering** — Allow writes to specific paths only
- **Multi-user authorization** — Require approval from multiple users
