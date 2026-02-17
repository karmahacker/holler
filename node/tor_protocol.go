package node

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	bineed25519 "github.com/cretz/bine/torutil/ed25519"

	"github.com/1F47E/holler/message"
)

const (
	maxMessageSize = 1 << 20 // 1MB
	readTimeout    = 30 * time.Second
	writeTimeout   = 10 * time.Second
)

// SendTor sends an envelope over a raw TCP connection using length-prefixed framing.
// Format: [4 bytes big-endian length][JSON payload]
func SendTor(conn net.Conn, env *message.Envelope) error {
	data, err := env.Marshal()
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if len(data) > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max %d)", len(data), maxMessageSize)
	}

	conn.SetWriteDeadline(time.Now().Add(writeTimeout))

	// Write length prefix
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	// Write payload
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

// RecvTor reads an envelope from a raw TCP connection using length-prefixed framing.
func RecvTor(conn net.Conn) (*message.Envelope, error) {
	conn.SetReadDeadline(time.Now().Add(readTimeout))

	// Read length prefix
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	if msgLen == 0 || msgLen > maxMessageSize {
		return nil, fmt.Errorf("invalid message length: %d", msgLen)
	}

	// Read payload
	payload := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	return message.UnmarshalEnvelope(payload)
}

// HandleTorConnections accepts incoming TCP connections on the TorNode message port
// and dispatches verified messages to the handler. Runs until ctx is cancelled.
func HandleTorConnections(ctx context.Context, tn *TorNode, myKeyPair bineed25519.KeyPair, handler MessageHandler) {
	for {
		conn, err := tn.AcceptMsg()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				logf("tor: accept error: %v", err)
				continue
			}
		}
		go handleTorConn(conn, tn.OnionAddr, myKeyPair, handler)
	}
}

func handleTorConn(conn net.Conn, myOnionAddr string, myKeyPair bineed25519.KeyPair, handler MessageHandler) {
	defer conn.Close()

	env, err := RecvTor(conn)
	if err != nil {
		logf("tor: recv error: %v", err)
		return
	}

	valid, err := env.VerifyTor()
	if err != nil || !valid {
		logf("tor: invalid signature from %s", env.From)
		return
	}

	handler(env)

	// Send ack
	ack := message.NewEnvelopeTor(myOnionAddr, env.From, "ack", env.ID)
	ack.ThreadID = env.ThreadID
	if err := ack.SignTor(myKeyPair); err != nil {
		logf("tor: sign ack: %v", err)
		return
	}
	if err := SendTor(conn, ack); err != nil {
		logf("tor: send ack: %v", err)
	}
}
