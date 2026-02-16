package node

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/1F47E/holler/message"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const ProtocolID = protocol.ID("/holler/1.0.0")

// MessageHandler is called when a valid message is received.
type MessageHandler func(env *message.Envelope)

// RegisterHandler sets up the stream handler for incoming holler messages.
// After receiving and verifying a message, it sends back an ack envelope.
func RegisterHandler(h host.Host, privKey crypto.PrivKey, myID peer.ID, handler MessageHandler) {
	h.SetStreamHandler(ProtocolID, func(s network.Stream) {
		defer s.Close()
		s.SetReadDeadline(time.Now().Add(30 * time.Second))

		reader := bufio.NewReader(s)
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			s.Reset()
			return
		}
		if len(line) == 0 {
			s.Reset()
			return
		}

		env, err := message.UnmarshalEnvelope(line)
		if err != nil {
			s.Reset()
			return
		}

		valid, err := env.Verify()
		if err != nil || !valid {
			s.Reset()
			return
		}

		handler(env)

		// Send ack back — body contains the original message ID
		fromID, _ := peer.Decode(env.From)
		ack := message.NewEnvelope(myID, fromID, "ack", env.ID)
		ack.ThreadID = env.ThreadID
		ack.Sign(privKey)
		ackData, _ := ack.Marshal()
		s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		s.Write(append(ackData, '\n'))
	})
}

// SendEnvelope opens a stream to the target peer, sends the envelope,
// and waits for an ack. Returns nil only if ack is received.
func SendEnvelope(ctx context.Context, h host.Host, target peer.ID, env *message.Envelope) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	s, err := h.NewStream(ctx, target, ProtocolID)
	if err != nil {
		return fmt.Errorf("open stream to %s: %w", target.String(), err)
	}
	defer s.Close()

	data, err := env.Marshal()
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	s.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := s.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}

	// Wait for ack
	s.SetReadDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(s)
	ackLine, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("waiting for ack: %w", err)
	}

	ackEnv, err := message.UnmarshalEnvelope(ackLine)
	if err != nil {
		return fmt.Errorf("parse ack: %w", err)
	}

	if ackEnv.Type != "ack" || ackEnv.Body != env.ID {
		return fmt.Errorf("unexpected ack (type=%s, body=%s)", ackEnv.Type, ackEnv.Body)
	}

	return nil
}
