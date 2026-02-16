package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"

	"github.com/1F47E/holler/identity"
	"github.com/1F47E/holler/message"
	"github.com/1F47E/holler/node"
	hollertor "github.com/1F47E/holler/tor"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/spf13/cobra"
)

var (
	sendStdin    bool
	sendPeerAddr string
	sendType     string
	sendReplyTo  string
	sendThread   string
	sendMeta     []string
	sendTor      bool
)

func init() {
	sendCmd.Flags().BoolVar(&sendStdin, "stdin", false, "Read message body from stdin")
	sendCmd.Flags().StringVar(&sendPeerAddr, "peer", "", "Direct multiaddr of the peer (skip DHT lookup)")
	sendCmd.Flags().StringVar(&sendType, "type", "message", "Message type (message, task-proposal, task-result, capability-query, status-update)")
	sendCmd.Flags().StringVar(&sendReplyTo, "reply-to", "", "Message ID this is replying to (for threading)")
	sendCmd.Flags().StringVar(&sendThread, "thread", "", "Thread ID to continue a conversation")
	sendCmd.Flags().StringSliceVar(&sendMeta, "meta", nil, "Metadata key=value pairs (can be repeated)")
	sendCmd.Flags().BoolVar(&sendTor, "tor", false, "Send via Tor transport (target must be .onion address)")
	rootCmd.AddCommand(sendCmd)
}

var sendCmd = &cobra.Command{
	Use:   "send <peer-id|alias> [message]",
	Short: "Send a message to a peer",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		var body string

		if sendStdin {
			scanner := bufio.NewScanner(os.Stdin)
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			body = strings.Join(lines, "\n")
		} else {
			if len(args) < 2 {
				return fmt.Errorf("provide a message or use --stdin")
			}
			body = strings.Join(args[1:], " ")
		}

		// Load identity
		privKey, err := identity.LoadOrFail()
		if err != nil {
			return err
		}

		var fromID string
		if sendTor {
			// For Tor mode, identity is the .onion address
			onion, err := hollertor.GetOrCreateOnionAddress()
			if err != nil {
				return fmt.Errorf("get onion address: %w", err)
			}
			fromID = onion
		} else {
			// For libp2p mode, identity is the peer ID
			peerID, err := identity.PeerIDFromKey(privKey)
			if err != nil {
				return err
			}
			fromID = peerID.String()
		}

		var toAddress string
		if sendTor {
			// For Tor mode, target should be .onion address
			contacts, err := identity.LoadContacts()
			if err != nil {
				return err
			}
			resolved := contacts.Resolve(target)
			
			// Check if it's a valid .onion address
			if !strings.HasSuffix(resolved, ".onion") {
				return fmt.Errorf("tor mode requires .onion address, got: %s", resolved)
			}
			toAddress = resolved
		} else {
			// For libp2p mode, resolve to peer ID
			contacts, err := identity.LoadContacts()
			if err != nil {
				return err
			}
			resolved := contacts.Resolve(target)

			toID, err := peer.Decode(resolved)
			if err != nil {
				return fmt.Errorf("invalid peer ID %q: %w", resolved, err)
			}
			toAddress = toID.String()
		}

		// Create envelope manually for Tor compatibility
		env := &message.Envelope{
			V:    1,
			ID:   uuid.New().String(),
			From: fromID,
			To:   toAddress,
			Ts:   time.Now().Unix(),
			Type: sendType,
			Body: body,
		}
		env.ReplyTo = sendReplyTo
		switch {
		case sendThread != "":
			env.ThreadID = sendThread
		case sendReplyTo != "":
			env.ThreadID = sendReplyTo
		default:
			env.ThreadID = env.ID
		}
		if len(sendMeta) > 0 {
			env.Meta = make(map[string]string)
			for _, kv := range sendMeta {
				if k, v, ok := strings.Cut(kv, "="); ok {
					env.Meta[k] = v
				}
			}
		}
		if err := env.Sign(privKey); err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		if sendTor {
			// Send via Tor
			fmt.Fprintf(os.Stderr, "Sending via Tor to %s...\n", toAddress)
			if err := hollertor.SendEnvelope(ctx, toAddress, env); err != nil {
				return fmt.Errorf("tor send failed: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Message sent via Tor to %s\n", toAddress)
		} else {
			// Send via libp2p (original logic)
			toID, _ := peer.Decode(toAddress)
			
			var d *dht.IpfsDHT
			h, err := node.NewHost(ctx, privKey, &d)
			if err != nil {
				return err
			}
			defer h.Close()

			// Direct peer connection (--peer flag) or DHT lookup
			if sendPeerAddr != "" {
				// Parse multiaddr and connect directly
				maddr, err := ma.NewMultiaddr(sendPeerAddr)
				if err != nil {
					return fmt.Errorf("invalid multiaddr: %w", err)
				}
				addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
				if err != nil {
					return fmt.Errorf("parse peer addr: %w", err)
				}
				// Override toID from the multiaddr if it contains a peer ID
				toID = addrInfo.ID
				env.To = toID.String()
				// Re-sign since To changed
				if err := env.Sign(privKey); err != nil {
					return err
				}

				fmt.Fprintf(os.Stderr, "Connecting directly to %s...\n", toID.String()[:16]+"...")
				if err := h.Connect(ctx, *addrInfo); err != nil {
					return fmt.Errorf("connect to peer: %w", err)
				}
			} else {
				// DHT discovery
				d, err = node.NewDHT(ctx, h)
				if err != nil {
					return err
				}
				defer d.Close()

				fmt.Fprintf(os.Stderr, "Bootstrapping DHT...\n")
				node.WaitForBootstrap(ctx, h, d, 5*time.Second)

				// Try 1: Direct DHT FindPeer
				fmt.Fprintf(os.Stderr, "Finding peer %s via DHT...\n", toID.String()[:16]+"...")
				addrInfo, err := node.FindPeer(ctx, d, toID)
				if err != nil {
					// Try 2: Rendezvous discovery
					fmt.Fprintf(os.Stderr, "DHT lookup failed, trying rendezvous discovery...\n")
					addrInfo, err = node.FindPeersRendezvous(ctx, h, d, toID)
				}
				if err != nil {
					// All methods failed — queue to outbox
					hollerDir, dirErr := identity.HollerDir()
					if dirErr != nil {
						return fmt.Errorf("peer not found and cannot save to outbox: %w", dirErr)
					}
					if saveErr := message.SaveToOutbox(hollerDir, env); saveErr != nil {
						return fmt.Errorf("peer not found and cannot save to outbox: %w", saveErr)
					}
					fmt.Fprintf(os.Stderr, "Peer offline — message queued in outbox for later delivery\n")
					return nil
				}

				fmt.Fprintf(os.Stderr, "Found peer, connecting...\n")
				connectCtx, connectCancel := context.WithTimeout(ctx, 15*time.Second)
				defer connectCancel()
				if err := h.Connect(connectCtx, addrInfo); err != nil {
					hollerDir, _ := identity.HollerDir()
					message.SaveToOutbox(hollerDir, env)
					fmt.Fprintf(os.Stderr, "Cannot connect to peer — message queued in outbox\n")
					return nil
				}
			}

			if err := node.SendEnvelope(ctx, h, toID, env); err != nil {
				hollerDir, _ := identity.HollerDir()
				message.SaveToOutbox(hollerDir, env)
				fmt.Fprintf(os.Stderr, "Send failed — message queued in outbox: %v\n", err)
				return nil
			}

			fmt.Fprintf(os.Stderr, "Message sent to %s\n", toID.String()[:16]+"...")
		}

		// Log sent message for history
		hollerDir, _ := identity.HollerDir()
		if sentData, err := json.Marshal(env); err == nil {
			message.AppendToSent(hollerDir, sentData)
		}

		return nil
	},
}
