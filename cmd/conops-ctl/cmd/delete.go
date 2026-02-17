package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

// deleteCmd represents the delete command
var deleteCmd = &cobra.Command{
	Use:   "delete [app-id]",
	Short: "Delete an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appID := args[0]
		client := NewClient()
		resp, err := client.Delete("/api/v1/apps/" + appID)
		if err != nil {
			return fmt.Errorf("error deleting app: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return CheckResponse(resp)
		}

		fmt.Println("App deleted successfully.")
		return nil
	},
}

func init() {
	appsCmd.AddCommand(deleteCmd)
}
