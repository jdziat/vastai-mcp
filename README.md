# vastai-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server for the
[Vast.ai](https://vast.ai) GPU marketplace, written in Go. It lets an MCP client
(Claude Code, Claude Desktop, Cursor, etc.) search offers, rent and manage
instances, read logs, and manage SSH keys, with enforced spend and destruction
guardrails.

## Install

Release binaries with checksums are attached to each
[GitHub release](https://github.com/jdziat/vastai-mcp/releases). Or build from
source:

```sh
go install github.com/jdziat/vastai-mcp/cmd/vastai-mcp@latest
# or, from a checkout
make build   # -> bin/vastai-mcp
```

## Authentication

The API key is resolved in this order:

1. `VASTAI_API_KEY` or `VAST_API_KEY` environment variable
2. A `.env` file in the working directory. Only those two keys are read from
   it; every other line is ignored and reported on stderr.
3. `~/.config/vastai/vast_api_key` or `~/.vast_api_key` (written by the official CLI)

Get a key from https://cloud.vast.ai/account/.

## Client configuration

Claude Code:

```sh
claude mcp add vastai -e VASTAI_API_KEY=... -e VASTAI_MAX_DPH=1.00 -- vastai-mcp
```

Generic JSON (Claude Desktop, Cursor, etc.):

```json
{
  "mcpServers": {
    "vastai": {
      "command": "vastai-mcp",
      "args": ["-max-dph", "1.00", "-max-instances", "2"],
      "env": { "VASTAI_API_KEY": "..." }
    }
  }
}
```

## Guardrails

These are enforced inside the server and do not rely on the model.

| Flag | Env | Default | Effect |
| --- | --- | --- | --- |
| `-confirm` | `VASTAI_CONFIRM` | `true` | `vast_create_instance`, `vast_destroy_instance`, and `vast_execute rm …` return a cost/impact preview and ask the user through the client's confirmation prompt (MCP elicitation). The answer is bound to the previewed arguments and price with a signed token; a decline is final. Clients without elicitation may pass `confirm: true` on stdio or loopback only. On such clients the check is advisory, since the model sets the flag; use `-max-dph`, `-max-instances`, or `-read-only` for enforcement there. |
| `-max-dph` | `VASTAI_MAX_DPH` | `0` (off) | Reject creates whose GPU plus storage cost exceeds this $/hr. Checked against the live offer before the request and against the real instance price after. A malformed value refuses to start. |
| `-max-instances` | `VASTAI_MAX_INSTANCES` | `0` (off) | Reject creates when this many instances already exist. Checked before and after approval; two approvals interleaving within the same second can still overshoot by one, since Vast.ai has no server-side reservation. |
| `-read-only` | `VASTAI_READ_ONLY` | `false` | Register only the read-only tools. |
| `-audit-log FILE` | `VASTAI_AUDIT_LOG` | | Append one JSON record per mutating call (mode 0600). stderr always receives them. Credentials are never logged. |
| `-expose-instance-secrets` | | `false` | Return `jupyter_token` and similar fields to the model. |

Logs and command output are wrapped in `<untrusted>` delimiters and stripped
of ANSI escapes so the model can tell container data from instructions.

## HTTP mode

```sh
vastai-mcp -http 127.0.0.1:8080          # loopback: no token needed
VASTAI_MCP_TOKEN=... vastai-mcp -http 0.0.0.0:8443 -tls-cert cert.pem -tls-key key.pem
```

Loopback binds take no token, so any local process on that machine can drive
the server; on shared hosts, set `VASTAI_MCP_TOKEN` anyway.

A non-loopback bind refuses to start without a bearer token (from
`VASTAI_MCP_TOKEN` or `-http-token-file`; never a flag) **and** TLS.
`-insecure-http` allows plaintext, which sends the token and every tool
payload readable on the network. Anyone who can reach the port with the token
can spend your Vast.ai credit and destroy your instances. On non-loopback
binds the `confirm: true` fallback is disabled, so the client must support
elicitation.

## Tools

| Tool | Description |
| --- | --- |
| `vast_search_offers` | Search rentable GPU offers by GPU, VRAM, price, reliability, region, etc. |
| `vast_search_templates` | Search recommended public templates (PyTorch, ComfyUI, vLLM, ...) |
| `vast_list_instances` | List your instances with status, cost and SSH details |
| `vast_show_instance` | Details for one instance |
| `vast_create_instance` | Rent an offer with an image or template (spends money, confirmed) |
| `vast_start_instance` / `vast_stop_instance` | Change instance state |
| `vast_reboot_instance` | Restart the container without losing data |
| `vast_destroy_instance` | Destroy an instance and its disk (irreversible, confirmed) |
| `vast_label_instance` | Set a label |
| `vast_instance_logs` | Fetch container logs (untrusted output) |
| `vast_execute` | Run `ls`, `du`, or `rm` inside an instance (`rm` confirmed) |
| `vast_show_user` | Account info and credit balance |
| `vast_list_ssh_keys` / `vast_create_ssh_key` / `vast_attach_ssh_key` | Manage SSH keys |

`vast_search_offers` and `vast_search_templates` accept a `raw_query` /
`raw_filters` JSON object for any Vast.ai query field not exposed as a
parameter (see `vastai search offers --help` for the field list).

## Development

```sh
make check        # vet + race tests + govulncheck
make lint         # golangci-lint + gosec
```

`internal/vast/testdata/` holds responses recorded from the live API with a
provenance header; tests pin units (RAM in MB, disk in GB, price per offer)
and response shapes against them.

## Contributing and releases

Commits follow [Conventional Commits](https://www.conventionalcommits.org):
`feat:` bumps the minor version, `fix:` the patch, and a `!` or
`BREAKING CHANGE:` footer bumps the major (minor while below 1.0). Run
`make hooks` once to enable the local `commit-msg` check; CI enforces it with
commitlint.

Releases are automatic: release-please opens a "chore(main): release x.y.z"
PR from the commits on `main`, and merging it tags the version, updates
`CHANGELOG.md`, and publishes cross-platform binaries with checksums via
goreleaser.

## Layout

```
cmd/vastai-mcp/   entrypoint, flags, transport selection
internal/serve/   HTTP bind policy and bearer auth
internal/vast/    Vast.ai REST client, pinned transport, .env loader
internal/tools/   MCP tools, guardrails, audit log
```

## License

MIT, see `LICENSE`.
