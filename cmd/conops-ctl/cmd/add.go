package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/conops/conops/internal/api"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	addName          string
	addRepoURL       string
	addBranch        string
	addComposePath   string
	addPollInterval  string
	addAuthMethod    string
	addDeployKey     string
	addSkipPrompts   bool
)

// addCmd represents the add command
var addCmd = &cobra.Command{
	Use:   "add [file]",
	Short: "Register a new application",
	Long: `Register a new application.
You can provide a JSON file, use flags, or run interactively.

Examples:
  # From JSON file
  conops-ctl apps add my-app.json

  # Using flags (non-interactive)
  conops-ctl apps add --name "MyApp" --repo "https://github.com/user/repo" --branch main --yes

  # Interactive mode (just run add)
  conops-ctl apps add`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var appData = make(map[string]interface{})

		// 1. If file is provided, use it
		if len(args) > 0 {
			filePath := args[0]
			fileData, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("error reading file: %v", err)
			}
			if err := json.Unmarshal(fileData, &appData); err != nil {
				return fmt.Errorf("invalid json file: %v", err)
			}
		} else {
			// 2. Otherwise, use flags or interactive mode
			// If -y is used, skip all prompts and use defaults/flags
			if addSkipPrompts {
				if addName == "" || addRepoURL == "" {
					return fmt.Errorf("name and repo are required when using --yes")
				}
				appData["name"] = addName
				appData["repo_url"] = addRepoURL
				if addBranch != "" { appData["branch"] = addBranch } else { appData["branch"] = "main" }
				if addComposePath != "" { appData["compose_path"] = addComposePath } else { appData["compose_path"] = "docker-compose.yml" }
				if addPollInterval != "" { appData["poll_interval"] = addPollInterval } else { appData["poll_interval"] = "30s" }
			} else {
				// Interactive Mode
				
				// Name
				if addName != "" {
					appData["name"] = addName
				} else {
					prompt := promptui.Prompt{
						Label: "App Name",
						Validate: func(input string) error {
							if len(input) == 0 {
								return fmt.Errorf("app name is required")
							}
							return nil
						},
					}
					result, err := prompt.Run()
					if err != nil { return err }
					appData["name"] = result
				}

				// Repo URL
				if addRepoURL != "" {
					appData["repo_url"] = addRepoURL
				} else {
					prompt := promptui.Prompt{
						Label: "Git Repository URL",
						Validate: func(input string) error {
							if len(input) == 0 {
								return fmt.Errorf("repo url is required")
							}
							return nil
						},
					}
					result, err := prompt.Run()
					if err != nil { return err }
					appData["repo_url"] = result
				}

				// Branch
				if addBranch != "" {
					appData["branch"] = addBranch
				} else {
					prompt := promptui.Prompt{
						Label:   "Branch",
						Default: "main",
					}
					result, err := prompt.Run()
					if err != nil { return err }
					appData["branch"] = result
				}

				// Compose Path
				if addComposePath != "" {
					appData["compose_path"] = addComposePath
				} else {
					prompt := promptui.Prompt{
						Label:   "Compose File Path",
						Default: "docker-compose.yml",
					}
					result, err := prompt.Run()
					if err != nil { return err }
					appData["compose_path"] = result
				}

				// Poll Interval
				if addPollInterval != "" {
					appData["poll_interval"] = addPollInterval
				} else {
					prompt := promptui.Prompt{
						Label:   "Poll Interval",
						Default: "30s",
					}
					result, err := prompt.Run()
					if err != nil { return err }
					appData["poll_interval"] = result
				}
			}

			// Auth Config (Flags only for now)
			if addAuthMethod != "" {
				appData["repo_auth_method"] = addAuthMethod
			} else {
				appData["repo_auth_method"] = "public"
			}
			if addDeployKey != "" {
				appData["deploy_key"] = addDeployKey
				appData["repo_auth_method"] = "deploy_key"
			}
		}

		client := NewClient()
		resp, err := client.Post("/api/v1/apps/", appData)
		if err != nil {
			return fmt.Errorf("error creating app: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return CheckResponse(resp)
		}

		var apiResp api.APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return fmt.Errorf("error decoding response: %v", err)
		}

		fmt.Println("App registered successfully.")
		return nil
	},
}

func init() {
	addCmd.Flags().StringVar(&addName, "name", "", "Application name")
	addCmd.Flags().StringVar(&addRepoURL, "repo", "", "Git repository URL")
	addCmd.Flags().StringVar(&addBranch, "branch", "", "Git branch (default: main)")
	addCmd.Flags().StringVar(&addComposePath, "compose-path", "", "Path to compose file (default: docker-compose.yml)")
	addCmd.Flags().StringVar(&addPollInterval, "poll-interval", "", "Sync interval (default: 30s)")
	addCmd.Flags().StringVar(&addAuthMethod, "auth-method", "", "Auth method: public or deploy_key")
	addCmd.Flags().StringVar(&addDeployKey, "deploy-key", "", "SSH private key for private repos")
	addCmd.Flags().BoolVarP(&addSkipPrompts, "yes", "y", false, "Skip interactive prompts (use defaults)")

	appsCmd.AddCommand(addCmd)
}
