package tools

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// confirmState is what the server signs into CallToolResult.RequestState so
// that the confirmation answer the client echoes back can only be honoured
// for the same tool, the same arguments, and the same previewed price.
type confirmState struct {
	Tool        string  `json:"t"`
	ArgHash     string  `json:"a"`
	Price       float64 `json:"p"`
	PreviewHash string  `json:"v,omitempty"`
	Nonce       string  `json:"n"`
	Expires     int64   `json:"e"`
}

// stateSigner HMACs confirmState with a per-process random key and tracks
// consumed nonces so one approval authorises exactly one action.
type stateSigner struct {
	key  []byte
	now  func() time.Time
	mu   sync.Mutex
	used map[string]int64 // nonce → expiry
}

func newStateSigner() *stateSigner {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return &stateSigner{key: k, now: time.Now, used: map[string]int64{}}
}

// hashPreview binds free-form preview text into the state.
func hashPreview(preview string) string {
	sum := sha256.Sum256([]byte(preview))
	return hex.EncodeToString(sum[:])
}

const confirmTTL = 10 * time.Minute

// hashArgs canonicalises raw JSON arguments (Go marshals map keys sorted).
func hashArgs(raw any) (string, error) {
	var v any
	switch a := raw.(type) {
	case nil:
		v = map[string]any{}
	case json.RawMessage:
		if len(a) == 0 {
			v = map[string]any{}
		} else if err := json.Unmarshal(a, &v); err != nil {
			return "", err
		}
	case []byte:
		if len(a) == 0 {
			v = map[string]any{}
		} else if err := json.Unmarshal(a, &v); err != nil {
			return "", err
		}
	default:
		v = a
	}
	// The confirm flag itself must not change the identity of the request.
	// Copy before deleting so a caller-owned map is never mutated.
	if m, ok := v.(map[string]any); ok {
		c := make(map[string]any, len(m))
		for k, val := range m {
			if k != confirmKey {
				c[k] = val
			}
		}
		v = c
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (s *stateSigner) sign(st confirmState) (string, error) {
	n := make([]byte, 12)
	if _, err := rand.Read(n); err != nil {
		return "", err
	}
	st.Nonce = hex.EncodeToString(n)
	st.Expires = s.now().Add(confirmTTL).Unix()
	payload, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

var (
	errBadState  = errors.New("confirmation state is missing or does not match this request (it may have been issued by a previous server process)")
	errStateUsed = errors.New("this confirmation was already used; each approval authorises exactly one action")
)

// verify checks token against the current tool/args/price/preview and, on
// success, consumes its nonce so the same approval cannot be replayed.
func (s *stateSigner) verify(token, tool, argHash string, price float64, previewHash string) error {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return errBadState
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return errBadState
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return errBadState
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), want) {
		return errBadState
	}
	var st confirmState
	if err := json.Unmarshal(payload, &st); err != nil {
		return errBadState
	}
	if st.Tool != tool || st.ArgHash != argHash {
		return errBadState
	}
	now := s.now().Unix()
	if now > st.Expires {
		return fmt.Errorf("confirmation expired; re-run to get a fresh preview")
	}
	if st.PreviewHash != previewHash {
		return fmt.Errorf("the target changed since the preview was approved; re-run to get a fresh preview")
	}
	// Consume the nonce before the price check too: an approval whose price
	// drifted out and back must not become usable again.
	s.mu.Lock()
	for n, exp := range s.used { // sweep expired entries; the map is bounded by TTL
		if exp < now {
			delete(s.used, n)
		}
	}
	_, seen := s.used[st.Nonce]
	s.used[st.Nonce] = st.Expires
	s.mu.Unlock()
	if seen {
		return errStateUsed
	}
	if !priceMatches(st.Price, price) {
		return fmt.Errorf("the price changed since the preview was approved (was $%.4f/hr, now $%.4f/hr); re-run to get a fresh preview", st.Price, price)
	}
	return nil
}

// priceMatches allows 1% or $0.001/hr drift, whichever is larger, capped at
// $0.05/hr so an expensive offer cannot drift by dollars per day unnoticed.
func priceMatches(a, b float64) bool {
	tol := math.Min(0.05, math.Max(0.001, 0.01*math.Max(a, b)))
	return math.Abs(a-b) <= tol
}
