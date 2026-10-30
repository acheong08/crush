package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/tailcatx"
	"github.com/spf13/cobra"
)

func init() {
	tailcatCmd.AddCommand(tailcatIDCmd)
	rootCmd.AddCommand(tailcatCmd)
}

var tailcatCmd = &cobra.Command{
	Use:   "tailcat",
	Short: "Inspect the tailcat transport identity",
	Long: `Inspect the tailcat transport identity.

Crush's tailcat transport has no account system: a peer is whatever
node key it presents. These commands expose the local key so it can be
added to a server's allowlist with --allow-peer (or
CRUSH_TAILCAT_ALLOW).
`,
}

var tailcatIDCmd = &cobra.Command{
	Use:   "id",
	Short: "Print this machine's node key",
	Long: `Print this machine's tailcat node key.

Add it to the server's --allow-peer list to let this machine through:

  crush server --host tailcat:// --allow-peer $(crush tailcat id)
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		nodeKey, err := tailcatx.ClientNodeKey(filepath.Dir(config.GlobalConfigData()))
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), nodeKey.String())
		return nil
	},
}
