# Changelog

Entries below this line are maintained by release-please.

## Pre-release notes

### Added
- MCP server for Vast.ai with 16 tools: offer/template search, instance
  lifecycle, logs, restricted exec, account, SSH keys.
- Enforced guardrails: `-confirm` (default **on**), `-max-dph`,
  `-max-instances`, `-read-only`, `-audit-log`.
- Streamable HTTP mode with bearer auth, TLS, and origin protection.
- Recorded API fixtures pinning units and response shapes.

### Breaking / notable defaults
- `-confirm` defaults to true: create, destroy, and `rm` require confirmation.
  Scripted callers must pass `-confirm=false` or answer the elicitation.
- `.env` files may set only `VASTAI_API_KEY` / `VAST_API_KEY`.
- `-http` refuses non-loopback binds without a token and TLS.
