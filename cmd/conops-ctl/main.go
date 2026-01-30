package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"
)

const defaultControllerURL = "http://localhost:8080"

// App represents the app structure for the CLI.
type App struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	RepoURL        string    `json:"repo_url"`
	Branch         string    `json:"branch"`
	Status         string    `json:"status"`
	LastSeenCommit string    `json:"last_seen_commit"`
	LastSyncAt     time.Time `json:"last_sync_at"`
}

func main() {
	controllerURL := os.Getenv("CONOPS_CONTROLLER_URL")
	if controllerURL == "" {
		controllerURL = defaultControllerURL
	}

	urlFlag := flag.String("url", controllerURL, "Controller URL")
	flag.Parse()

	if len(flag.Args()) < 1 {
		usage()
		os.Exit(1)
	}

	command := flag.Arg(0)

	switch command {
	case "apps":
		if len(flag.Args()) < 2 {
			fmt.Println("Usage: conops-ctl apps <list|add|delete>")
			os.Exit(1)
		}
		subcommand := flag.Arg(1)
		switch subcommand {
		case "list":
			listApps(*urlFlag)
		case "add":
			addApp(*urlFlag, flag.Args()[2:])
		case "delete":
			deleteApp(*urlFlag, flag.Args()[2:])
		default:
			fmt.Printf("Unknown subcommand: %s\n", subcommand)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("Usage: conops-ctl [flags] <command> [subcommand] [args]")
	fmt.Println("\nCommands:")
	fmt.Println("  apps list                  List registered applications")
	fmt.Println("  apps add <json-file>       Register a new application")
	fmt.Println("  apps delete <app-id>       Delete an application")
	fmt.Println("\nFlags:")
	fmt.Println("  -url string                Controller URL (default http://localhost:8080)")
}

// APIResponse matches the controller's response structure
type APIResponse struct {
	Message string `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func listApps(baseURL string) {
	resp, err := http.Get(baseURL + "/api/v1/apps/")
	if err != nil {
		fmt.Printf("Error fetching apps: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error: Received status code %d\n", resp.StatusCode)
		os.Exit(1)
	}

	// The controller returns { "data": [ ... ] }
	// We need to decode into a struct that matches that structure.
	var apiResp struct {
		Data []App `json:"data"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		fmt.Printf("Error decoding response: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tREPO\tBRANCH\tSTATUS\tLAST SYNC")
	for _, app := range apiResp.Data {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", app.Name, app.RepoURL, app.Branch, app.Status, app.LastSyncAt.Format(time.RFC3339))
	}
	w.Flush()
}

func addApp(baseURL string, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: conops-ctl apps add <json-file>")
		os.Exit(1)
	}

	filePath := args[0]
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	resp, err := http.Post(baseURL+"/api/v1/apps/", "application/json", bytes.NewBuffer(fileData))
	if err != nil {
		fmt.Printf("Error adding app: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error adding app (Status %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	fmt.Println("App registered successfully.")
}

func deleteApp(baseURL string, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: conops-ctl apps delete <app-id>")
		os.Exit(1)
	}

	appID := args[0]
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/apps/"+appID, nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		os.Exit(1)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error deleting app: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error deleting app (Status %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	fmt.Println("App deleted successfully.")
}
