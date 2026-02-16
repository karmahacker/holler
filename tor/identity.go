package tor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/1F47E/holler/identity"
)

const onionFile = "onion.txt"

// SaveOnionAddress saves the .onion address to the holler directory
func SaveOnionAddress(onionAddr string) error {
	hollerDir, err := identity.HollerDir()
	if err != nil {
		return err
	}

	onionPath := filepath.Join(hollerDir, onionFile)
	if err := os.WriteFile(onionPath, []byte(onionAddr), 0600); err != nil {
		return fmt.Errorf("save onion address: %w", err)
	}

	return nil
}

// LoadOnionAddress loads the .onion address from the holler directory
func LoadOnionAddress() (string, error) {
	hollerDir, err := identity.HollerDir()
	if err != nil {
		return "", err
	}

	onionPath := filepath.Join(hollerDir, onionFile)
	data, err := os.ReadFile(onionPath)
	if err != nil {
		return "", fmt.Errorf("load onion address: %w", err)
	}

	onion := strings.TrimSpace(string(data))
	if !strings.HasSuffix(onion, ".onion") {
		return "", fmt.Errorf("invalid onion address: %s", onion)
	}

	return onion, nil
}

// OnionExists checks if an .onion address is already stored
func OnionExists() bool {
	hollerDir, err := identity.HollerDir()
	if err != nil {
		return false
	}

	onionPath := filepath.Join(hollerDir, onionFile)
	_, err = os.Stat(onionPath)
	return err == nil
}

// GetOrCreateOnionAddress gets existing or creates new .onion address
func GetOrCreateOnionAddress() (string, error) {
	if OnionExists() {
		return LoadOnionAddress()
	}

	onion, err := SetupHiddenService()
	if err != nil {
		return "", err
	}

	if err := SaveOnionAddress(onion); err != nil {
		return "", err
	}

	return onion, nil
}