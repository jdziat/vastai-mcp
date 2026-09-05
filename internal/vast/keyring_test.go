package vast

import (
	"errors"
	"os"
	"testing"
)

func TestKeyringRoundTripAndPrecedence(t *testing.T) {
	KeyringMockForTests()
	for _, k := range []string{"VASTAI_API_KEY", "VAST_API_KEY"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("HOME", t.TempDir()) // no CLI key files

	if _, err := KeyringGet(); !errors.Is(err, ErrKeyringNotFound) {
		t.Fatalf("empty keyring: %v", err)
	}
	if err := KeyringSet("  "); err == nil {
		t.Fatal("empty key stored")
	}
	if err := KeyringSet(" ring-key \n"); err != nil {
		t.Fatal(err)
	}
	if v, err := KeyringGet(); err != nil || v != "ring-key" {
		t.Fatalf("get = %q %v", v, err)
	}
	key, src, err := LoadAPIKey()
	if err != nil || key != "ring-key" || src != KeySourceKeyring {
		t.Fatalf("LoadAPIKey = %q %q %v", key, src, err)
	}
	t.Setenv("VASTAI_API_KEY", "env-key")
	if key, src, _ := LoadAPIKey(); key != "env-key" || src != KeySourceEnv {
		t.Fatalf("env must override keyring: %q %q", key, src)
	}
	os.Unsetenv("VASTAI_API_KEY")
	if err := KeyringDelete(); err != nil {
		t.Fatal(err)
	}
	if err := KeyringDelete(); err != nil {
		t.Fatalf("second delete must be a no-op: %v", err)
	}
	if _, _, err := LoadAPIKey(); err == nil {
		t.Fatal("expected no key after delete")
	}
}
