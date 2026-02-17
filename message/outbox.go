package message

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const outboxFile = "outbox.jsonl"

// OutboxEntry wraps an envelope with retry metadata.
type OutboxEntry struct {
	Envelope  *Envelope `json:"envelope"`
	Attempts  int       `json:"attempts"`
	NextRetry int64     `json:"next_retry"`
}

// OutboxPath returns the path to ~/.holler/outbox.jsonl.
func OutboxPath(hollerDir string) string {
	return filepath.Join(hollerDir, outboxFile)
}

// SaveToOutbox appends a new outbox entry for a failed delivery.
func SaveToOutbox(hollerDir string, env *Envelope) error {
	entry := OutboxEntry{
		Envelope:  env,
		Attempts:  0,
		NextRetry: time.Now().Add(30 * time.Second).Unix(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal outbox entry: %w", err)
	}
	path := OutboxPath(hollerDir)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open outbox: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write to outbox: %w", err)
	}
	return nil
}

// LoadOutbox reads all outbox entries from disk.
func LoadOutbox(hollerDir string) ([]OutboxEntry, error) {
	path := OutboxPath(hollerDir)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open outbox: %w", err)
	}
	defer f.Close()

	var entries []OutboxEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry OutboxEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // skip corrupt lines
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

// WriteOutbox atomically overwrites the outbox file with the given entries.
func WriteOutbox(hollerDir string, entries []OutboxEntry) error {
	path := OutboxPath(hollerDir)
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	// Write to temp file, then atomic rename
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create outbox tmp: %w", err)
	}
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("marshal outbox entry: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write outbox entry: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close outbox tmp: %w", err)
	}
	return os.Rename(tmp, path)
}

// NextBackoff returns the next retry delay based on attempt count.
// 30s → 1m → 2m → 5m → 10m (cap)
func NextBackoff(attempts int) time.Duration {
	backoffs := []time.Duration{
		30 * time.Second,
		1 * time.Minute,
		2 * time.Minute,
		5 * time.Minute,
		10 * time.Minute,
	}
	if attempts >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempts]
}

const MaxRetries = 100
