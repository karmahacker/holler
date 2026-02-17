package node

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cretz/bine/control"
	"github.com/cretz/bine/torutil/ed25519"
)

const torKeyFile = "tor_key"

// LoadOrCreateOnionKey loads an existing onion service ed25519 key from the holler
// data directory, or generates and saves a new one. Returns the key wrapped for
// use with the Tor control protocol.
func LoadOrCreateOnionKey(hollerDir string) (*control.ED25519Key, error) {
	path := filepath.Join(hollerDir, torKeyFile)

	if data, err := os.ReadFile(path); err == nil {
		// Existing key — 64 bytes of bine ed25519 private key
		if len(data) != 64 {
			return nil, fmt.Errorf("corrupt tor_key: expected 64 bytes, got %d", len(data))
		}
		kp := ed25519.PrivateKey(data).KeyPair()
		logf("tor: loaded onion key from %s", path)
		return &control.ED25519Key{KeyPair: kp}, nil
	}

	// Generate new key
	kp, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("generate onion key: %w", err)
	}

	if err := os.WriteFile(path, kp.PrivateKey(), 0600); err != nil {
		return nil, fmt.Errorf("save onion key: %w", err)
	}
	logf("tor: generated new onion key at %s", path)
	return &control.ED25519Key{KeyPair: kp}, nil
}
