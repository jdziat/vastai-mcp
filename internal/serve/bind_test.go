package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckBind(t *testing.T) {
	cases := []struct {
		addr                     string
		token, tls, insecure, ok bool
		loopback                 bool
	}{
		{"127.0.0.1:1", false, false, false, true, true},
		{"localhost:1", false, false, false, true, true},
		{"[::1]:1", false, false, false, true, true},
		{":8080", false, false, false, false, false},
		{":8080", true, false, false, false, false},
		{":8080", true, false, true, true, false},
		{":8080", true, true, false, true, false},
		{"0.0.0.0:1", true, false, false, false, false},
		{"192.168.1.5:1", false, true, false, false, false},
		{"bogus", true, true, false, false, false},
	}
	for _, c := range cases {
		p, err := CheckBind(c.addr, c.token, c.tls, c.insecure)
		if (err == nil) != c.ok {
			t.Errorf("CheckBind(%q,token=%v,tls=%v,insecure=%v) err=%v, want ok=%v", c.addr, c.token, c.tls, c.insecure, err, c.ok)
		}
		if err == nil && p.Loopback != c.loopback {
			t.Errorf("%q loopback=%v", c.addr, p.Loopback)
		}
	}
}

func TestBearerAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := BearerAuth("s3cret", ok)
	for hdr, want := range map[string]int{"": 401, "Bearer wrong": 401, "Basic s3cret": 401, "Bearer s3cret": 204, "Bearer  s3cret ": 204} {
		req := httptest.NewRequest("POST", "/", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != want {
			t.Errorf("Authorization %q → %d, want %d", hdr, rr.Code, want)
		}
	}
	rr := httptest.NewRecorder()
	BearerAuth("", ok).ServeHTTP(rr, httptest.NewRequest("POST", "/", nil))
	if rr.Code != 204 {
		t.Error("empty token must disable auth (loopback only)")
	}
}

func TestLoadToken(t *testing.T) {
	t.Setenv("VASTAI_MCP_TOKEN", "")
	os.Unsetenv("VASTAI_MCP_TOKEN")
	if tok, err := LoadToken(""); err != nil || tok != "" {
		t.Fatalf("no token: %q %v", tok, err)
	}
	p := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(p, []byte(" filetoken\n"), 0o600)
	if tok, err := LoadToken(p); err != nil || tok != "filetoken" {
		t.Fatalf("file token: %q %v", tok, err)
	}
	t.Setenv("VASTAI_MCP_TOKEN", "envtoken")
	if tok, _ := LoadToken(p); tok != "envtoken" {
		t.Fatalf("env should win: %q", tok)
	}
	os.WriteFile(p, []byte(""), 0o600)
	os.Unsetenv("VASTAI_MCP_TOKEN")
	if _, err := LoadToken(p); err == nil {
		t.Fatal("empty file must error")
	}
}
