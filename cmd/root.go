package cmd

import (
	"github.com/1F47E/holler/identity"
	"github.com/1F47E/holler/node"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "holler",
	Short: "P2P encrypted messenger for AI agents",
	Long:  "holler — peer-to-peer encrypted messaging. No servers, no registration. Identity is a keypair.",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&identity.DirOverride, "dir", "", "data directory (default ~/.holler)")
	rootCmd.PersistentFlags().BoolVarP(&node.Verbose, "verbose", "v", false, "verbose debug logging")
	rootCmd.PersistentFlags().BoolVar(&node.TorMode, "tor", false, "route all traffic through Tor (requires tor daemon)")
}

func Execute() error {
	return rootCmd.Execute()
}
