package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"

	"github.com/1F47E/holler/identity"
	"github.com/1F47E/holler/message"
	"github.com/1F47E/holler/node"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/spf13/cobra"
)

var pingPeerAddr string

func init() {
	pingCmd.Flags().StringVar(&pingPeerAddr, "peer", "", "Direct multiaddr of the peer (skip DHT lookup)")
	rootCmd.AddCommand(pingCmd)
}

var pingCmd = &cobra.Command{
	Use:   "ping <peer-id|alias>",
	Short: "Check if a peer is online",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		// Tor mode: entirely separate path
		if node.TorMode {
			return pingViaTor(ctx, target)
		}

		// Clearnet
		privKey, err := identity.LoadOrFail()
		if err != nil {
			return err
		}
		fromID, err := identity.PeerIDFromKey(privKey)
		if err != nil {
			return err
		}

		contacts, err := identity.LoadContacts()
		if err != nil {
			return err
		}
		resolved := contacts.Resolve(target)

		toID, err := peer.Decode(resolved)
		if err != nil {
			return fmt.Errorf("invalid peer ID %q: %w", resolved, err)
		}

		// Clearnet path
		var d *dht.IpfsDHT
		h, err := node.NewHost(ctx, privKey, &d)
		if err != nil {
			return err
		}
		defer h.Close()

		if pingPeerAddr != "" {
			maddr, err := ma.NewMultiaddr(pingPeerAddr)
			if err != nil {
				return fmt.Errorf("invalid multiaddr: %w", err)
			}
			addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				return fmt.Errorf("parse peer addr: %w", err)
			}
			toID = addrInfo.ID
			if err := h.Connect(ctx, *addrInfo); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
		} else {
			d, err = node.NewDHT(ctx, h)
			if err != nil {
				return err
			}
			defer d.Close()

			fmt.Fprintf(os.Stderr, "Finding peer...\n")
			node.WaitForBootstrap(ctx, h, d, 5*time.Second)

			addrInfo, findErr := node.FindPeer(ctx, d, toID)
			if findErr != nil {
				addrInfo, findErr = node.FindPeersRendezvous(ctx, h, d, toID)
			}
			if findErr != nil {
				fmt.Fprintf(os.Stderr, "Peer %s is offline (not found on DHT)\n", toID.String()[:16]+"...")
				return nil
			}

			connectCtx, connectCancel := context.WithTimeout(ctx, 15*time.Second)
			defer connectCancel()
			if err := h.Connect(connectCtx, addrInfo); err != nil {
				fmt.Fprintf(os.Stderr, "Peer %s found but unreachable: %v\n", toID.String()[:16]+"...", err)
				return nil
			}
		}

		env := message.NewEnvelope(fromID, toID, "ping", "")
		if err := env.Sign(privKey); err != nil {
			return err
		}

		start := time.Now()
		if err := node.SendEnvelope(ctx, h, toID, env); err != nil {
			fmt.Fprintf(os.Stderr, "Peer %s connected but not responding: %v\n", toID.String()[:16]+"...", err)
			return nil
		}
		rtt := time.Since(start)

		fmt.Printf("pong from %s: rtt=%s\n", toID.String()[:16]+"...", rtt.Round(time.Millisecond))
		return nil
	},
}

func pingViaTor(ctx context.Context, target string) error {
	if err := node.CheckTorSOCKS(); err != nil {
		return err
	}

	hollerDir, err := identity.HollerDir()
	if err != nil {
		return err
	}
	onionKey, err := node.LoadOrCreateOnionKey(hollerDir)
	if err != nil {
		return err
	}
	myOnion := identity.OnionAddrFromKey(onionKey)
	kp := identity.OnionKeyPairFromBine(onionKey)

	// Resolve target
	torContacts, err := identity.LoadTorContacts()
	if err != nil {
		return err
	}
	toOnion := torContacts.Resolve(target)
	if !identity.ValidOnionAddr(toOnion) {
		return fmt.Errorf("cannot resolve %q to a Tor contact — add it with: holler contacts add --tor %s <onion-address>", target, target)
	}

	env := message.NewEnvelopeTor(myOnion, toOnion, "ping", "")
	if err := env.SignTor(kp); err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Tor: connecting to %s.onion...\n", toOnion[:16])
	connectCtx, connectCancel := context.WithTimeout(ctx, 120*time.Second)
	defer connectCancel()

	conn, err := node.DialTor(connectCtx, toOnion, 9000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tor: peer %s.onion unreachable: %v\n", toOnion[:16], err)
		return nil
	}
	defer conn.Close()

	start := time.Now()
	if err := node.SendTor(conn, env); err != nil {
		fmt.Fprintf(os.Stderr, "Tor: send failed: %v\n", err)
		return nil
	}

	ack, err := node.RecvTor(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tor: no ack: %v\n", err)
		return nil
	}
	rtt := time.Since(start)

	if ack.Type == "ack" {
		if valid, verr := ack.VerifyTor(); verr != nil || !valid {
			fmt.Fprintf(os.Stderr, "Tor: ack signature invalid\n")
			return nil
		}
		fmt.Printf("pong from %s.onion via Tor: rtt=%s\n", toOnion[:16], rtt.Round(time.Millisecond))
	} else {
		fmt.Fprintf(os.Stderr, "Tor: unexpected response type: %s\n", ack.Type)
	}
	return nil
}
