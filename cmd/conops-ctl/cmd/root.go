package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	controllerURL string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "conops-ctl",
	Short: "Command line interface for the Conops platform",
	Long:  `CLI for managing Conops applications (Git-based Docker Compose sync).`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	rootCmd.PersistentFlags().StringVar(&controllerURL, "url", "http://localhost:8080", "Controller URL")

	// Bind flags to viper
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
}

// initConfig reads in ENV variables if set.
func initConfig() {
	viper.SetEnvPrefix("CONOPS")
	viper.AutomaticEnv() // read in environment variables that match
}
