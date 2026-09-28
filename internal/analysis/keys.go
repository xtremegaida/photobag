package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"photobag/internal/llm"
)

// KeyEnv overrides the stored API key for every endpoint.
const KeyEnv = "PHOTOBAG_API_KEY"

// KeyStore keeps API keys per endpoint in a file in the user's
// configuration directory, so they never travel inside a bag (or its
// backups and exports).
type KeyStore struct {
	Path string
	mu   sync.Mutex
}

// DefaultKeyStorePath is <user config dir>/PhotoBag/credentials.json
// (%AppData% on Windows, ~/.config on Linux).
func DefaultKeyStorePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "PhotoBag", "credentials.json")
}

type keyFile struct {
	APIKeys map[string]string `json:"apiKeys"`
}

func (k *KeyStore) read() (keyFile, error) {
	f := keyFile{APIKeys: map[string]string{}}
	if k == nil || k.Path == "" {
		return f, nil
	}
	data, err := os.ReadFile(k.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s is unreadable: %w", k.Path, err)
	}
	if f.APIKeys == nil {
		f.APIKeys = map[string]string{}
	}
	return f, nil
}

// Key returns the API key for endpoint and whether it came from the
// environment.
func (k *KeyStore) Key(endpoint string) (key string, fromEnv bool, err error) {
	if v := os.Getenv(KeyEnv); v != "" {
		return v, true, nil
	}
	if k == nil {
		return "", false, nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	f, err := k.read()
	return f.APIKeys[llm.NormalizeEndpoint(endpoint)], false, err
}

// SetKey stores (or, when key is empty, forgets) the key for endpoint.
func (k *KeyStore) SetKey(endpoint, key string) error {
	if k == nil || k.Path == "" {
		return fmt.Errorf("no credentials file is configured")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	f, err := k.read()
	if err != nil {
		return err
	}
	ep := llm.NormalizeEndpoint(endpoint)
	if key == "" {
		delete(f.APIKeys, ep)
	} else {
		f.APIKeys[ep] = key
	}
	if err := os.MkdirAll(filepath.Dir(k.Path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	tmp := k.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.Path)
}

// KeyHint masks a key for display ("…a1b2").
func KeyHint(key string) string {
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) <= 8 {
		return "…"
	}
	return "…" + string(r[len(r)-4:])
}
