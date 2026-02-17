package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/conops/conops/internal/api"
	"github.com/spf13/cobra"
)

// getCmd represents the get command
var getCmd = &cobra.Command{
	Use:   "get [app-id]",
	Short: "Get application details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appID := args[0]
		client := NewClient()
		resp, err := client.Get("/api/v1/apps/" + appID)
		if err != nil {
			return fmt.Errorf("error getting app: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return CheckResponse(resp)
		}

		var apiResp struct {
			Data api.App `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return fmt.Errorf("error decoding response: %v", err)
		}

		PrintJSON(apiResp.Data)
		return nil
	},
}

func init() {
	appsCmd.AddCommand(getCmd)
}
