# Stoa Documentation

Welcome to the Stoa documentation repository. This directory contains guides and references for the project.

## User Guides

- [SETUP.md](SETUP.md) - Getting started (standalone, agentd, containerized)
- [PERMISSIONS.md](PERMISSIONS.md) - MCP tool permission system
- [SAMPLING_PARAMS.md](SAMPLING_PARAMS.md) - LLM sampling hyperparameter reference
- [ZULIP_SERVER.md](ZULIP_SERVER.md) - Zulip bridge setup

## Technical Reference

- [ARCHITECTURE.md](ARCHITECTURE.md) - **Runtime architecture**: component map, request lifecycle, auth flow, MCP lifecycle
- [ARCHITECTURE_AGENTD.md](ARCHITECTURE_AGENTD.md) - Historical design doc: agentd + thin TUI split
- [MCP_FILESYSTEM_SERVER.md](MCP_FILESYSTEM_SERVER.md) - MCP filesystem server reference
- [MCP_INTEGRATIONS_PLAN.md](MCP_INTEGRATIONS_PLAN.md) - Third-party API server design notes
- [GATEWAY_FEATURES_PLAN.md](GATEWAY_FEATURES_PLAN.md) - Gateway implementation phases 1–4 (reference)
- [CONTAINERIZATION.md](CONTAINERIZATION.md) - Docker/Podman containerization guide
- [PODMAN.md](PODMAN.md) - Podman quick-start
- [BINARY_SIZE_OPTIMIZATION.md](BINARY_SIZE_OPTIMIZATION.md) - Build size optimization reference
- [SYNTHETIC_HISTORY_INJECTION.md](SYNTHETIC_HISTORY_INJECTION.md) - Design rationale for message history injection
- [LLM_PIPELINE_ARCHITECTURE.md](LLM_PIPELINE_ARCHITECTURE.md) - End-to-end pipeline architecture with sequence diagrams

## Remaining Work

See [ROADMAP.md](../ROADMAP.md) for all outstanding work: performance, security hardening, gateway features, MCP servers, bridge features, proxy compliance, and decision-blocked items.
