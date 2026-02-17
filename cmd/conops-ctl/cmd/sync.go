package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

// syncCmd represents the sync command
var syncCmd = &cobra.Command{
	Use:   "sync [app-id]",
	Short: "Force sync an application",
	Long:  `Trigger immediate Git sync and Compose update.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appID := args[0]
		client := NewClient()
		// sync is a POST request to /apps/{id}/sync with empty body
		resp, err := client.Post("/api/v1/apps/"+appID+"/sync", nil)
		if err != nil {
			return fmt.Errorf("error syncing app: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return CheckResponse(resp)
		}

		fmt.Println("Sync triggered successfully.")
		return nil
	},
}

func init() {
	appsCmd.AddCommand(syncCmd)
}
