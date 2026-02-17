package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of conops-ctl",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("conops-ctl v0.1.0")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
