package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/conops/conops/internal/api"
	"github.com/spf13/viper"
)

// APIClient handles communication with the Conops controller
type APIClient struct {
	BaseURL string
	Client  *http.Client
}

// NewClient creates a new APIClient
func NewClient() *APIClient {
	return &APIClient{
		BaseURL: viper.GetString("url"),
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Get performs a GET request
func (c *APIClient) Get(path string) (*http.Response, error) {
	return c.Client.Get(c.BaseURL + path)
}

// Post performs a POST request
func (c *APIClient) Post(path string, body interface{}) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.Client.Post(c.BaseURL+path, "application/json", bytes.NewBuffer(jsonBody))
}

// Patch performs a PATCH request
func (c *APIClient) Patch(path string, body interface{}) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPatch, c.BaseURL+path, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.Client.Do(req)
}

// Delete performs a DELETE request
func (c *APIClient) Delete(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.Client.Do(req)
}

// CheckResponse checks the API response for errors
func CheckResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	var apiResp api.APIResponse
	if err := json.Unmarshal(body, &apiResp); err == nil && apiResp.Message != "" {
		return fmt.Errorf("API Error (%d): %s", resp.StatusCode, apiResp.Message)
	}

	return fmt.Errorf("API Error (%d): %s", resp.StatusCode, string(body))
}

// PrintJSON prints data as JSON
func PrintJSON(data interface{}) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		fmt.Printf("Error encoding JSON: %v\n", err)
	}
}
