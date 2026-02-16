package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const Version = "0.2.2"

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("holler v%s\n", Version)
	},
}
