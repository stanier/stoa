# Security Policy

## Supported Versions

Stoa is currently under active development. Security fixes are applied to the latest code on `main`; there are no maintained release branches at this time.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

To report a vulnerability, email **keyton.j.stanier@gmail.com** with:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept (if available)
- Any suggested mitigations you have identified

You should receive an acknowledgement within **72 hours**. We aim to provide a more detailed response — including an assessment and expected timeline — within **7 days**.

Once a fix is ready we will coordinate disclosure with you before publishing. We appreciate responsible disclosure and will credit reporters in the release notes unless you prefer to remain anonymous.

## Scope

Areas of particular security relevance in this project:

- **MCP shell server** (`internal/mcpserver/shellserver`) — executes arbitrary shell commands; sandboxed via Docker/Podman but worth scrutiny
- **Permission system** (`internal/permissions`) — gates tool execution; bypass would allow unauthorized tool calls
- **Auth flow** (`internal/authd`, `internal/authdclient`) — OAuth2 token issuance and validation
- **WebSocket protocol** (`internal/agentproto`) — unauthenticated or spoofed frames could hijack sessions
- **SSRF protections** — agentd makes outbound requests on behalf of users; bypasses are in scope

Out of scope: vulnerabilities in upstream dependencies (report those to their maintainers), denial-of-service through excessive resource consumption without an authentication bypass, or theoretical issues with no practical exploit path.

## Security Design Notes

The project's security architecture and known hardening work in progress are documented in `ROADMAP.md` under the Security Hardening section.
