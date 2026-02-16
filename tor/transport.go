package tor

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/1F47E/holler/message"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"golang.org/x/net/proxy"
)

const (
	// Default Tor SOCKS5 proxy
	DefaultTorProxy = "127.0.0.1:9050"
	// Tor hidden service directory
	HiddenServiceDir = "/var/lib/tor-holler/hidden_service"
)

// TorDialer creates connections through Tor SOCKS5 proxy
func TorDialer() (proxy.Dialer, error) {
	dialer, err := proxy.SOCKS5("tcp", DefaultTorProxy, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("create SOCKS5 dialer: %w", err)
	}
	return dialer, nil
}

// SendEnvelope sends a message envelope over Tor to an .onion address
func SendEnvelope(ctx context.Context, onionAddr string, env *message.Envelope) error {
	dialer, err := TorDialer()
	if err != nil {
		return fmt.Errorf("create Tor dialer: %w", err)
	}

	// Connect to the .onion address on the default port
	target := onionAddr + ":8081"
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", target, err)
	}
	defer conn.Close()

	// Set timeout for the entire operation
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Marshal envelope to JSON
	data, err := env.Marshal()
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	// Send length-prefixed JSON
	if err := sendLengthPrefixedData(conn, data); err != nil {
		return fmt.Errorf("send envelope: %w", err)
	}

	// Read ack response
	ackData, err := receiveLengthPrefixedData(conn)
	if err != nil {
		return fmt.Errorf("receive ack: %w", err)
	}

	// Parse ack envelope
	ackEnv, err := message.UnmarshalEnvelope(ackData)
	if err != nil {
		return fmt.Errorf("parse ack: %w", err)
	}

	if ackEnv.Type != "ack" || ackEnv.Body != env.ID {
		return fmt.Errorf("unexpected ack (type=%s, body=%s)", ackEnv.Type, ackEnv.Body)
	}

	return nil
}

// sendLengthPrefixedData sends data with a 4-byte big-endian length prefix
func sendLengthPrefixedData(conn net.Conn, data []byte) error {
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	
	if _, err := conn.Write(length); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	return nil
}

// receiveLengthPrefixedData receives data with a 4-byte big-endian length prefix
func receiveLengthPrefixedData(conn net.Conn) ([]byte, error) {
	// Read length prefix
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, fmt.Errorf("read length prefix: %w", err)
	}
	
	length := binary.BigEndian.Uint32(lengthBuf)
	if length > 1024*1024 { // 1MB max
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}
	
	// Read data
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}
	
	return data, nil
}

// MessageHandler is called when a valid message is received.
type MessageHandler func(env *message.Envelope)

// HandleTorConnection handles an incoming Tor connection
func HandleTorConnection(conn net.Conn, handler MessageHandler, privKey crypto.PrivKey, myOnion string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Receive envelope
	data, err := receiveLengthPrefixedData(conn)
	if err != nil {
		return
	}

	env, err := message.UnmarshalEnvelope(data)
	if err != nil {
		return
	}

	// Skip signature verification for Tor mode for now (TODO: implement proper Tor verification)
	// In Tor mode, the From field is a .onion address, not a peer ID
	if !strings.HasSuffix(env.From, ".onion") {
		// Verify signature for libp2p peer IDs only
		valid, err := env.Verify()
		if err != nil || !valid {
			return
		}
	}

	// Call message handler
	handler(env)

	// Send ack back
	ack := &message.Envelope{
		V:        1,
		ID:       uuid.New().String(),
		From:     myOnion,
		To:       env.From,
		Ts:       time.Now().Unix(),
		Type:     "ack",
		Body:     env.ID,
		ThreadID: env.ThreadID,
	}
	if err := ack.Sign(privKey); err != nil {
		return
	}

	ackData, err := ack.Marshal()
	if err != nil {
		return
	}

	conn.SetDeadline(time.Now().Add(10 * time.Second))
	sendLengthPrefixedData(conn, ackData)
}

// SetupHiddenService creates a Tor hidden service configuration
func SetupHiddenService() (string, error) {
	hiddenServiceDir := HiddenServiceDir
	if err := os.MkdirAll(hiddenServiceDir, 0700); err != nil {
		return "", fmt.Errorf("create hidden service dir: %w", err)
	}

	// Check if hostname already exists
	hostnameFile := filepath.Join(hiddenServiceDir, "hostname")
	if data, err := os.ReadFile(hostnameFile); err == nil {
		onion := strings.TrimSpace(string(data))
		if strings.HasSuffix(onion, ".onion") {
			return onion, nil
		}
	}

	// Create torrc configuration snippet
	torrcPath := filepath.Join(hiddenServiceDir, "torrc")
	torrcContent := fmt.Sprintf(`HiddenServiceDir %s
HiddenServicePort 8081 127.0.0.1:8081
`, hiddenServiceDir)

	if err := os.WriteFile(torrcPath, []byte(torrcContent), 0600); err != nil {
		return "", fmt.Errorf("write torrc: %w", err)
	}

	// Try to add to system torrc
	if err := addToSystemTorrc(hiddenServiceDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not configure system Tor: %v\n", err)
		fmt.Fprintf(os.Stderr, "You may need to manually add the following to /etc/tor/torrc:\n")
		fmt.Fprintf(os.Stderr, "%s", torrcContent)
		fmt.Fprintf(os.Stderr, "Then restart Tor with: sudo systemctl restart tor\n")
	}

	// Wait for hostname file to be created
	for i := 0; i < 30; i++ {
		if data, err := os.ReadFile(hostnameFile); err == nil {
			onion := strings.TrimSpace(string(data))
			if strings.HasSuffix(onion, ".onion") {
				return onion, nil
			}
		}
		time.Sleep(1 * time.Second)
	}

	return "", fmt.Errorf("timeout waiting for hidden service to be created")
}

// addToSystemTorrc attempts to add hidden service config to system torrc
func addToSystemTorrc(hiddenServiceDir string) error {
	torrcPath := "/etc/tor/torrc"
	
	// Check if already configured
	if data, err := os.ReadFile(torrcPath); err == nil {
		content := string(data)
		if strings.Contains(content, hiddenServiceDir) {
			// Already configured, restart tor
			return restartTor()
		}
	}

	// Append configuration
	config := fmt.Sprintf(`
# Holler hidden service
HiddenServiceDir %s
HiddenServicePort 8080 127.0.0.1:8080
`, hiddenServiceDir)

	f, err := os.OpenFile(torrcPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(config); err != nil {
		return err
	}

	return restartTor()
}

// restartTor restarts the system Tor service
func restartTor() error {
	// Try systemctl first
	if err := exec.Command("systemctl", "restart", "tor").Run(); err == nil {
		return nil
	}

	// Try service command
	if err := exec.Command("service", "tor", "restart").Run(); err == nil {
		return nil
	}

	return fmt.Errorf("could not restart Tor service")
}

// StartTorListener starts a TCP listener for incoming Tor connections
func StartTorListener(ctx context.Context, handler MessageHandler, privKey crypto.PrivKey, myOnion string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:8081")
	if err != nil {
		return fmt.Errorf("start TCP listener: %w", err)
	}
	defer listener.Close()

	fmt.Fprintf(os.Stderr, "Tor listener started on %s\n", myOnion)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			continue
		}

		go HandleTorConnection(conn, handler, privKey, myOnion)
	}
}