package vast

import (
	"errors"
	"strings"

	"github.com/zalando/go-keyring"
)

// Keyring identifiers. The OS keyring entry is looked up by service + user.
const (
	KeyringService = "vastai-mcp"
	KeyringUser    = "api_key"
)

// ErrKeyringNotFound is returned when no key is stored.
var ErrKeyringNotFound = errors.New("no Vast.ai API key in the OS keyring")

// KeyringGet returns the stored API key.
func KeyringGet() (string, error) {
	v, err := keyring.Get(KeyringService, KeyringUser)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrKeyringNotFound
		}
		return "", err
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ErrKeyringNotFound
	}
	return v, nil
}

// KeyringSet stores the API key.
func KeyringSet(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("refusing to store an empty key")
	}
	return keyring.Set(KeyringService, KeyringUser, key)
}

// KeyringDelete removes the stored key; missing is not an error.
func KeyringDelete() error {
	err := keyring.Delete(KeyringService, KeyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// KeyringMockForTests swaps in an in-memory keyring.
func KeyringMockForTests() { keyring.MockInit() }
