package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cretz/bine/control"
	"github.com/cretz/bine/torutil"
	bineed25519 "github.com/cretz/bine/torutil/ed25519"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	defaultDir = ".holler"
	keyFile    = "key.bin"
)

// DirOverride is set by the --dir flag. Empty means use default ~/.holler/.
var DirOverride string

// HollerDir returns the holler data directory, creating it if needed.
func HollerDir() (string, error) {
	dir := DirOverride
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		dir = filepath.Join(home, defaultDir)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create holler dir: %w", err)
	}
	return dir, nil
}

// KeyPath returns the path to key.bin inside the holler dir.
func KeyPath() (string, error) {
	dir, err := HollerDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keyFile), nil
}

// GenerateKey creates a new Ed25519 keypair and returns the libp2p private key.
func GenerateKey() (crypto.PrivKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	key, _, err := crypto.KeyPairFromStdKey(&priv)
	if err != nil {
		return nil, fmt.Errorf("convert to libp2p key: %w", err)
	}
	return key, nil
}

// SaveKey marshals and writes the private key to path with 0600 permissions.
func SaveKey(path string, key crypto.PrivKey) error {
	raw, err := crypto.MarshalPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

// LoadKey reads and unmarshals the private key from path.
func LoadKey(path string) (crypto.PrivKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	key, err := crypto.UnmarshalPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal key: %w", err)
	}
	return key, nil
}

// LoadOrFail loads the key from the default path, printing a helpful error if not found.
func LoadOrFail() (crypto.PrivKey, error) {
	path, err := KeyPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("no identity found — run 'holler init' first")
	}
	return LoadKey(path)
}

// PeerIDFromKey derives a libp2p PeerID from a private key.
func PeerIDFromKey(key crypto.PrivKey) (peer.ID, error) {
	return peer.IDFromPrivateKey(key)
}

// KeyExists returns true if the key file exists at the default path.
func KeyExists() bool {
	path, err := KeyPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// OnionKeyPairFromBine extracts the bine ed25519 KeyPair from a control.ED25519Key.
func OnionKeyPairFromBine(key *control.ED25519Key) bineed25519.KeyPair {
	return key.KeyPair
}

// OnionAddrFromKey computes the 56-char lowercase onion service ID from a bine key.
func OnionAddrFromKey(key *control.ED25519Key) string {
	return strings.ToLower(torutil.OnionServiceIDFromPrivateKey(key.KeyPair))
}
