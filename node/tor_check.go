package node

import (
	"fmt"
	"net"
	"time"
)

// TorMode enables Tor transport mode (set by --tor flag).
var TorMode bool

// CheckTorSOCKS verifies the Tor SOCKS5 proxy is reachable (port 9050).
// Needed for dialing (send/ping).
func CheckTorSOCKS() error {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9050", 2*time.Second)
	if err != nil {
		return fmt.Errorf("Tor SOCKS5 proxy not reachable on 127.0.0.1:9050 — install and start tor:\n  macOS: brew install tor && brew services start tor\n  Linux: sudo apt install tor && sudo systemctl start tor")
	}
	conn.Close()
	logf("tor: SOCKS5 proxy reachable")
	return nil
}

// CheckTorAvailable verifies both SOCKS5 (9050) and control (9051) ports.
// Needed for listening (creating onion services).
func CheckTorAvailable() error {
	if err := CheckTorSOCKS(); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9051", 2*time.Second)
	if err != nil {
		return fmt.Errorf("Tor control port not reachable on 127.0.0.1:9051 — enable it in torrc:\n  ControlPort 9051\n  CookieAuthentication 1")
	}
	conn.Close()
	logf("tor: SOCKS5 and control port reachable")
	return nil
}
