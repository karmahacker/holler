package cmd

import (
	"fmt"

	"github.com/1F47E/holler/identity"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"
)

var contactsTor bool
var contactsOnion string

func init() {
	contactsCmd.PersistentFlags().BoolVar(&contactsTor, "tor", false, "Manage Tor contacts (onion addresses)")
	contactsAddCmd.Flags().StringVar(&contactsOnion, "onion", "", "Onion address for Tor contacts (56-char base32)")
	contactsCmd.AddCommand(contactsAddCmd)
	contactsCmd.AddCommand(contactsRmCmd)
	rootCmd.AddCommand(contactsCmd)
}

var contactsCmd = &cobra.Command{
	Use:   "contacts",
	Short: "Manage contact aliases",
	RunE: func(cmd *cobra.Command, args []string) error {
		if contactsTor {
			return listTorContacts()
		}
		contacts, err := identity.LoadContacts()
		if err != nil {
			return err
		}
		if len(contacts) == 0 {
			fmt.Println("No contacts saved.")
			return nil
		}
		for _, alias := range contacts.SortedAliases() {
			fmt.Printf("%-20s %s\n", alias, contacts[alias])
		}
		return nil
	},
}

var contactsAddCmd = &cobra.Command{
	Use:   "add <alias> <peer-id|onion-addr>",
	Short: "Save a contact alias",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		alias, addr := args[0], args[1]

		if contactsTor {
			return addTorContact(alias, addr)
		}

		// Clearnet: validate peer ID
		if _, err := peer.Decode(addr); err != nil {
			return fmt.Errorf("invalid peer ID: %w", err)
		}

		contacts, err := identity.LoadContacts()
		if err != nil {
			return err
		}
		contacts[alias] = addr
		if err := identity.SaveContacts(contacts); err != nil {
			return err
		}
		fmt.Printf("Added contact %q → %s\n", alias, addr[:16]+"...")
		return nil
	},
}

var contactsRmCmd = &cobra.Command{
	Use:   "rm <alias>",
	Short: "Remove a contact",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		alias := args[0]

		if contactsTor {
			return rmTorContact(alias)
		}

		contacts, err := identity.LoadContacts()
		if err != nil {
			return err
		}
		if _, ok := contacts[alias]; !ok {
			return fmt.Errorf("contact %q not found", alias)
		}
		delete(contacts, alias)
		if err := identity.SaveContacts(contacts); err != nil {
			return err
		}
		fmt.Printf("Removed contact %q\n", alias)
		return nil
	},
}

func listTorContacts() error {
	contacts, err := identity.LoadTorContacts()
	if err != nil {
		return err
	}
	if len(contacts) == 0 {
		fmt.Println("No Tor contacts saved.")
		return nil
	}
	for _, alias := range contacts.SortedAliases() {
		fmt.Printf("%-20s %s.onion\n", alias, contacts[alias])
	}
	return nil
}

func addTorContact(alias, onionAddr string) error {
	if !identity.ValidOnionAddr(onionAddr) {
		return fmt.Errorf("invalid onion address: must be 56 characters, lowercase a-z and 2-7")
	}

	contacts, err := identity.LoadTorContacts()
	if err != nil {
		return err
	}
	contacts[alias] = onionAddr
	if err := identity.SaveTorContacts(contacts); err != nil {
		return err
	}
	fmt.Printf("Added Tor contact %q → %s.onion\n", alias, onionAddr[:16]+"...")
	return nil
}

func rmTorContact(alias string) error {
	contacts, err := identity.LoadTorContacts()
	if err != nil {
		return err
	}
	if _, ok := contacts[alias]; !ok {
		return fmt.Errorf("tor contact %q not found", alias)
	}
	delete(contacts, alias)
	if err := identity.SaveTorContacts(contacts); err != nil {
		return err
	}
	fmt.Printf("Removed Tor contact %q\n", alias)
	return nil
}
