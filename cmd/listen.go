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
	"github.com/spf13/cobra"
)

var listenDaemon bool

func init() {
	listenCmd.Flags().BoolVar(&listenDaemon, "daemon", false, "Write to inbox.jsonl instead of stdout")
	rootCmd.AddCommand(listenCmd)
}

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen for incoming messages",
	RunE: func(cmd *cobra.Command, args []string) error {
		hollerDir, err := identity.HollerDir()
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		// Message handler — shared between Tor and clearnet
		msgHandler := func(env *message.Envelope) {
			data, err := json.Marshal(env)
			if err != nil {
				return
			}
			message.AppendToInbox(hollerDir, data)
			if !listenDaemon {
				fmt.Println(string(data))
			}
		}

		if node.TorMode {
			if err := node.CheckTorAvailable(); err != nil {
				return err
			}
			onionKey, err := node.LoadOrCreateOnionKey(hollerDir)
			if err != nil {
				return err
			}
			onionAddr := identity.OnionAddrFromKey(onionKey)
			kp := identity.OnionKeyPairFromBine(onionKey)

			tn, err := node.ListenTor(onionKey, onionAddr)
			if err != nil {
				return err
			}
			defer tn.Close()

			fmt.Fprintf(os.Stderr, "Tor mode: listening as %s.onion:9000\n", onionAddr)
			if listenDaemon {
				fmt.Fprintf(os.Stderr, "Daemon mode: writing to %s\n", message.InboxPath(hollerDir))
			}

			// Start message handler
			go node.HandleTorConnections(ctx, tn, kp, msgHandler)

			// Start homepage
			profile := node.LoadProfile(hollerDir)
			go node.StartHomepage(ctx, tn.HTTPListener(), node.HomepageData{
				Name:      profile.Name,
				Bio:       profile.Bio,
				OnionAddr: onionAddr,
				Version:   Version,
			})
			fmt.Fprintf(os.Stderr, "Homepage: http://%s.onion\n", onionAddr)

			// Start Tor outbox retry
			go retryOutboxLoopTor(ctx, hollerDir)

			<-ctx.Done()
			fmt.Fprintf(os.Stderr, "\nShutting down...\n")
			return nil
		}

		// Clearnet path
		privKey, err := identity.LoadOrFail()
		if err != nil {
			return err
		}
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

		node.RegisterHandler(h, privKey, myID, msgHandler)

		fmt.Fprintf(os.Stderr, "Listening as %s\n", myID.String())
		for _, addr := range h.Addrs() {
			fmt.Fprintf(os.Stderr, "  %s/p2p/%s\n", addr, myID.String())
		}
		if listenDaemon {
			fmt.Fprintf(os.Stderr, "Daemon mode: writing to %s\n", message.InboxPath(hollerDir))
		}

		node.WaitForBootstrap(ctx, h, d, 5*time.Second)
		node.Advertise(ctx, h, d)
		fmt.Fprintf(os.Stderr, "Advertised on DHT — senders can now find us\n")

		go func() {
			time.Sleep(15 * time.Second)
			for _, addr := range h.Addrs() {
				if node.Verbose {
					fmt.Fprintf(os.Stderr, "[debug] address: %s/p2p/%s\n", addr, myID.String())
				}
			}
		}()

		go retryOutboxLoop(ctx, h, d, hollerDir)

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

func retryOutboxLoopTor(ctx context.Context, hollerDir string) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processOutboxTor(ctx, hollerDir)
		}
	}
}

func processOutboxTor(ctx context.Context, hollerDir string) {
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

		// The To field is an onion address in Tor mode
		toOnion := entry.Envelope.To
		if len(toOnion) != 56 {
			// Not a valid onion address — skip
			entry.Attempts++
			entry.NextRetry = time.Now().Add(message.NextBackoff(entry.Attempts)).Unix()
			remaining = append(remaining, entry)
			continue
		}

		if err := deliverTorOutboxEntry(ctx, toOnion, entry.Envelope); err != nil {
			entry.Attempts++
			entry.NextRetry = time.Now().Add(message.NextBackoff(entry.Attempts)).Unix()
			remaining = append(remaining, entry)
			continue
		}

		delivered++
		fmt.Fprintf(os.Stderr, "outbox: delivered message %s to %s.onion\n", entry.Envelope.ID, toOnion[:16])
	}

	if delivered > 0 {
		fmt.Fprintf(os.Stderr, "outbox: delivered %d pending message(s)\n", delivered)
	}
	if err := message.WriteOutbox(hollerDir, remaining); err != nil {
		fmt.Fprintf(os.Stderr, "outbox: failed to write: %v\n", err)
	}
}

func deliverTorOutboxEntry(ctx context.Context, toOnion string, env *message.Envelope) error {
	connectCtx, connectCancel := context.WithTimeout(ctx, 120*time.Second)
	defer connectCancel()

	conn, err := node.DialTor(connectCtx, toOnion, 9000)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := node.SendTor(conn, env); err != nil {
		return err
	}
	// Wait for ack (best effort)
	node.RecvTor(conn)
	return nil
}
