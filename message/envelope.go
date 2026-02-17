package message

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cretz/bine/torutil"
	bineed25519 "github.com/cretz/bine/torutil/ed25519"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Envelope is the JSONL message format for holler.
//
// Type can be: message, ack, ping, task-proposal, task-result,
// capability-query, status-update, or any custom string.
type Envelope struct {
	V       int               `json:"v"`
	ID      string            `json:"id"`
	From    string            `json:"from"`
	To      string            `json:"to"`
	Ts      int64             `json:"ts"`
	Type    string            `json:"type"`
	Body    string            `json:"body"`
	ReplyTo  string            `json:"reply_to,omitempty"`
	ThreadID string            `json:"thread_id,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
	Sig     string            `json:"sig"`
}

// NewEnvelope creates a new unsigned envelope with libp2p PeerIDs (clearnet mode).
func NewEnvelope(from, to peer.ID, msgType, body string) *Envelope {
	return &Envelope{
		V:    1,
		ID:   uuid.New().String(),
		From: from.String(),
		To:   to.String(),
		Ts:   time.Now().Unix(),
		Type: msgType,
		Body: body,
	}
}

// NewEnvelopeTor creates a new unsigned envelope with onion addresses (Tor mode).
func NewEnvelopeTor(fromOnion, toOnion string, msgType, body string) *Envelope {
	return &Envelope{
		V:    1,
		ID:   uuid.New().String(),
		From: fromOnion,
		To:   toOnion,
		Ts:   time.Now().Unix(),
		Type: msgType,
		Body: body,
	}
}

// signPayload returns the bytes to sign: id+from+to+ts+type+body+reply_to+meta.
func (e *Envelope) signPayload() []byte {
	payload := fmt.Sprintf("%s%s%s%d%s%s%s", e.ID, e.From, e.To, e.Ts, e.Type, e.Body, e.ReplyTo)
	if len(e.Meta) > 0 {
		if metaJSON, err := json.Marshal(e.Meta); err == nil {
			payload += string(metaJSON)
		}
	}
	return []byte(payload)
}

// Sign signs the envelope with the given private key.
func (e *Envelope) Sign(key crypto.PrivKey) error {
	sig, err := key.Sign(e.signPayload())
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}
	e.Sig = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// Verify checks the envelope signature against the sender's public key.
func (e *Envelope) Verify() (bool, error) {
	fromID, err := peer.Decode(e.From)
	if err != nil {
		return false, fmt.Errorf("decode sender peer ID: %w", err)
	}
	pubKey, err := fromID.ExtractPublicKey()
	if err != nil {
		return false, fmt.Errorf("extract public key: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(e.Sig)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}
	return pubKey.Verify(e.signPayload(), sig)
}

// SignTor signs the envelope with a bine ed25519 keypair (Tor mode).
func (e *Envelope) SignTor(kp bineed25519.KeyPair) error {
	sig := bineed25519.Sign(kp, e.signPayload())
	e.Sig = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// VerifyTor verifies the envelope signature using the sender's onion address.
// Extracts the ed25519 public key from the From field (56-char onion service ID).
func (e *Envelope) VerifyTor() (bool, error) {
	pubKey, err := PubKeyFromOnion(e.From)
	if err != nil {
		return false, fmt.Errorf("extract public key from onion address: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(e.Sig)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}
	return pubKey.Verify(e.signPayload(), sig), nil
}

// PubKeyFromOnion extracts the ed25519 public key from a 56-char onion service ID.
func PubKeyFromOnion(onionAddr string) (bineed25519.PublicKey, error) {
	return torutil.PublicKeyFromV3OnionServiceID(onionAddr)
}

// Marshal serializes the envelope to JSON bytes (single line, no trailing newline).
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope deserializes an envelope from JSON bytes.
func UnmarshalEnvelope(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return &env, nil
}
