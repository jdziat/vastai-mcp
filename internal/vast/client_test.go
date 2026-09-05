package vast

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("test-key", srv.URL, nil)
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c, srv
}

func TestDoSendsAuthAndDecodes(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/users/current/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"credit": 1.5}`))
	})
	u, err := c.ShowUser(context.Background())
	if err != nil || u["credit"] != 1.5 {
		t.Fatalf("got %v, %v", u, err)
	}
}

func TestDoAPIErrorTruncated(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(strings.Repeat("x", 5000)))
	})
	_, err := c.ShowUser(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 400 || len(apiErr.Body) > 2010 {
		t.Fatalf("err = %v", err)
	}
}

func TestDoDecodeError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) })
	if _, err := c.ShowUser(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	n := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"offers": []}`))
	})
	if _, err := c.SearchOffers(context.Background(), nil, SearchDefaults{}, "", 1); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("attempts = %d", n)
	}
}

func TestNoRetryForCreate(t *testing.T) {
	n := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { n++; w.WriteHeader(503) })
	if _, err := c.CreateInstance(context.Background(), 1, CreateInstanceParams{Image: "x"}); err == nil {
		t.Fatal("expected error")
	}
	if n != 1 {
		t.Fatalf("create must never retry; attempts = %d", n)
	}
}

func TestSearchOffersDefaultsAndSkips(t *testing.T) {
	var got map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		got = body["q"].(map[string]any)
		w.Write([]byte(`{"offers": [{"id":1},{"id":2},{"id":3}]}`))
	})
	offers, err := c.SearchOffers(context.Background(), map[string]any{"gpu_name": map[string]any{"eq": "RTX 4090"}}, SearchDefaults{}, "-dph_total", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"verified", "rentable", "rented", "external", "type", "order", "limit"} {
		if _, ok := got[k]; !ok {
			t.Errorf("default %q missing from query %v", k, got)
		}
	}
	if len(offers) != 2 {
		t.Errorf("client-side limit not applied: %d", len(offers))
	}
	if _, err := c.SearchOffers(context.Background(), map[string]any{"type": "bid"}, SearchDefaults{SkipVerified: true, SkipRented: true}, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["verified"]; ok {
		t.Error("SkipVerified did not suppress verified")
	}
	if _, ok := got["rented"]; ok {
		t.Error("SkipRented did not suppress rented")
	}
	if got["type"] != "bid" {
		t.Error("caller-supplied type overwritten")
	}
}

func TestLookupOfferUsesAskContractIDNoDefaults(t *testing.T) {
	var got map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		got = body["q"].(map[string]any)
		w.Write([]byte(`{"offers": [{"id": 42, "dph_total": 0.5}]}`))
	})
	o, err := c.LookupOffer(context.Background(), 42, true, 25)
	if err != nil || o["id"] != float64(42) {
		t.Fatalf("got %v %v", o, err)
	}
	if _, ok := got["verified"]; ok {
		t.Error("lookup must not apply verified default")
	}
	if got["allocated_storage"] != float64(25) {
		t.Errorf("allocated_storage must be passed so dph_total prices the intended disk: %v", got)
	}
	if _, ok := got["ask_contract_id"]; !ok {
		t.Errorf("lookup must filter on ask_contract_id: %v", got)
	}
	if got["type"] != "bid" {
		t.Errorf("bid lookup type = %v", got["type"])
	}
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"offers": []}`)) })
	if o, err := c2.LookupOffer(context.Background(), 1, false, 0); err != nil || o != nil {
		t.Fatalf("missing offer should be nil,nil: %v %v", o, err)
	}
}

func TestShowInstanceNull(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"instances": null}`)) })
	inst, err := c.ShowInstance(context.Background(), 1)
	if err != nil || inst != nil {
		t.Fatalf("got %v %v", inst, err)
	}
}

func TestPollResult(t *testing.T) {
	n := 0
	var srv *httptest.Server
	c, s := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/result" {
			n++
			if n < 3 {
				w.WriteHeader(404)
				return
			}
			w.Write([]byte("log line"))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result_url": srv.URL + "/result"})
	})
	srv = s
	out, err := c.InstanceLogs(context.Background(), 1, 10, "")
	if err != nil || out != "log line" {
		t.Fatalf("got %q %v", out, err)
	}
	if n != 3 {
		t.Errorf("polls = %d", n)
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	s := "héllo wörld"
	out := Truncate(s, 2) // 'h' + first byte of 'é'
	if !strings.HasPrefix(out, "h") || strings.ContainsRune(out, '�') || len(out) > 2+len("…") {
		t.Fatalf("Truncate = %q", out)
	}
	if Truncate("abc", 10) != "abc" {
		t.Fatal("short strings must be unchanged")
	}
}

func TestParseOrder(t *testing.T) {
	got := parseOrder("dph_total, -reliability2,+gpu_ram")
	want := [][]string{{"dph_total", "asc"}, {"reliability2", "desc"}, {"gpu_ram", "asc"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOrder = %v, want %v", got, want)
	}
}

// ---- dotenv ---------------------------------------------------------------

func TestLoadDotEnvAllowlist(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := "# comment\nexport VASTAI_API_KEY=\"from-dotenv\"\nHTTPS_PROXY=http://evil:1\nSSL_CERT_FILE=/tmp/evil.pem\nVASTAI_BASE_URL=https://evil.example\nVAST_API_KEY=preset-wins\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"VASTAI_API_KEY", "HTTPS_PROXY", "SSL_CERT_FILE", "VASTAI_BASE_URL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("VAST_API_KEY", "preset")
	res, err := LoadDotEnv(p)
	if err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("VASTAI_API_KEY"); v != "from-dotenv" {
		t.Errorf("VASTAI_API_KEY = %q", v)
	}
	for _, k := range []string{"HTTPS_PROXY", "SSL_CERT_FILE", "VASTAI_BASE_URL"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s must not be set from .env (got %q)", k, v)
		}
	}
	if v := os.Getenv("VAST_API_KEY"); v != "preset" {
		t.Errorf("preset env must win, got %q", v)
	}
	if !reflect.DeepEqual(res.Skipped, []string{"HTTPS_PROXY", "SSL_CERT_FILE", "VASTAI_BASE_URL"}) {
		t.Errorf("skipped = %v", res.Skipped)
	}
	if res.Warning != "" {
		t.Errorf("unexpected warning for 0600 file: %s", res.Warning)
	}
	os.Chmod(p, 0o644)
	res, _ = LoadDotEnv(p)
	if res.Warning == "" {
		t.Error("expected permissions warning for 0644")
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	res, err := LoadDotEnv(filepath.Join(t.TempDir(), "nope"))
	if err != nil || res.Path != "" {
		t.Fatalf("missing file should be a no-op: %v %v", res, err)
	}
}

func TestPinnedTransportIgnoresLateProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	os.Unsetenv("HTTPS_PROXY")
	tr, err := NewPinnedTransport("https://example.invalid")
	if err != nil {
		t.Skip("no system pool on this platform")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	req, _ := http.NewRequest("GET", "https://example.invalid/x", nil)
	u, perr := tr.Proxy(req)
	if perr != nil {
		t.Fatal(perr)
	}
	if u != nil {
		t.Fatalf("proxy set after pinning must be ignored, got %v", u)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Skip("system cert pool unavailable on this platform")
	}
}

// ---- fixtures: pin the recorded API contract ------------------------------

type fixture struct {
	Meta struct {
		Endpoint   string `json:"endpoint"`
		Method     string `json:"method"`
		RecordedAt string `json:"recorded_at"`
	} `json:"_meta"`
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.Meta.Endpoint == "" || f.Meta.Method == "" || f.Meta.RecordedAt == "" {
		t.Fatalf("fixture %s lacks a provenance _meta header (endpoint/method/recorded_at); fixtures must be recorded from the live API, not hand-written", name)
	}
	return b
}

func TestFixturesHaveProvenance(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no fixtures")
	}
	for _, e := range entries {
		loadFixture(t, e.Name())
	}
}

func TestFixtureSearchAsksUnitsAndShape(t *testing.T) {
	var d struct {
		Offers []map[string]any `json:"offers"`
	}
	json.Unmarshal(loadFixture(t, "search_asks.json"), &d)
	if len(d.Offers) != 2 {
		t.Fatalf("limit=2 was recorded; offers = %d", len(d.Offers))
	}
	o := d.Offers[0]
	// gpu_ram is MB: a real GPU has >= 4 GB = 4096 MB and no GPU has >= 4096 GB.
	if r := o["gpu_ram"].(float64); r < 4096 || r > 1_000_000 {
		t.Errorf("gpu_ram %v is not MB", r)
	}
	if r := o["cpu_ram"].(float64); r < 1024 {
		t.Errorf("cpu_ram %v is not MB", r)
	}
	if r := o["disk_space"].(float64); r > 100_000 {
		t.Errorf("disk_space %v is not GB", r)
	}
	for _, k := range []string{"id", "ask_contract_id", "dph_total", "storage_cost", "min_bid", "num_gpus", "verification", "rentable", "rented"} {
		if _, ok := o[k]; !ok {
			t.Errorf("offer lacks %q", k)
		}
	}
	if o["id"] != o["ask_contract_id"] {
		t.Error("LookupOffer relies on ask_contract_id == id")
	}
}

func TestFixtureLookupOfferResolves(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write(loadFixture(t, "lookup_offer.json")) })
	o, err := c.LookupOffer(context.Background(), 45669396, false, 10)
	if err != nil || o == nil || o["id"] != float64(45669396) {
		t.Fatalf("got %v %v", o, err)
	}
}

func TestFixtureInstanceMissingIsNull(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write(loadFixture(t, "instance_missing.json")) })
	inst, err := c.ShowInstance(context.Background(), 1)
	if err != nil || inst != nil {
		t.Fatalf("got %v %v", inst, err)
	}
}

func TestFixtureTemplatesShape(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("select_filters") == "" {
			t.Error("select_filters missing")
		}
		w.Write(loadFixture(t, "template.json"))
	})
	res, err := c.SearchTemplates(context.Background(), map[string]any{"recommended": map[string]any{"eq": true}})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	ts := m["templates"].([]any)
	if len(ts) == 0 || ts[0].(map[string]any)["hash_id"] == nil {
		t.Fatalf("templates shape unexpected: %v", m)
	}
}

func TestFixtureSSHKeysIsArray(t *testing.T) {
	var f struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(loadFixture(t, "ssh.json"), &f)
	if len(f.Data) == 0 || f.Data[0]["public_key"] == nil {
		t.Fatal("ssh fixture shape")
	}
}

func TestValidateResultURL(t *testing.T) {
	c := New("k", "https://console.vast.ai/api/v0", http.DefaultTransport)
	ok := []string{"https://s3.amazonaws.com/public.vast.ai/x", "https://console.vast.ai/api/v0/r", "https://logs.vast.ai/a"}
	bad := []string{"http://s3.amazonaws.com/x", "https://169.254.169.254/latest/meta-data/", "https://evil.example.com/x", "https://amazonaws.com.evil.example/x", "https://[::1]/x", "ftp://vast.ai/x"}
	for _, u := range ok {
		if err := c.ValidateResultURL(u); err != nil {
			t.Errorf("%s rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := c.ValidateResultURL(u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	if _, _, err := c.FetchURL(context.Background(), "https://169.254.169.254/"); err == nil {
		t.Error("FetchURL must refuse link-local")
	}
	// An http base (test stubs only) may serve http results on its own host, nothing else.
	local := New("k", "http://127.0.0.1:4321", http.DefaultTransport)
	if err := local.ValidateResultURL("http://127.0.0.1:4321/result"); err != nil {
		t.Errorf("same-host http result on http base rejected: %v", err)
	}
	if err := local.ValidateResultURL("http://127.0.0.1:9/other"); err == nil {
		t.Error("different host:port http result accepted")
	}
	if err := local.ValidateResultURL("http://169.254.169.254/"); err == nil {
		t.Error("link-local accepted on http base")
	}
}

func TestPinnedTransportFailsClosedComment(t *testing.T) {
	tr, err := NewPinnedTransport("https://example.invalid")
	if err != nil {
		t.Skip("no system pool on this platform")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil || tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("pinned transport must carry RootCAs and TLS >= 1.2")
	}
}

func TestFixtureStoppedInstancePriceIncludesStorage(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write(loadFixture(t, "instance_stopped.json")) })
	inst, err := c.ShowInstance(context.Background(), 1)
	if err != nil || inst == nil {
		t.Fatal(err)
	}
	base, total, st := inst["dph_base"].(float64), inst["dph_total"].(float64), inst["storage_total_cost"].(float64)
	if inst["cur_state"] != "stopped" {
		t.Fatal("fixture must be a stopped instance")
	}
	if d := total - (base + st); d > 1e-9 || d < -1e-9 {
		t.Fatalf("stopped instance dph_total %v != dph_base %v + storage %v", total, base, st)
	}
	if total < base {
		t.Fatal("stopped instance must still report the full running rate")
	}
}
