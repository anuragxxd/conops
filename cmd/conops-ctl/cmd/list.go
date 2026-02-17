package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/spf13/cobra"
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all applications",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := NewClient()
		resp, err := client.Get("/api/v1/apps/")
		if err != nil {
			return fmt.Errorf("error fetching apps: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return CheckResponse(resp)
		}

		var apiResp struct {
			Data []api.App `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return fmt.Errorf("error decoding response: %v", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tREPO\tBRANCH\tSTATUS\tLAST SYNC")
		for _, app := range apiResp.Data {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", app.ID, app.Name, app.RepoURL, app.Branch, app.Status, app.LastSyncAt.Format(time.RFC3339))
		}
		w.Flush()

		return nil
	},
}

func init() {
	appsCmd.AddCommand(listCmd)
}
