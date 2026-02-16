package node

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"

	dht "github.com/libp2p/go-libp2p-kad-dht"
)

// Verbose enables debug logging to stderr.
var Verbose bool

func logf(format string, args ...any) {
	if Verbose {
		fmt.Fprintf(os.Stderr, "[debug] "+format+"\n", args...)
	}
}

// NewHost creates a libp2p host with the given private key and all transports enabled.
// dhtPtr is a pointer-to-pointer that gets populated after DHT creation. AutoRelay
// reads it to find relay candidates from the DHT routing table.
func NewHost(ctx context.Context, privKey crypto.PrivKey, dhtPtr **dht.IpfsDHT) (host.Host, error) {
	cm, err := connmgr.NewConnManager(10, 100)
	if err != nil {
		return nil, fmt.Errorf("create conn manager: %w", err)
	}

	// Peer source for AutoRelay: returns peers from the DHT routing table
	// as relay candidates. AutoRelay probes them to check if they support
	// circuit relay v2 and makes reservations with those that do.
	// When behind NAT, this enables the host to advertise relay addresses
	// like /p2p/<relay>/p2p-circuit/p2p/<us> so remote peers can reach us.
	peerSource := func(ctx context.Context, num int) <-chan peer.AddrInfo {
		ch := make(chan peer.AddrInfo, num)
		go func() {
			defer close(ch)
			if dhtPtr == nil || *dhtPtr == nil {
				return
			}
			d := *dhtPtr
			peers := d.RoutingTable().ListPeers()
			logf("autorelay: %d peers in routing table, need %d relay candidates", len(peers), num)
			sent := 0
			for _, p := range peers {
				if sent >= num {
					break
				}
				addrs := d.Host().Peerstore().Addrs(p)
				if len(addrs) == 0 {
					continue
				}
				select {
				case ch <- peer.AddrInfo{ID: p, Addrs: addrs}:
					sent++
				case <-ctx.Done():
					return
				}
			}
			logf("autorelay: sent %d relay candidates", sent)
		}()
		return ch
	}

	h, err := libp2p.New(
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/46875",
			"/ip4/0.0.0.0/udp/46875/quic-v1",
			"/ip6/::/tcp/46875",
			"/ip6/::/udp/46875/quic-v1",
		),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoNATv2(),
		libp2p.NATPortMap(),
		libp2p.ConnectionManager(cm),
		libp2p.EnableAutoRelayWithPeerSource(
			autorelay.PeerSource(peerSource),
			autorelay.WithNumRelays(2),
			autorelay.WithMinInterval(30*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	logf("host created: %s", h.ID())
	for _, addr := range h.Addrs() {
		logf("  listening: %s", addr)
	}

	return h, nil
}
