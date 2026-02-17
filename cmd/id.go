package cmd

import (
	"fmt"

	"github.com/1F47E/holler/identity"
	"github.com/1F47E/holler/node"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(idCmd)
}

var idCmd = &cobra.Command{
	Use:   "id",
	Short: "Print your PeerID and onion address",
	RunE: func(cmd *cobra.Command, args []string) error {
		key, err := identity.LoadOrFail()
		if err != nil {
			return err
		}
		pid, err := identity.PeerIDFromKey(key)
		if err != nil {
			return err
		}
		fmt.Printf("PeerID: %s\n", pid.String())

		// Show onion address if tor_key exists
		hollerDir, dirErr := identity.HollerDir()
		if dirErr == nil {
			if onionKey, keyErr := node.LoadOrCreateOnionKey(hollerDir); keyErr == nil {
				fmt.Printf("Onion:  %s.onion\n", identity.OnionAddrFromKey(onionKey))
			}
		}
		return nil
	},
}
