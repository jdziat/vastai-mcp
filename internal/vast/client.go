// Package vast is a small HTTP client for the Vast.ai REST API (api/v0).
package vast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultBaseURL = "https://console.vast.ai/api/v0"
	// MaxResponseBytes caps any single API response body.
	MaxResponseBytes = 8 << 20
	maxRetries       = 3
)

// Client talks to the Vast.ai API.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// Sleep is overridable for tests.
	Sleep func(context.Context, time.Duration) error
}

// APIError is returned for non-2xx responses.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("vast.ai API error: HTTP %d: %s", e.Status, e.Body)
}

// KeySource names where the API key came from.
type KeySource string

const (
	KeySourceEnv     KeySource = "environment"
	KeySourceKeyring KeySource = "os keyring"
	KeySourceFile    KeySource = "config file"
)

// LoadAPIKey resolves the API key in this order: VASTAI_API_KEY or
// VAST_API_KEY (explicit override, also how .env is surfaced), the OS
// keyring (see `vastai-mcp auth set`), then ~/.config/vastai/vast_api_key
// or ~/.vast_api_key (written by the official CLI).
func LoadAPIKey() (string, KeySource, error) {
	for _, env := range []string{"VASTAI_API_KEY", "VAST_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, KeySourceEnv, nil
		}
	}
	if v, err := KeyringGet(); err == nil {
		return v, KeySourceKeyring, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	for _, p := range []string{
		filepath.Join(home, ".config", "vastai", "vast_api_key"),
		filepath.Join(home, ".vast_api_key"),
	} {
		b, err := os.ReadFile(p) // #nosec G304 -- fixed paths under $HOME
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			return v, KeySourceFile, nil
		}
	}
	return "", "", errors.New("no Vast.ai API key found: run `vastai-mcp auth set`, or set VASTAI_API_KEY")
}

// New creates a client. baseURL may be empty to use the default. transport
// may be nil, in which case a pinned transport is built now.
func New(apiKey, baseURL string, transport http.RoundTripper) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if transport == nil {
		transport = NewPinnedTransport(baseURL)
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 60 * time.Second, Transport: transport},
		Sleep:   sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Do performs a request against path (relative to BaseURL) with an optional
// JSON body and query params, decoding the JSON response into out (if non-nil).
// retry enables backoff on 429/502/503/504; only use it for idempotent calls.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any, retry bool) error {
	raw, err := c.DoRaw(ctx, method, path, query, body, retry)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, Truncate(string(raw), 500))
	}
	return nil
}

// DoRaw is like Do but returns the raw response body.
func (c *Client) DoRaw(ctx context.Context, method, path string, query url.Values, body any, retry bool) ([]byte, error) {
	u := c.BaseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		payload = b
	}
	attempts := 1
	if retry {
		attempts = maxRetries
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		raw, status, retryAfter, err := c.once(ctx, method, u, payload)
		if err == nil && status >= 200 && status < 300 {
			return raw, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = &APIError{Status: status, Body: Truncate(string(raw), 2000)}
		}
		if !retry || !retryable(status, err) || i == attempts-1 {
			return nil, lastErr
		}
		delay := backoff(i, retryAfter)
		if err := c.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) once(ctx context.Context, method, u string, payload []byte) ([]byte, int, time.Duration, error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, 0, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("User-Agent", "vastai-mcp")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, 0, err
	}
	var ra time.Duration
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			ra = time.Duration(n) * time.Second
		}
	}
	return raw, resp.StatusCode, ra, nil
}

func retryable(status int, err error) bool {
	if err != nil {
		return errors.Is(err, io.ErrUnexpectedEOF)
	}
	switch status {
	case 429, 502, 503, 504:
		return true
	}
	return false
}

func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > 30*time.Second {
			retryAfter = 30 * time.Second
		}
		return retryAfter
	}
	base := 500 * time.Millisecond << attempt
	jitter := time.Duration(rand.Int64N(int64(base / 2))) // #nosec G404 -- backoff jitter, not security-sensitive
	return base + jitter
}

// FetchURL GETs an absolute URL (used for result_url log/command outputs).
func (c *Client) FetchURL(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	return raw, resp.StatusCode, err
}

// Truncate shortens s to at most n bytes without splitting a UTF-8 sequence.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// ---- Typed helpers -------------------------------------------------------

// Get is a GET returning the decoded JSON as a generic value.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodGet, path, query, nil, &out, true)
	return out, err
}

// SearchDefaults controls which of the official-CLI default filters are
// applied to a search. Zero value = all defaults applied.
type SearchDefaults struct {
	SkipVerified bool // do not add verified:true
	SkipRentable bool // do not add rentable:true
	SkipRented   bool // do not add rented:false
	SkipExternal bool // do not add external:false
	SkipType     bool // do not add type:on-demand
}

// NoDefaults suppresses every default filter.
var NoDefaults = SearchDefaults{true, true, true, true, true}

// SearchOffers queries the marketplace via PUT /search/asks/. q is the
// Vast.ai query object (e.g. {"gpu_name": {"eq": "RTX 4090"}}). Defaults
// mirroring the official CLI are added for keys not already present unless
// suppressed by d. gpu_ram/cpu_ram are MB, disk_space GB, dph_total is $/hr for
// the whole offer.
func (c *Client) SearchOffers(ctx context.Context, q map[string]any, d SearchDefaults, orderBy string, limit int) ([]map[string]any, error) {
	if q == nil {
		q = map[string]any{}
	}
	setDefault := func(skip bool, k string, v any) {
		if skip {
			return
		}
		if _, ok := q[k]; !ok {
			q[k] = v
		}
	}
	setDefault(d.SkipVerified, "verified", map[string]any{"eq": true})
	setDefault(d.SkipRentable, "rentable", map[string]any{"eq": true})
	setDefault(d.SkipRented, "rented", map[string]any{"eq": false})
	setDefault(d.SkipExternal, "external", map[string]any{"eq": false})
	setDefault(d.SkipType, "type", "on-demand")
	if orderBy != "" {
		q["order"] = parseOrder(orderBy)
	}
	if limit > 0 {
		q["limit"] = limit
	}
	var out struct {
		Offers []map[string]any `json:"offers"`
	}
	err := c.Do(ctx, http.MethodPut, "/search/asks/", nil, map[string]any{"q": q}, &out, true)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(out.Offers) > limit {
		out.Offers = out.Offers[:limit]
	}
	return out.Offers, nil
}

// LookupOffer resolves a single offer by id with no default filters, so
// unverified, bid, and rented offers resolve. The API does not filter on
// "id"; ask_contract_id (which equals the offer id) is the working key.
// Returns nil, nil when the offer no longer exists.
func (c *Client) LookupOffer(ctx context.Context, id int64, bid bool) (map[string]any, error) {
	q := map[string]any{"ask_contract_id": map[string]any{"eq": id}, "type": "on-demand"}
	if bid {
		q["type"] = "bid"
	}
	offers, err := c.SearchOffers(ctx, q, NoDefaults, "", 1)
	if err != nil {
		return nil, err
	}
	if len(offers) == 0 {
		return nil, nil
	}
	return offers[0], nil
}

// parseOrder turns "dph_total,-reliability" into [["dph_total","asc"],["reliability","desc"]].
func parseOrder(s string) [][]string {
	var order [][]string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		dir := "asc"
		if strings.HasPrefix(f, "-") {
			dir, f = "desc", f[1:]
		} else if strings.HasPrefix(f, "+") {
			f = f[1:]
		}
		order = append(order, []string{f, dir})
	}
	return order
}

// ListInstances returns the current user's instances.
func (c *Client) ListInstances(ctx context.Context) ([]map[string]any, error) {
	var out struct {
		Instances []map[string]any `json:"instances"`
	}
	err := c.Do(ctx, http.MethodGet, "/instances/", url.Values{"owner": {"me"}}, nil, &out, true)
	return out.Instances, err
}

// ShowInstance returns one instance, or nil, nil if it does not exist
// (the API returns {"instances": null}).
func (c *Client) ShowInstance(ctx context.Context, id int64) (map[string]any, error) {
	var out struct {
		Instances map[string]any `json:"instances"`
	}
	err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/instances/%d/", id), url.Values{"owner": {"me"}}, nil, &out, true)
	return out.Instances, err
}

// CreateInstanceParams mirrors the fields accepted by PUT /asks/{id}/.
type CreateInstanceParams struct {
	Image          string
	Disk           float64
	Label          string
	OnStart        string
	RunType        string
	Env            map[string]string
	ImageLogin     string
	Price          float64 // bid price for interruptible
	TemplateHashID string
	CancelUnavail  bool
}

// CreateInstanceResult is the response of PUT /asks/{id}/.
type CreateInstanceResult struct {
	Success     bool   `json:"success"`
	NewContract int64  `json:"new_contract"`
	Msg         string `json:"msg"`
}

// CreateInstance rents offer askID. Never retried.
func (c *Client) CreateInstance(ctx context.Context, askID int64, p CreateInstanceParams) (*CreateInstanceResult, error) {
	body := map[string]any{"client_id": "me", "image": p.Image}
	if p.Disk > 0 {
		body["disk"] = p.Disk
	}
	if p.Label != "" {
		body["label"] = p.Label
	}
	if p.OnStart != "" {
		body["onstart"] = p.OnStart
	}
	if p.RunType != "" {
		body["runtype"] = p.RunType
	}
	if len(p.Env) > 0 {
		body["env"] = p.Env
	}
	if p.ImageLogin != "" {
		body["image_login"] = p.ImageLogin
	}
	if p.Price > 0 {
		body["price"] = p.Price
	}
	if p.TemplateHashID != "" {
		body["template_hash_id"] = p.TemplateHashID
	}
	if p.CancelUnavail {
		body["cancel_unavail"] = true
	}
	var out CreateInstanceResult
	if err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/asks/%d/", askID), nil, body, &out, false); err != nil {
		return nil, err
	}
	if !out.Success {
		return &out, fmt.Errorf("create instance failed: %s", out.Msg)
	}
	return &out, nil
}

// DestroyInstance deletes an instance. Never retried.
func (c *Client) DestroyInstance(ctx context.Context, id int64) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("/instances/%d/", id), nil, nil, &out, false)
	return out, err
}

// SetInstanceState sets "running" or "stopped".
func (c *Client) SetInstanceState(ctx context.Context, id int64, state string) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/instances/%d/", id), nil, map[string]any{"state": state}, &out, false)
	return out, err
}

// LabelInstance sets a label.
func (c *Client) LabelInstance(ctx context.Context, id int64, label string) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/instances/%d/", id), nil, map[string]any{"label": label}, &out, false)
	return out, err
}

// RebootInstance restarts the container without losing data.
func (c *Client) RebootInstance(ctx context.Context, id int64) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/instances/reboot/%d/", id), nil, map[string]any{}, &out, false)
	return out, err
}

// ShowUser returns the current account.
func (c *Client) ShowUser(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.Do(ctx, http.MethodGet, "/users/current/", nil, nil, &out, true)
	return out, err
}

// resultURLResponse is the shape returned by async log/command endpoints.
type resultURLResponse struct {
	Success   bool   `json:"success"`
	ResultURL string `json:"result_url"`
	Msg       string `json:"msg"`
}

// pollResult fetches a result_url until it returns 200 or the context ends.
func (c *Client) pollResult(ctx context.Context, resultURL string, maxWait time.Duration) (string, error) {
	deadline := time.Now().Add(maxWait)
	for {
		raw, status, err := c.FetchURL(ctx, resultURL)
		if err != nil {
			return "", err
		}
		if status == http.StatusOK {
			return string(raw), nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for result (last HTTP %d)", status)
		}
		if err := c.Sleep(ctx, 1500*time.Millisecond); err != nil {
			return "", err
		}
	}
}

// InstanceLogs requests and returns the container logs.
func (c *Client) InstanceLogs(ctx context.Context, id int64, tail int, filter string) (string, error) {
	body := map[string]any{}
	if tail > 0 {
		body["tail"] = strconv.Itoa(tail)
	}
	if filter != "" {
		body["filter"] = filter
	}
	var r resultURLResponse
	if err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/instances/request_logs/%d/", id), nil, body, &r, true); err != nil {
		return "", err
	}
	if !r.Success || r.ResultURL == "" {
		return "", fmt.Errorf("log request failed: %s", r.Msg)
	}
	return c.pollResult(ctx, r.ResultURL, 45*time.Second)
}

// Execute runs a command inside the instance via the Vast.ai command API.
// Callers must validate command against the allowlist first.
func (c *Client) Execute(ctx context.Context, id int64, command string) (string, error) {
	var r resultURLResponse
	if err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/instances/command/%d/", id), nil, map[string]any{"command": command}, &r, false); err != nil {
		return "", err
	}
	if !r.Success || r.ResultURL == "" {
		return "", fmt.Errorf("execute failed: %s", r.Msg)
	}
	return c.pollResult(ctx, r.ResultURL, 45*time.Second)
}

// ListSSHKeys lists the account's SSH keys.
func (c *Client) ListSSHKeys(ctx context.Context) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodGet, "/ssh/", nil, nil, &out, true)
	return out, err
}

// CreateSSHKey adds a public key to the account.
func (c *Client) CreateSSHKey(ctx context.Context, pubKey string) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodPost, "/ssh/", nil, map[string]any{"ssh_key": pubKey}, &out, false)
	return out, err
}

// AttachSSHKey adds a public key to a running instance.
func (c *Client) AttachSSHKey(ctx context.Context, id int64, pubKey string) (any, error) {
	var out any
	err := c.Do(ctx, http.MethodPost, fmt.Sprintf("/instances/%d/ssh/", id), nil, map[string]any{"ssh_key": pubKey}, &out, false)
	return out, err
}

// SearchTemplates lists templates matching filters. Booleans/numbers take the
// {"eq": v} form; "name" is an exact-match string.
func (c *Client) SearchTemplates(ctx context.Context, filters map[string]any) (any, error) {
	if filters == nil {
		filters = map[string]any{}
	}
	fb, err := json.Marshal(filters)
	if err != nil {
		return nil, fmt.Errorf("encode filters: %w", err)
	}
	q := url.Values{}
	q.Set("select_cols", `["*"]`)
	q.Set("select_filters", string(fb))
	return c.Get(ctx, "/template/", q)
}
