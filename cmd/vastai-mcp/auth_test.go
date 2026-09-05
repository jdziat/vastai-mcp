package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jdziat/vastai-mcp/internal/vast"
)

func TestAuthSetFromStdinAndDelete(t *testing.T) {
	vast.KeyringMockForTests()
	var out, errb bytes.Buffer
	if code := runAuth([]string{"set"}, strings.NewReader("piped-key\n"), &out, &errb); code != 0 {
		t.Fatalf("set exit %d: %s", code, errb.String())
	}
	if v, err := vast.KeyringGet(); err != nil || v != "piped-key" {
		t.Fatalf("stored %q %v", v, err)
	}
	if strings.Contains(out.String(), "piped-key") {
		t.Error("key echoed to stdout")
	}
	if code := runAuth([]string{"set"}, strings.NewReader("\n"), &out, &errb); code == 0 {
		t.Error("empty stdin accepted")
	}
	if code := runAuth([]string{"delete"}, nil, &out, &errb); code != 0 {
		t.Fatalf("delete exit %d", code)
	}
	if _, err := vast.KeyringGet(); err == nil {
		t.Error("key still present after delete")
	}
	if code := runAuth(nil, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "usage") {
		t.Error("missing subcommand should print usage and exit 2")
	}
}

func TestMaskKey(t *testing.T) {
	if m := maskKey("abcdefghijkl"); m != "abcd…ijkl" {
		t.Errorf("mask = %q", m)
	}
	if m := maskKey("short"); m != "****" {
		t.Errorf("short mask = %q", m)
	}
}
