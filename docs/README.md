# Stoa Documentation

Welcome to the Stoa documentation repository. This directory contains guides and references for the project.

## User Guides

- [SETUP.md](SETUP.md) - Getting started (standalone, agentd, containerized)
- [PERMISSIONS.md](PERMISSIONS.md) - MCP tool permission system
- [SAMPLING_PARAMS.md](SAMPLING_PARAMS.md) - LLM sampling hyperparameter reference
- [ADDING_CHANNELS.md](ADDING_CHANNELS.md) - Adding new chat platform integrations
- [ZULIP_SERVER.md](ZULIP_SERVER.md) - Zulip bridge setup and usage

## Technical Reference

- [ARCHITECTURE.md](ARCHITECTURE.md) - **Runtime architecture**: component map, request lifecycle, auth flow, MCP lifecycle
- [AGENTD_REFACTOR.md](AGENTD_REFACTOR.md) - Architecture design: agentd + thin TUI split (historical)
- [MCP_FILESYSTEM_SERVER.md](MCP_FILESYSTEM_SERVER.md) - MCP filesystem server reference
- [MCP_SERVERS_ROADMAP.md](MCP_SERVERS_ROADMAP.md) - Implemented MCP servers and roadmap
- [MCP_INTEGRATIONS_PLAN.md](MCP_INTEGRATIONS_PLAN.md) - Third-party API server design notes
- [CONTAINERIZATION.md](CONTAINERIZATION.md) - Docker/Podman containerization guide
- [PODMAN.md](PODMAN.md) - Podman quick-start
- [BINARY_SIZE_OPTIMIZATION.md](BINARY_SIZE_OPTIMIZATION.md) - Build size optimization reference
- [SYNTHETIC_HISTORY_INJECTION.md](SYNTHETIC_HISTORY_INJECTION.md) - Design rationale for message history injection
- [INEFFICIENCIES.md](INEFFICIENCIES.md) - Performance audit log

## Remaining Work

- [remaining/README.md](remaining/README.md) - Decision-blocked items
- [remaining/SECURITY.md](remaining/SECURITY.md) - Security hardening remaining
- [remaining/MCP_SERVERS.md](remaining/MCP_SERVERS.md) - MCP servers not yet started
- [remaining/PROXY_COMPLIANCE.md](remaining/PROXY_COMPLIANCE.md) - Proxy compliance gaps
