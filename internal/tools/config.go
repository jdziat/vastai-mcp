package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Config holds the enforced guardrails. All limits are enforced server-side
// in this process, independent of anything the model does.
type Config struct {
	// MaxDPH caps $/hr for vast_create_instance (0 = unlimited).
	MaxDPH float64
	// MaxInstances caps the number of instances that may exist when creating (0 = unlimited).
	MaxInstances int
	// ReadOnly leaves mutating tools unregistered.
	ReadOnly bool
	// Confirm requires human confirmation for create/destroy/rm.
	Confirm bool
	// ConfirmArgAllowed permits the `confirm: true` argument as a fallback when
	// the client cannot elicit. main sets this only for stdio or loopback binds.
	ConfirmArgAllowed bool
	// ExposeInstanceSecrets returns jupyter_token and similar fields to the model.
	ExposeInstanceSecrets bool
	// Audit receives one "AUDIT {json}" line per mutating call. Defaults to stderr.
	Audit io.Writer
	// AuditFile, if set, receives the same records as raw JSONL.
	AuditFile io.Writer
	// MaxOutputBytes caps every tool result (default 96 KiB).
	MaxOutputBytes int
}

const defaultMaxOutput = 96 * 1024

func (c *Config) maxOutput() int {
	if c.MaxOutputBytes > 0 {
		return c.MaxOutputBytes
	}
	return defaultMaxOutput
}

// OpenAuditLog opens (or creates, mode 0600) an append-only audit file and
// tightens the mode of an existing file that is group/world readable.
func OpenAuditLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- operator-supplied path by design
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil && fi.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(0o600); err != nil {
			fmt.Fprintf(os.Stderr, "vastai-mcp: warning: audit log %s is mode %o and could not be tightened: %v\n", path, fi.Mode().Perm(), err)
		}
	}
	return f, nil
}

// auditor writes one JSON record per mutating call. Never logs credentials.
// Stderr lines carry an "AUDIT " prefix for grep; the file sink (if any)
// receives raw JSONL.
type auditor struct {
	mu       sync.Mutex
	stderr   io.Writer
	file     io.Writer // may be nil
	failures int
}

// redactArgs returns a copy of args safe to log: image_login dropped,
// env values replaced by their keys, public keys fingerprinted.
func redactArgs(args any) any {
	var m map[string]any
	switch a := args.(type) {
	case map[string]any:
		m = a
	case json.RawMessage:
		if json.Unmarshal(a, &m) != nil {
			return "<unparseable>"
		}
	case []byte:
		if json.Unmarshal(a, &m) != nil {
			return "<unparseable>"
		}
	default:
		b, err := json.Marshal(args)
		if err != nil || json.Unmarshal(b, &m) != nil {
			return "<unparseable>"
		}
	}
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch k {
		case "image_login":
			out[k] = "<redacted>"
		case "onstart":
			// Start scripts routinely embed tokens; keep only the size.
			if sc, ok := v.(string); ok {
				out[k] = fmt.Sprintf("<redacted script, %d bytes>", len(sc))
			} else {
				out[k] = "<redacted>"
			}
		case "public_key":
			if s, ok := v.(string); ok {
				out[k] = keyFingerprint(s)
			} else {
				out[k] = "<redacted>"
			}
		case "env":
			if em, ok := v.(map[string]any); ok {
				keys := make([]string, 0, len(em))
				for ek := range em {
					keys = append(keys, ek)
				}
				out["env_keys"] = keys
			} else {
				out["env_keys"] = "<redacted>"
			}
		default:
			out[k] = v
		}
	}
	return out
}

func keyFingerprint(pub string) string {
	f := strings.Fields(pub)
	if len(f) < 2 {
		return "<invalid-key>"
	}
	b := f[1]
	if len(b) > 12 {
		b = b[:6] + "…" + b[len(b)-6:]
	}
	return f[0] + " " + b
}

func (a *auditor) log(tool string, args any, outcome string, extra map[string]any) {
	if a == nil {
		return
	}
	rec := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"tool":    tool,
		"args":    redactArgs(args),
		"outcome": outcome,
	}
	for k, v := range extra {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"tool":%q,"outcome":%q,"error":"marshal"}`, tool, outcome))
	}
	line := append(b, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stderr != nil {
		_, _ = a.stderr.Write(append([]byte("AUDIT "), line...))
	}
	if a.file != nil {
		if _, err := a.file.Write(line); err != nil {
			a.failures++
			if a.failures == 1 || a.failures%100 == 0 {
				fmt.Fprintf(os.Stderr, "vastai-mcp: AUDIT FILE WRITE FAILED (%d so far): %v; mutating calls are proceeding with stderr-only records\n", a.failures, err)
			}
		}
	}
}
