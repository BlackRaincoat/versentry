package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	_ "time/tzdata" // embedded IANA zones for scratch image (no apk tzdata)

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core"
	"github.com/BlackRaincoat/versentry/internal/health"
	"github.com/BlackRaincoat/versentry/internal/logging"
	"github.com/BlackRaincoat/versentry/internal/version"
	_ "github.com/BlackRaincoat/versentry/internal/notifier/discord"
	_ "github.com/BlackRaincoat/versentry/internal/notifier/gotify"
	_ "github.com/BlackRaincoat/versentry/internal/notifier/ntfy"
	_ "github.com/BlackRaincoat/versentry/internal/notifier/stdout"
	_ "github.com/BlackRaincoat/versentry/internal/notifier/telegram"
	_ "github.com/BlackRaincoat/versentry/internal/notifier/webhook"
	_ "github.com/BlackRaincoat/versentry/internal/provider/docker"
	_ "github.com/BlackRaincoat/versentry/internal/registry/oci"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string
	var logLevel string

	root := &cobra.Command{
		Use:          "versentry",
		Short:        "Docker image update monitor",
		SilenceUsage: true, // runtime errors should not dump flag help
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "path to config file")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "log level: debug, info, warn, error (overrides config)")

	root.AddCommand(newCheckCmd(&configPath, &logLevel))
	root.AddCommand(newRunCmd(&configPath, &logLevel))
	root.AddCommand(newLinksCmd(&configPath, &logLevel))
	root.AddCommand(newHealthCmd(&configPath))
	root.AddCommand(newVersionCmd())

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.String())
		},
	}
}

func newCheckCmd(configPath, logLevel *string) *cobra.Command {
	return &cobra.Command{
		Use:          "check",
		Short:        "Run a single update check and exit",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			app, err := loadApp(cfg, *logLevel)
			if err != nil {
				return err
			}
			version.LogStartup(slog.Default())
			return app.Check(cmd.Context())
		},
	}
}

func newRunCmd(configPath, logLevel *string) *cobra.Command {
	return &cobra.Command{
		Use:          "run",
		Short:        "Run periodic update checks until interrupted",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}

			if err := health.Touch(*configPath, cfg.StateFile); err != nil {
				return fmt.Errorf("health stamp: %w", err)
			}

			app, err := loadApp(cfg, *logLevel)
			if err != nil {
				return fmt.Errorf("init app: %w", err)
			}
			version.LogStartup(slog.Default())

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			statePath := config.ResolveStatePath(*configPath, cfg.StateFile)
			return app.Run(ctx, cfg, *configPath, statePath)
		},
	}
}

func newLinksCmd(configPath, logLevel *string) *cobra.Command {
	return &cobra.Command{
		Use:          "links",
		Short:        "Print notification URLs for monitored containers (no registry calls)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			app, err := loadApp(cfg, *logLevel)
			if err != nil {
				return err
			}
			return app.Links(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func newHealthCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:          "health",
		Short:        "Check daemon liveness (Docker API ping + health stamp)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			return health.Check(cmd.Context(), cfg, *configPath)
		},
	}
}

func loadApp(cfg *config.Config, flagLogLevel string) (*core.App, error) {
	level, err := resolveLogLevel(cfg.LogLevel, flagLogLevel)
	if err != nil {
		return nil, err
	}

	log := logging.New(os.Stderr, level)
	slog.SetDefault(log)

	app, err := core.NewApp(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("init app: %w", err)
	}

	return app, nil
}

func resolveLogLevel(configLevel, flagLevel string) (slog.Level, error) {
	raw := configLevel
	if flagLevel != "" {
		raw = flagLevel
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", raw)
	}
}
