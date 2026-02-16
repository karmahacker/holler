package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/1F47E/holler/identity"
	"github.com/1F47E/holler/message"
	"github.com/1F47E/holler/node"
	hollertor "github.com/1F47E/holler/tor"
	"github.com/spf13/cobra"
)

var (
	listenDaemon bool
	listenTor    bool
)

func init() {
	listenCmd.Flags().BoolVar(&listenDaemon, "daemon", false, "Write to inbox.jsonl instead of stdout")
	listenCmd.Flags().BoolVar(&listenTor, "tor", false, "Listen via Tor transport (creates .onion hidden service)")
	rootCmd.AddCommand(listenCmd)
}

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen for incoming messages",
	RunE: func(cmd *cobra.Command, args []string) error {
		privKey, err := identity.LoadOrFail()
		if err != nil {
			return err
		}

		hollerDir, err := identity.HollerDir()
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		// Create message handler
		messageHandler := func(env *message.Envelope) {
			data, err := json.Marshal(env)
			if err != nil {
				return
			}
			// Always log to inbox for persistent history
			message.AppendToInbox(hollerDir, data)
			if !listenDaemon {
				// Also print to stdout for piping
				fmt.Println(string(data))
			}
		}

		if listenTor {
			// Tor mode
			onion, err := hollertor.GetOrCreateOnionAddress()
			if err != nil {
				return fmt.Errorf("setup onion service: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Listening via Tor as %s\n", onion)
			if listenDaemon {
				fmt.Fprintf(os.Stderr, "Daemon mode: writing to %s\n", message.InboxPath(hollerDir))
			}

			if err := hollertor.StartTorListener(ctx, messageHandler, privKey, onion); err != nil {
				return fmt.Errorf("tor listener failed: %w", err)
			}
		} else {
			// libp2p mode (original logic)
			myID, err := identity.PeerIDFromKey(privKey)
			if err != nil {
				return err
			}

			var d *dht.IpfsDHT
			h, err := node.NewHost(ctx, privKey, &d)
			if err != nil {
				return err
			}
			defer h.Close()

			d, err = node.NewDHT(ctx, h)
			if err != nil {
				return err
			}
			defer d.Close()

			// Register message handler
			node.RegisterHandler(h, privKey, myID, messageHandler)

			fmt.Fprintf(os.Stderr, "Listening as %s\n", myID.String())
			for _, addr := range h.Addrs() {
				fmt.Fprintf(os.Stderr, "  %s/p2p/%s\n", addr, myID.String())
			}
			if listenDaemon {
				fmt.Fprintf(os.Stderr, "Daemon mode: writing to %s\n", message.InboxPath(hollerDir))
			}

			// Wait for bootstrap connections
			node.WaitForBootstrap(ctx, h, d, 5*time.Second)

			// Advertise on DHT so senders can find us
			node.Advertise(ctx, h, d)
			fmt.Fprintf(os.Stderr, "Advertised on DHT — senders can now find us\n")

			// Log relay addresses if AutoRelay assigned any
			go func() {
				// Give AutoRelay time to find relays and get reservations
				time.Sleep(15 * time.Second)
				for _, addr := range h.Addrs() {
					if node.Verbose {
						fmt.Fprintf(os.Stderr, "[debug] address: %s/p2p/%s\n", addr, myID.String())
					}
				}
			}()

			// Start outbox retry loop
			go retryOutboxLoop(ctx, h, d, hollerDir)

			// Re-advertise periodically (DHT records expire)
			go func() {
				ticker := time.NewTicker(10 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						node.Advertise(ctx, h, d)
					}
				}
			}()

			<-ctx.Done()
		}

		fmt.Fprintf(os.Stderr, "\nShutting down...\n")
		return nil
	},
}

func retryOutboxLoop(ctx context.Context, h host.Host, d *dht.IpfsDHT, hollerDir string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processOutbox(ctx, h, d, hollerDir)
		}
	}
}

func processOutbox(ctx context.Context, h host.Host, d *dht.IpfsDHT, hollerDir string) {
	entries, err := message.LoadOutbox(hollerDir)
	if err != nil || len(entries) == 0 {
		return
	}

	now := time.Now().Unix()
	var remaining []message.OutboxEntry
	delivered := 0

	for _, entry := range entries {
		if entry.NextRetry > now {
			remaining = append(remaining, entry)
			continue
		}
		if entry.Attempts >= message.MaxRetries {
			fmt.Fprintf(os.Stderr, "outbox: giving up on message %s after %d attempts\n", entry.Envelope.ID, entry.Attempts)
			continue
		}

		// Try to deliver
		toID, err := peer.Decode(entry.Envelope.To)
		if err != nil {
			continue
		}

		addrInfo, err := node.FindPeer(ctx, d, toID)
		if err != nil {
			entry.Attempts++
			entry.NextRetry = time.Now().Add(message.NextBackoff(entry.Attempts)).Unix()
			remaining = append(remaining, entry)
			continue
		}

		if err := h.Connect(ctx, addrInfo); err != nil {
			entry.Attempts++
			entry.NextRetry = time.Now().Add(message.NextBackoff(entry.Attempts)).Unix()
			remaining = append(remaining, entry)
			continue
		}

		if err := node.SendEnvelope(ctx, h, toID, entry.Envelope); err != nil {
			entry.Attempts++
			entry.NextRetry = time.Now().Add(message.NextBackoff(entry.Attempts)).Unix()
			remaining = append(remaining, entry)
			continue
		}

		delivered++
		fmt.Fprintf(os.Stderr, "outbox: delivered message %s to %s\n", entry.Envelope.ID, toID.String()[:16]+"...")
	}

	if delivered > 0 {
		fmt.Fprintf(os.Stderr, "outbox: delivered %d pending message(s)\n", delivered)
	}
	if err := message.WriteOutbox(hollerDir, remaining); err != nil {
		fmt.Fprintf(os.Stderr, "outbox: failed to write: %v\n", err)
	}
}
