package cli

import (
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:           "fodmap-detector",
	Short:         "A CLI tool to process FODMAP data.",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return viper.BindPFlags(cmd.Flags())
	},
}

func init() {
	cobra.OnInitialize(initConfig)
	// Register the persistent --river-schema flag (default "river"). Bound
	// to viper + RIVER_SCHEMA env so every River client/migrator site reads
	// the same value via cli.riverSchemaName().
	addRiverSchemaFlag(rootCmd)
}

func initConfig() {
	viper.SetConfigName("service")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			slog.Warn("error reading config file", "error", err)
		}
	}
}

// Execute runs the root cobra command and exits with a non-zero status on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
