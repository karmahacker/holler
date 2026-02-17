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
)

func init() {
	sendCmd.Flags().BoolVar(&sendStdin, "stdin", false, "Read message body from stdin")
	sendCmd.Flags().StringVar(&sendPeerAddr, "peer", "", "Direct multiaddr of the peer (skip DHT lookup)")
	sendCmd.Flags().StringVar(&sendType, "type", "message", "Message type (message, task-proposal, task-result, capability-query, status-update)")
	sendCmd.Flags().StringVar(&sendReplyTo, "reply-to", "", "Message ID this is replying to (for threading)")
	sendCmd.Flags().StringVar(&sendThread, "thread", "", "Thread ID to continue a conversation")
	sendCmd.Flags().StringSliceVar(&sendMeta, "meta", nil, "Metadata key=value pairs (can be repeated)")
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

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		// Tor mode: entirely separate path
		if node.TorMode {
			return sendViaTor(ctx, target, body)
		}

		// Clearnet: load identity and resolve contacts
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

		env := message.NewEnvelope(fromID, toID, sendType, body)
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

		// Clearnet path
		var d *dht.IpfsDHT
		h, err := node.NewHost(ctx, privKey, &d)
		if err != nil {
			return err
		}
		defer h.Close()

		if sendPeerAddr != "" {
			maddr, err := ma.NewMultiaddr(sendPeerAddr)
			if err != nil {
				return fmt.Errorf("invalid multiaddr: %w", err)
			}
			addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				return fmt.Errorf("parse peer addr: %w", err)
			}
			toID = addrInfo.ID
			env.To = toID.String()
			if err := env.Sign(privKey); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Connecting directly to %s...\n", toID.String()[:16]+"...")
			if err := h.Connect(ctx, *addrInfo); err != nil {
				return fmt.Errorf("connect to peer: %w", err)
			}
		} else {
			d, err = node.NewDHT(ctx, h)
			if err != nil {
				return err
			}
			defer d.Close()

			fmt.Fprintf(os.Stderr, "Bootstrapping DHT...\n")
			node.WaitForBootstrap(ctx, h, d, 5*time.Second)

			fmt.Fprintf(os.Stderr, "Finding peer %s via DHT...\n", toID.String()[:16]+"...")
			addrInfo, err := node.FindPeer(ctx, d, toID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "DHT lookup failed, trying rendezvous discovery...\n")
				addrInfo, err = node.FindPeersRendezvous(ctx, h, d, toID)
			}
			if err != nil {
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

		hollerDir, _ := identity.HollerDir()
		if sentData, err := json.Marshal(env); err == nil {
			message.AppendToSent(hollerDir, sentData)
		}

		fmt.Fprintf(os.Stderr, "Message sent to %s\n", toID.String()[:16]+"...")
		return nil
	},
}

func sendViaTor(ctx context.Context, target, body string) error {
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

	// Resolve target via tor contacts
	torContacts, err := identity.LoadTorContacts()
	if err != nil {
		return err
	}
	toOnion := torContacts.Resolve(target)

	// Validate onion address format
	if !identity.ValidOnionAddr(toOnion) {
		return fmt.Errorf("cannot resolve %q to a Tor contact — add it with: holler contacts add --tor %s <onion-address>", target, target)
	}

	// Build envelope
	env := message.NewEnvelopeTor(myOnion, toOnion, sendType, body)
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
	if err := env.SignTor(kp); err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	// Dial and send
	fmt.Fprintf(os.Stderr, "Tor: connecting to %s.onion...\n", toOnion[:16])
	connectCtx, connectCancel := context.WithTimeout(ctx, 120*time.Second)
	defer connectCancel()

	conn, err := node.DialTor(connectCtx, toOnion, 9000)
	if err != nil {
		// Queue to outbox
		message.SaveToOutbox(hollerDir, env)
		fmt.Fprintf(os.Stderr, "Tor: peer unreachable — message queued in outbox\n")
		return nil
	}
	defer conn.Close()

	if err := node.SendTor(conn, env); err != nil {
		message.SaveToOutbox(hollerDir, env)
		fmt.Fprintf(os.Stderr, "Tor: send failed — message queued in outbox: %v\n", err)
		return nil
	}

	// Wait for ack and verify signature
	ack, err := node.RecvTor(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tor: no ack received (message likely delivered): %v\n", err)
	} else if ack.Type == "ack" && ack.Body == env.ID {
		if valid, verr := ack.VerifyTor(); verr != nil || !valid {
			fmt.Fprintf(os.Stderr, "Tor: ack signature invalid\n")
		}
	}

	if sentData, err := json.Marshal(env); err == nil {
		message.AppendToSent(hollerDir, sentData)
	}
	fmt.Fprintf(os.Stderr, "Message sent via Tor to %s.onion\n", toOnion[:16])
	return nil
}
