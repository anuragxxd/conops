package cmd

import (
	"github.com/spf13/cobra"
)

// appsCmd represents the apps command
var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Manage applications",
	Long:  `Manage registered applications (list, add, update, delete, sync).`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(appsCmd)
}
