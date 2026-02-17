package node

import (
	"context"
	"fmt"
	"net"
	"net/textproto"

	"github.com/cretz/bine/control"
	"golang.org/x/net/proxy"
)

const (
	torSOCKSAddr     = "127.0.0.1:9050"
	torControlAddr   = "127.0.0.1:9051"
	torMsgPort       = 9000
	torHTTPPort      = 80
)

// TorNode holds the state for a Tor onion service with two ports.
type TorNode struct {
	OnionAddr    string // 56-char base32 service ID (no .onion)
	ctrl         *control.Conn
	msgListener  net.Listener // local TCP for port 9000 (holler messages)
	httpListener net.Listener // local TCP for port 80 (homepage)
}

// ListenTor creates a Tor onion service with two virtual ports:
//   - 9000 → holler message protocol
//   - 80   → HTTP homepage
//
// Returns a TorNode with both listeners ready to Accept().
func ListenTor(onionKey *control.ED25519Key, onionAddr string) (*TorNode, error) {
	// Start two local TCP listeners
	msgLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("tor: bind message listener: %w", err)
	}
	msgPort := msgLn.Addr().(*net.TCPAddr).Port
	logf("tor: message listener on 127.0.0.1:%d", msgPort)

	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		msgLn.Close()
		return nil, fmt.Errorf("tor: bind http listener: %w", err)
	}
	httpPort := httpLn.Addr().(*net.TCPAddr).Port
	logf("tor: http listener on 127.0.0.1:%d", httpPort)

	// Connect to Tor control port
	textConn, err := textproto.Dial("tcp", torControlAddr)
	if err != nil {
		msgLn.Close()
		httpLn.Close()
		return nil, fmt.Errorf("tor: connect to control port: %w", err)
	}
	ctrl := control.NewConn(textConn)

	if err := ctrl.Authenticate(""); err != nil {
		msgLn.Close()
		httpLn.Close()
		ctrl.Close()
		return nil, fmt.Errorf("tor: authenticate with control port: %w", err)
	}
	logf("tor: authenticated with control port")

	// Create onion service with two ports
	resp, err := ctrl.AddOnion(&control.AddOnionRequest{
		Key: onionKey,
		Ports: []*control.KeyVal{
			{Key: fmt.Sprintf("%d", torMsgPort), Val: fmt.Sprintf("127.0.0.1:%d", msgPort)},
			{Key: fmt.Sprintf("%d", torHTTPPort), Val: fmt.Sprintf("127.0.0.1:%d", httpPort)},
		},
	})
	if err != nil {
		msgLn.Close()
		httpLn.Close()
		ctrl.Close()
		return nil, fmt.Errorf("tor: create onion service: %w", err)
	}
	logf("tor: onion service created: %s.onion (ports %d, %d)", resp.ServiceID, torMsgPort, torHTTPPort)

	if resp.ServiceID != onionAddr {
		msgLn.Close()
		httpLn.Close()
		ctrl.Close()
		return nil, fmt.Errorf("tor: onion service ID mismatch: expected %s, got %s", onionAddr, resp.ServiceID)
	}

	return &TorNode{
		OnionAddr:    onionAddr,
		ctrl:         ctrl,
		msgListener:  msgLn,
		httpListener: httpLn,
	}, nil
}

// AcceptMsg waits for the next incoming TCP connection on the message port (9000).
func (tn *TorNode) AcceptMsg() (net.Conn, error) {
	return tn.msgListener.Accept()
}

// HTTPListener returns the net.Listener for the HTTP homepage port (80).
func (tn *TorNode) HTTPListener() net.Listener {
	return tn.httpListener
}

// Close tears down the onion service and closes all listeners.
func (tn *TorNode) Close() error {
	var firstErr error
	if tn.ctrl != nil && tn.OnionAddr != "" {
		if err := tn.ctrl.DelOnion(tn.OnionAddr); err != nil {
			firstErr = fmt.Errorf("remove onion service: %w", err)
		}
		if err := tn.ctrl.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close tor control: %w", err)
		}
	}
	if err := tn.msgListener.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := tn.httpListener.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// DialTor connects to a remote onion address via Tor SOCKS5 proxy.
func DialTor(ctx context.Context, onionAddr string, port int) (net.Conn, error) {
	target := fmt.Sprintf("%s.onion:%d", onionAddr, port)
	logf("tor: dialing %s via SOCKS5", target)

	dialer, err := proxy.SOCKS5("tcp", torSOCKSAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("tor dial: create SOCKS5 dialer: %w", err)
	}

	if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
		return ctxDialer.DialContext(ctx, "tcp", target)
	}
	return dialer.Dial("tcp", target)
}
