# Security

## Reporting a vulnerability

Use GitHub's private vulnerability reporting for this repository:
https://github.com/jdziat/vastai-mcp/security/advisories/new

Please do not open public issues for vulnerabilities. You should receive an
acknowledgement within 7 days.

## Supported versions

Only the latest minor release line receives security fixes.

## Threat model

This server holds a Vast.ai API key that can spend money and destroy
instances, and it is driven by a language model. The controls below are
enforced in this process and do not depend on the model behaving.

| Risk | Control |
| --- | --- |
| Model creates, destroys, grants SSH access, or runs `rm` without the user's intent | `-confirm` (default on): those tools return a preview and require a human answer through the client's elicitation prompt. The answer is bound to the tool, arguments, previewed price, and preview text with an HMAC-signed, single-use token, so it cannot be forged, replayed, or applied to a different action. Decline is final. On stdio/loopback a `confirm: true` argument is accepted as a fallback; on remote HTTP it is not. |
| Runaway spend | `-max-dph` rejects creates and starts whose total hourly cost, including storage, exceeds the cap, and re-checks the real price after creation. `-max-instances` caps concurrency. `-read-only` removes mutating tools entirely. Malformed guardrail values refuse to start. |
| Hostile working directory | `.env` may set only `VASTAI_API_KEY` / `VAST_API_KEY`. Proxy and CA settings are captured before `.env` is read. `VASTAI_BASE_URL` is never read from `.env`. |
| Network exposure of `-http` | Non-loopback binds require a bearer token (env or file, never argv) and TLS unless `-insecure-http`. Cross-origin protection is enabled. |
| Prompt injection via container output | Logs and command output are ANSI-stripped, capped, and wrapped in `<untrusted>` delimiters with a preamble. |
| Server-side request forgery via API responses | Asynchronous result URLs must be https to a Vast.ai or AWS S3 host; IP literals and other hosts are refused. |
| Secret leakage to the model | `api_key` is always stripped; `jupyter_token` and similar fields are redacted (recursively, including lists) unless `-expose-instance-secrets`. |
| Secret leakage to logs | Audit records drop `image_login` and `onstart`, log `env` keys only, and fingerprint public keys. |
| Arbitrary remote execution | `vast_execute` accepts only `ls`, `du`, `rm` with no shell metacharacters, validated locally. The Vast.ai server-side filter is looser, so the local check is the control. |
| Supply chain | Releases are built by a SHA-pinned workflow and `checksums.txt` is signed with Sigstore cosign under the workflow's identity; see the README for verification. |

## Escape hatches

`VASTAI_ALLOW_INSECURE_BASE_URL=1` disables the https requirement on the API
base URL. It exists for local test stubs only; with it set, the bearer token
travels in cleartext to whatever `-base-url` names.

## Rotating a key

If a key was ever placed in a `.env` on a shared or CI machine, rotate it at
https://cloud.vast.ai/account/.
