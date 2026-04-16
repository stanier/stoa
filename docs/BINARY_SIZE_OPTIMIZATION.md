# Binary Size Optimization Guide

This document explains various techniques for reducing binary sizes in this Go project.

## Current State

As of 2026-02-20:
- **Debug builds** (bin/debug/): ~108M total
- **Stripped builds** (bin/dist/): ~74M total (31% reduction)
- **Largest binaries**: chat (27M), server (20M)
- **Smallest binaries**: MCP servers (~2.1M each)

## Optimization Techniques (In Order of Impact)

### 1. Strip Debug Symbols ✅ (Already Implemented)
**Command**: `go build -ldflags="-s -w"`
**Reduction**: ~30-35%
**Tradeoffs**: None for production
**Details**:
- `-s`: Disable symbol table
- `-w`: Disable DWARF debugging info
- No runtime cost
- Makes debugging harder (use debug builds for development)

**Usage**: `make dist-all`

### 2. Remove Filesystem Paths ✅ (Already Implemented)
**Command**: `go build -trimpath`
**Reduction**: ~1-5%
**Tradeoffs**: Stack traces show relative paths instead of absolute
**Details**:
- Removes build directory paths from binary
- Improves reproducible builds
- Security benefit: doesn't leak filesystem structure

**Usage**: Automatic in all `dist-*` targets

### 3. UPX Compression (Optional)
**Command**: `upx --best --lzma <binary>`
**Reduction**: ~50-70%
**Tradeoffs**:
- Slower startup (decompression overhead)
- Higher memory usage during startup
- Some systems flag UPX binaries as suspicious
- Cannot be used with code signing on some platforms

**Installation**: `sudo apt-get install upx-ucl`
**Usage**:
```bash
make upx          # Compress chat binary
make upx-all      # Compress all binaries
```

**Size comparison example**:
```
Original (debug):    37M
Stripped:            27M  (27% reduction)
UPX compressed:      ~8M  (78% total reduction)
```

### 4. Dependency Analysis (Advanced)

**Check what's actually in your binary**:
```bash
# See all dependencies
go list -m all

# See why a specific module is included
go mod why github.com/some/module

# Find module graph
go mod graph | grep specific-module
```

**Heavy dependencies in this project**:
- Bubbletea + Charm ecosystem (TUI, markdown rendering)
- Anthropic SDK + OpenAI SDK
- Viper (configuration)
- tiktoken-go (token counting)

**Optimization ideas**:
- Could make providers build-tag conditional (only include one SDK)
- Could make TUI optional for server builds
- Could lazy-load heavy dependencies

### 5. Build Tags (Not Yet Implemented)

Split features into optional builds:
```go
// +build with_anthropic

package provider

import "github.com/anthropics/anthropic-sdk-go"
```

Build with specific providers:
```bash
go build -tags with_anthropic
go build -tags with_openai
```

**Potential savings**: 5-15% per excluded provider

### 6. Lazy Loading (Code Change Required)

Instead of importing all dependencies at package level:
```go
// Before: all imported at startup
import "github.com/heavy/dependency"

// After: lazy load when needed
if needFeature {
    dep := loadDependency() // dynamic loading
}
```

**Complexity**: High
**Savings**: Varies (doesn't reduce binary size, but reduces memory usage)

## Why NOT Shared Libraries

**Question**: Could we use Go's `-buildmode=shared` or `-buildmode=plugin`?

**Answer**: Not recommended for this project because:

1. **Deployment complexity**: Instead of `scp binary server:/usr/local/bin/`, you need to deploy multiple .so files and manage paths
2. **Different dependency profiles**:
   - `chat` is large because of TUI libraries (Bubbletea, Glamour)
   - `server` is large because of Zulip client
   - The shared `internal/` code is relatively small
   - You'd ship all the .so files anyway, so total distribution size doesn't improve
3. **CGO requirement**:
   - Breaks Go's easy cross-compilation
   - Requires C toolchain on build machine
   - Runtime linking can fail due to version mismatches
4. **Only makes sense at scale**: We have 8 binaries. Shared libs make sense when you have 50+ binaries on the same system.
5. **Plugin system limitations**: Go's plugin system is experimental and has significant limitations

**When shared libs DO make sense**:
- Microservice architecture with dozens of similar services
- Plugin systems where you need hot-reloading
- Embedded systems with tight storage constraints and many binaries

## Recommendations

**For development**:
```bash
make build-all    # Debug builds in bin/debug/
```

**For distribution** (choose based on requirements):

1. **General use** (recommended):
   ```bash
   make dist-all     # Stripped + trimpath (74M total)
   ```

2. **Bandwidth-constrained** (slower startup acceptable):
   ```bash
   make upx-all      # UPX compressed (~25-30M total)
   ```

3. **Custom optimization** (advanced users):
   - Analyze dependencies: `go list -m all`
   - Use build tags to exclude providers
   - Profile startup time vs binary size tradeoffs

## Size Measurement Commands

```bash
# Total size by directory
du -sh bin/debug bin/dist

# Individual binary sizes
ls -lh bin/dist/

# Compare before/after
make dist && ls -lh bin/dist/chat
make upx && ls -lh bin/dist/chat

# See what's in the binary (debug builds only)
go tool nm -size bin/debug/chat | sort -rn -k2 | head -20
```

## Future Optimization Ideas

1. **Provider build tags**: Only include Anthropic OR OpenAI SDK
2. **Minimal server build**: Server doesn't need TUI dependencies
3. **Shared config parsing**: Extract viper into separate tool
4. **Static analysis**: Use tools like `bloaty` or `go-binsize-viz` to visualize what's in the binary
5. **Module analysis**: Check if `internal/` is actually small or hiding bloat

## References

- [Go linker flags](https://pkg.go.dev/cmd/link)
- [UPX documentation](https://upx.github.io/)
- [Go build modes](https://pkg.go.dev/cmd/go#hdr-Build_modes)
- [Reducing binary size in Go](https://blog.filippo.io/shrink-your-go-binaries-with-this-one-weird-trick/)
