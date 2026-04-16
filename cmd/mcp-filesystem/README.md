# MCP Filesystem Server

A Model Context Protocol (MCP) server implementation that provides filesystem operations for a specified directory.

## Overview

This server exposes filesystem tools (read, write, list, search) via the MCP protocol, allowing LLM assistants to interact with a directory through a well-defined interface. It uses stdio transport for communication and validates all paths to prevent directory traversal attacks.

## Building

```bash
# Using Makefile
make build-mcp

# Or directly
go build -o mcp-filesystem ./cmd/mcp-filesystem
```

## Usage

### Standalone Testing

```bash
# Run with current directory as root
./mcp-filesystem

# Run with specific root directory
./mcp-filesystem -root /path/to/directory

# Run with logging
./mcp-filesystem -root /tmp -log /tmp/mcp-fs.log
```

### With Chat Client

Add to `~/.config/stoa/config.yaml`:

```yaml
mcp:
  enabled: true
  servers:
    - name: local-filesystem
      transport: stdio
      command: ./mcp-filesystem
      args:
        - -root
        - /path/to/directory
        - -log
        - /tmp/mcp-fs.log
```

## Command Line Flags

- `-root` (string): Root directory for filesystem operations (default: ".")
- `-log` (string): Log file path (default: logs to stderr)

## Available Tools

### read_file

Read the contents of a file within the root directory.

**Parameters:**
- `path` (string, required): Relative path to the file

**Example:**
```json
{
  "name": "read_file",
  "arguments": {
    "path": "docs/README.md"
  }
}
```

### write_file

Write content to a file within the root directory. Creates parent directories if needed.

**Parameters:**
- `path` (string, required): Relative path to the file
- `content` (string, required): Content to write

**Example:**
```json
{
  "name": "write_file",
  "arguments": {
    "path": "output/result.txt",
    "content": "Hello, world!"
  }
}
```

### list_directory

List contents of a directory within the root.

**Parameters:**
- `path` (string, optional): Relative path to directory (empty for root)

**Example:**
```json
{
  "name": "list_directory",
  "arguments": {
    "path": "src"
  }
}
```

### search_files

Search for files by name pattern within the root directory.

**Parameters:**
- `pattern` (string, required): Filename pattern (supports wildcards: `*`, `?`)
- `path` (string, optional): Directory to search in (empty for root)

**Example:**
```json
{
  "name": "search_files",
  "arguments": {
    "pattern": "*.go",
    "path": "internal"
  }
}
```

## Protocol

The server implements MCP protocol version 2024-11-05 using JSON-RPC 2.0 over stdio.

### Example Session

```bash
# Initialize
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"clientInfo":{"name":"test","version":"1.0"}}}' | ./mcp-filesystem -root /tmp

# List tools
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | ./mcp-filesystem -root /tmp

# Call tool
echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"test.txt"}}}' | ./mcp-filesystem -root /tmp
```

## Security

- **Path Validation**: All paths are validated to ensure they stay within the root directory
- **No Directory Traversal**: `../` sequences are cleaned and validated
- **Explicit Root**: Root directory must be explicitly specified
- **Same Permissions**: Runs with the same permissions as the parent process

## Architecture

- `main.go`: Entry point, CLI parsing, signal handling
- `internal/mcpserver/types.go`: Core MCP protocol types
- `internal/mcpserver/stdio_server.go`: JSON-RPC stdio transport
- `internal/mcpserver/filesystem/server.go`: Filesystem tool implementations

## Troubleshooting

**Check logs:**
```bash
tail -f /tmp/mcp-fs.log
```

**Common issues:**
- **Permission denied**: Check that the root directory exists and is readable
- **Path escapes root**: Ensure paths don't use `../` to escape
- **Tool not found**: Verify tool name matches exactly (case-sensitive)

## License

MIT
