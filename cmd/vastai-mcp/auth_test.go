package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jdziat/vastai-mcp/internal/vast"
)

func stubAPI(t *testing.T, acceptKey string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+acceptKey {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"credit": 5}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthSetFromStdinAndDelete(t *testing.T) {
	vast.KeyringMockForTests()
	srv := stubAPI(t, "piped-key")
	var out, errb bytes.Buffer
	if code := runAuth([]string{"set"}, strings.NewReader("wrong-key\n"), &out, &errb, srv.URL, http.DefaultTransport); code == 0 {
		t.Fatal("rejected key was stored")
	}
	if _, err := vast.KeyringGet(); err == nil {
		t.Fatal("rejected key must not be stored")
	}
	if code := runAuth([]string{"set", "-no-verify"}, strings.NewReader("unchecked\n"), &out, &errb, srv.URL, http.DefaultTransport); code != 0 {
		t.Fatalf("-no-verify exit %d: %s", code, errb.String())
	}
	if code := runAuth([]string{"set"}, strings.NewReader("piped-key\n"), &out, &errb, srv.URL, http.DefaultTransport); code != 0 {
		t.Fatalf("set exit %d: %s", code, errb.String())
	}
	if v, err := vast.KeyringGet(); err != nil || v != "piped-key" {
		t.Fatalf("stored %q %v", v, err)
	}
	if strings.Contains(out.String(), "piped-key") {
		t.Error("key echoed to stdout")
	}
	t.Setenv("VASTAI_API_KEY", "env-key")
	errb.Reset()
	if code := runAuth([]string{"set", "-no-verify"}, strings.NewReader("piped-key\n"), &out, &errb, srv.URL, http.DefaultTransport); code != 0 || !strings.Contains(errb.String(), "takes precedence") {
		t.Errorf("shadowing warning missing: %s", errb.String())
	}
	os.Unsetenv("VASTAI_API_KEY")
	if code := runAuth([]string{"set"}, strings.NewReader("\n"), &out, &errb, srv.URL, http.DefaultTransport); code == 0 {
		t.Error("empty stdin accepted")
	}
	if code := runAuth([]string{"set"}, nil, &out, &errb, srv.URL, http.DefaultTransport); code == 0 {
		t.Error("nil stdin accepted")
	}
	if code := runAuth([]string{"delete"}, nil, &out, &errb, srv.URL, http.DefaultTransport); code != 0 {
		t.Fatalf("delete exit %d", code)
	}
	if _, err := vast.KeyringGet(); err == nil {
		t.Error("key still present after delete")
	}
	if code := runAuth(nil, nil, &out, &errb, srv.URL, http.DefaultTransport); code != 2 || !strings.Contains(errb.String(), "usage") {
		t.Error("missing subcommand should print usage and exit 2")
	}
}

func TestCheckBaseURL(t *testing.T) {
	t.Setenv("VASTAI_ALLOW_INSECURE_BASE_URL", "")
	os.Unsetenv("VASTAI_ALLOW_INSECURE_BASE_URL")
	if err := checkBaseURL("http://evil.example"); err == nil {
		t.Fatal("http base URL accepted")
	}
	if err := checkBaseURL("https://console.vast.ai/api/v0"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VASTAI_ALLOW_INSECURE_BASE_URL", "1")
	if err := checkBaseURL("http://127.0.0.1:1"); err != nil {
		t.Fatal("escape hatch not honoured")
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
