package cmd

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

var (
	updateName         string
	updateBranch       string
	updateComposePath  string
	updatePollInterval string
)

// updateCmd represents the update command
var updateCmd = &cobra.Command{
	Use:   "update [app-id]",
	Short: "Update an application",
	Long:  `Update app settings. Only provided flags are changed.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appID := args[0]
		updates := make(map[string]interface{})

		if cmd.Flags().Changed("name") {
			updates["name"] = updateName
		}
		if cmd.Flags().Changed("branch") {
			updates["branch"] = updateBranch
		}
		if cmd.Flags().Changed("compose-path") {
			updates["compose_path"] = updateComposePath
		}
		if cmd.Flags().Changed("poll-interval") {
			updates["poll_interval"] = updatePollInterval
		}

		if len(updates) == 0 {
			return fmt.Errorf("no updates provided")
		}

		client := NewClient()
		resp, err := client.Patch("/api/v1/apps/"+appID, updates)
		if err != nil {
			return fmt.Errorf("error updating app: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return CheckResponse(resp)
		}

		fmt.Println("App updated successfully.")
		return nil
	},
}

func init() {
	updateCmd.Flags().StringVar(&updateName, "name", "", "New name for the app")
	updateCmd.Flags().StringVar(&updateBranch, "branch", "", "New branch to track")
	updateCmd.Flags().StringVar(&updateComposePath, "compose-path", "", "New compose file path")
	updateCmd.Flags().StringVar(&updatePollInterval, "poll-interval", "", "New poll interval (e.g. 30s)")
	appsCmd.AddCommand(updateCmd)
}
