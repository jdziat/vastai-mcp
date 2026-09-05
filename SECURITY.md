# Security

## Threat model

This server holds a Vast.ai API key that can spend money and destroy
instances, and it is driven by a language model. The controls below are
enforced in this process and do not depend on the model behaving.

| Risk | Control |
| --- | --- |
| Model creates/destroys without the user's intent | `-confirm` (default on): create, destroy, and `rm` return a preview and require a human answer through the client's elicitation prompt. Decline is final. On stdio/loopback a `confirm: true` argument is accepted as a fallback; on remote HTTP it is not. |
| Runaway spend | `-max-dph` rejects creates above a $/hr cap before any request is sent, and re-checks the real price after creation. `-max-instances` caps concurrency. `-read-only` removes mutating tools entirely. |
| Hostile working directory | `.env` may set only `VASTAI_API_KEY` / `VAST_API_KEY`. Proxy and CA settings are captured before `.env` is read. `VASTAI_BASE_URL` is never read from `.env`. |
| Network exposure of `-http` | Non-loopback binds require a bearer token (env or file, never argv) and TLS unless `-insecure-http`. Origin checks are enabled. |
| Prompt injection via container output | Logs and command output are ANSI-stripped and wrapped in `<untrusted>` delimiters with a preamble. |
| Secret leakage to the model | `api_key` is always stripped; `jupyter_token` and similar are redacted unless `-expose-instance-secrets`. |
| Secret leakage to logs | Audit records drop `image_login`, log `env` keys only, and fingerprint public keys. |
| Arbitrary remote execution | `vast_execute` accepts only `ls`, `du`, `rm` with no shell metacharacters, validated locally. |

## Reporting

Open a private security advisory on the GitHub repository, or email the
maintainer listed in `go.mod`'s module path owner. Please do not file public
issues for vulnerabilities.

## Rotating a key

If a key was ever placed in a `.env` on a shared or CI machine, rotate it at
https://cloud.vast.ai/account/.
