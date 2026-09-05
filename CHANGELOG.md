# Changelog

## 0.1.0 (2026-09-05)


### Features

* **ci:** conventional commits and automated releases ([38f4d17](https://github.com/jdziat/vastai-mcp/commit/38f4d175c033060c91b5f683b8cd1cb665605c1e))
* **release:** sign checksums keylessly with cosign ([cffe7e2](https://github.com/jdziat/vastai-mcp/commit/cffe7e246bb7cb08f8ba8dbc88ce3a5d64acf8ed))


### Bug Fixes

* **release:** valid goreleaser config (template in flow sequence broke YAML parsing) ([36b0928](https://github.com/jdziat/vastai-mcp/commit/36b09282f49fe960904b1bbec6e78615d1bb245b))


### Documentation

* **remediation:** drop key-rotation follow-up (single-user workstation, not required) ([fe32efd](https://github.com/jdziat/vastai-mcp/commit/fe32efd5d6f0eaeb5b5fc6cf2456198a00a9c7d4))

## Changelog

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
