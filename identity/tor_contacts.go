package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var onionAddrRegex = regexp.MustCompile(`^[a-z2-7]{56}$`)

// ValidOnionAddr checks if s is a valid 56-char base32 v3 onion service ID.
func ValidOnionAddr(s string) bool {
	return onionAddrRegex.MatchString(s)
}

const torContactsFile = "tor_contacts.json"

// TorContacts maps alias names to onion addresses (56-char base32, no .onion suffix).
type TorContacts map[string]string

// TorContactsPath returns the path to ~/.holler/tor_contacts.json.
func TorContactsPath() (string, error) {
	dir, err := HollerDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, torContactsFile), nil
}

// LoadTorContacts reads tor contacts from disk. Returns empty map if file doesn't exist.
func LoadTorContacts() (TorContacts, error) {
	path, err := TorContactsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(TorContacts), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tor contacts: %w", err)
	}
	var c TorContacts
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse tor contacts: %w", err)
	}
	return c, nil
}

// SaveTorContacts writes tor contacts to disk.
func SaveTorContacts(c TorContacts) error {
	path, err := TorContactsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tor contacts: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Resolve tries to resolve an alias to an onion address.
// If the input is not a known alias, returns the input as-is (assumed to be a raw onion address).
func (c TorContacts) Resolve(aliasOrOnion string) string {
	if addr, ok := c[aliasOrOnion]; ok {
		return addr
	}
	return aliasOrOnion
}

// FindByOnion does a reverse lookup: finds the alias for a given onion address.
func (c TorContacts) FindByOnion(onionAddr string) (alias string, found bool) {
	for a, addr := range c {
		if addr == onionAddr {
			return a, true
		}
	}
	return "", false
}

// SortedAliases returns alias names sorted alphabetically.
func (c TorContacts) SortedAliases() []string {
	aliases := make([]string, 0, len(c))
	for a := range c {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	return aliases
}
