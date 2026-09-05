package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/jdziat/vastai-mcp/internal/vast"
)

const authUsage = `usage: vastai-mcp auth <command>

  set [-no-verify]   store the API key in the OS keyring (prompts, or reads
                     stdin when piped); the key is checked against the API first
  status             report where the key would be loaded from and whether it works
  delete             remove the key from the OS keyring

The keyring is macOS Keychain, Windows Credential Manager, or the Linux
Secret Service (GNOME Keyring / KWallet via D-Bus).`

// runAuth handles `vastai-mcp auth ...`. Returns an exit code.
func runAuth(args []string, stdin io.Reader, stdout, stderr io.Writer, baseURL string, transport http.RoundTripper) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, authUsage)
		return 2
	}
	switch args[0] {
	case "set":
		verify := true
		for _, f := range args[1:] {
			if f == "-no-verify" || f == "--no-verify" {
				verify = false
			}
		}
		key, err := readKey(stdin, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "vastai-mcp auth set: %v\n", err)
			return 1
		}
		if verify {
			c := vast.New(key, baseURL, transport)
			if _, err := c.ShowUser(context.Background()); err != nil {
				fmt.Fprintf(stderr, "vastai-mcp auth set: key rejected by Vast.ai, not stored (%v); use -no-verify to store anyway\n", err)
				return 1
			}
		}
		if err := vast.KeyringSet(key); err != nil {
			fmt.Fprintf(stderr, "vastai-mcp auth set: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "stored API key in the OS keyring (%s/%s)\n", vast.KeyringService, vast.KeyringUser)
		warnShadowed(stderr)
		return 0
	case "delete":
		if err := vast.KeyringDelete(); err != nil {
			fmt.Fprintf(stderr, "vastai-mcp auth delete: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "removed API key from the OS keyring")
		return 0
	case "status":
		key, src, err := vast.LoadAPIKey()
		if err != nil {
			fmt.Fprintf(stderr, "vastai-mcp auth status: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "key source: %s (%s)\n", src, maskKey(key))
		c := vast.New(key, baseURL, transport)
		u, err := c.ShowUser(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "key rejected by Vast.ai: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "key valid: credit $%.2f\n", asFloat(u["credit"]))
		return 0
	default:
		fmt.Fprintln(stderr, authUsage)
		return 2
	}
}

// warnShadowed tells the user when a higher-precedence source will hide the
// key they just stored.
func warnShadowed(stderr io.Writer) {
	for _, k := range []string{"VASTAI_API_KEY", "VAST_API_KEY"} {
		if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
			fmt.Fprintf(stderr, "warning: %s is set (or provided by ./.env) and takes precedence over the keyring\n", k)
		}
	}
}

func readKey(stdin io.Reader, stderr io.Writer) (string, error) {
	if stdin == nil {
		return "", errors.New("no input available")
	}
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(stderr, "Vast.ai API key (input hidden): ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	key := strings.TrimSpace(line)
	if key == "" {
		return "", errors.New("no key provided on stdin")
	}
	return key, nil
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func asFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}
