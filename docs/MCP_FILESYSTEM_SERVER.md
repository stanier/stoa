# MCP Filesystem Server

## Overview

A standalone MCP server implementation that provides filesystem operations (read, write, list, search) for a specified directory. Can be used as a subprocess MCP server or as a reference implementation for building custom MCP servers.

## Package Structure

```
cmd/mcp-filesystem/
└── main.go                    - Server executable

internal/mcpserver/
├── types.go                   - Core MCP protocol types
├── stdio_server.go            - JSON-RPC stdio transport handler
└── filesystem/
    └── server.go              - Filesystem tool implementations
```

## Building and Running

**Build:**
```bash
# Build MCP filesystem server
make build-mcp

# Or build directly
go build -o mcp-filesystem ./cmd/mcp-filesystem

# Build all binaries
make build-all
```

**Run Standalone:**
```bash
# Run with current directory as root
./mcp-filesystem

# Run with specific root directory
./mcp-filesystem -root /path/to/directory

# Run with logging to file
./mcp-filesystem -root /tmp -log /tmp/mcp-fs.log
```

**Run as MCP Server:**
The server is designed to be launched as a subprocess by MCP clients. It communicates via stdin/stdout using line-delimited JSON.

## Configuration

To use the filesystem server with the chat client, add to your config:

```yaml
mcp:
  enabled: true
  servers:
    - name: local-filesystem
      transport: stdio
      command: ./mcp-filesystem
      args:
        - -root
        - /home/user/projects
        - -log
        - /tmp/mcp-fs.log
```

**Configuration Parameters:**
- `command`: Path to the mcp-filesystem binary
- `-root`: Root directory for filesystem operations (required)
- `-log`: Log file path (optional, logs to stderr if omitted)

## Available Tools

**`read_file`**:
- Reads the contents of a file within the root directory
- Parameters:
  - `path` (string, required): Relative path to the file
- Returns: File contents as text
- Example:
  ```json
  {
    "path": "README.md"
  }
  ```

**`write_file`**:
- Writes content to a file within the root directory
- Creates parent directories if needed
- Parameters:
  - `path` (string, required): Relative path to the file
  - `content` (string, required): Content to write
- Returns: Success message with bytes written
- Example:
  ```json
  {
    "path": "output/result.txt",
    "content": "Hello, world!"
  }
  ```

**`list_directory`**:
- Lists contents of a directory within the root
- Parameters:
  - `path` (string, optional): Relative path to directory (empty for root)
- Returns: Directory listing with file/dir types and sizes
- Example:
  ```json
  {
    "path": "src"
  }
  ```

**`search_files`**:
- Searches for files by name pattern within the root
- Supports wildcards (*, ?)
- Parameters:
  - `pattern` (string, required): Filename pattern to search for
  - `path` (string, optional): Directory to search in (empty for root)
- Returns: List of matching file paths
- Example:
  ```json
  {
    "pattern": "*.go",
    "path": "internal"
  }
  ```

## Protocol Implementation

**JSON-RPC 2.0 Format:**
All messages follow JSON-RPC 2.0 specification, line-delimited on stdin/stdout.

**MCP Handshake:**
```json
// 1. Client → Server: initialize
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": { "tools": {} },
    "clientInfo": { "name": "client-name", "version": "1.0" }
  }
}

// 2. Server → Client: initialize response
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": { "tools": {} },
    "serverInfo": { "name": "mcp-filesystem", "version": "0.1.0" }
  }
}

// 3. Client → Server: initialized notification
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized"
}
```

**Tool Discovery:**
```json
// Request
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list"
}

// Response
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      {
        "name": "read_file",
        "description": "Read the contents of a file within /path/to/root",
        "inputSchema": {
          "type": "object",
          "properties": {
            "path": {
              "type": "string",
              "description": "Path to the file (relative to root directory)"
            }
          },
          "required": ["path"]
        }
      }
      // ... more tools
    ]
  }
}
```

**Tool Execution:**
```json
// Request
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "read_file",
    "arguments": {
      "path": "README.md"
    }
  }
}

// Response
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "# Project Title\n\nProject description..."
      }
    ],
    "isError": false
  }
}
```

## Architecture

**StdioServer** (`internal/mcpserver/stdio_server.go`):
- Handles JSON-RPC communication over stdin/stdout
- Reads line-delimited JSON from stdin
- Writes line-delimited JSON to stdout
- Dispatches requests to Server interface implementation
- Handles protocol errors and method routing

**FilesystemServer** (`internal/mcpserver/filesystem/server.go`):
- Implements Server interface
- Provides filesystem tools (read, write, list, search)
- Path validation ensures operations stay within root directory
- Security: Prevents directory traversal attacks

**Server Interface** (`internal/mcpserver/types.go`):
```go
type Server interface {
    Initialize(ctx, params) (*InitializeResult, error)
    ListTools(ctx) ([]Tool, error)
    CallTool(ctx, name, arguments) (*CallToolResult, error)
    Shutdown(ctx) error
}
```

## Security Considerations

1. **Path Validation**: All file operations validate that paths stay within the root directory
2. **Directory Traversal**: Prevents `../` escapes using `filepath.Clean()` and prefix checking
3. **Permissions**: Server runs with the same permissions as the parent process
4. **Root Directory**: Must be specified explicitly, no default to prevent unintended access

## Error Handling

**Path Validation Errors:**
```json
{
  "content": [
    { "type": "text", "text": "path escapes root directory: ../../etc/passwd" }
  ],
  "isError": true
}
```

**File Operation Errors:**
```json
{
  "content": [
    { "type": "text", "text": "Error reading file: open /path/to/file: no such file or directory" }
  ],
  "isError": true
}
```

**Protocol Errors:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32601,
    "message": "Method not found: invalid/method"
  }
}
```

## Testing

**Manual Testing:**
```bash
# Start server
./mcp-filesystem -root /tmp -log /tmp/mcp.log

# In another terminal, send commands (echo + paste, then Ctrl+D)
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"clientInfo":{"name":"test","version":"1.0"}}}' | ./mcp-filesystem -root /tmp

# List tools
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | ./mcp-filesystem -root /tmp

# Read file
echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"test.txt"}}}' | ./mcp-filesystem -root /tmp
```

**Integration Testing:**
Configure in chat client and use the LLM to interact with filesystem tools.

## Extending the Server

To add new tools:

1. Add tool handler to FilesystemServer:
   ```go
   s.handlers["new_tool"] = s.handleNewTool
   ```

2. Add tool description in ListTools():
   ```go
   {
       Name:        "new_tool",
       Description: "Tool description",
       InputSchema: map[string]interface{}{
           "type": "object",
           "properties": map[string]interface{}{
               "param": map[string]interface{}{
                   "type":        "string",
                   "description": "Parameter description",
               },
           },
           "required": []string{"param"},
       },
   }
   ```

3. Implement handler:
   ```go
   func (s *Server) handleNewTool(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
       // Implementation
   }
   ```

## Building Custom MCP Servers

Use this as a template for building other MCP servers:

1. Implement the `mcpserver.Server` interface
2. Register tool handlers in your server implementation
3. Use `mcpserver.NewStdioServer()` to wrap your server
4. Call `server.Run(ctx)` in main()

Example server types:
- Database server (SQL queries, schema inspection)
- API server (HTTP requests, webhook handling)
- Git server (repository operations)
- Docker server (container management)
- Custom domain-specific operations

## Known Limitations

1. **No streaming**: Tool results are returned in full, not streamed
2. **No resources**: Only implements tools, not MCP resources or prompts
3. **No caching**: File contents not cached between calls
4. **Synchronous only**: No concurrent tool execution
5. **No permissions**: All files within root are accessible

## Future Enhancements

1. **Streaming Support**: Stream large file contents
2. **Resource Support**: Expose files as MCP resources
3. **Watch Support**: Notify on file changes
4. **Permissions**: Fine-grained access control within root
5. **Caching**: Cache file metadata and contents
6. **Compression**: Compress large file transfers
7. **Binary Support**: Handle binary files (base64 encoding)
8. **Symlink Support**: Follow or skip symlinks (configurable)
