package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/dmdmdm-nz/tunneld/internal/api"
	"github.com/dmdmdm-nz/tunneld/internal/netmon"
	"github.com/dmdmdm-nz/tunneld/internal/rsd"
	"github.com/dmdmdm-nz/tunneld/internal/runtime"
	"github.com/dmdmdm-nz/tunneld/pkg/cli"
)

func main() {
	// Parse command line flags
	cfg := cli.ParseFlags()

	// Configure logging
	setLogLevel(cfg.LogLevel)
	log.SetFormatter(&log.TextFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		FullTimestamp:   true,
	})

	log.Infof("Config: Host=%s", cfg.Host)
	log.Infof("Config: Port=%d", cfg.Port)
	log.Infof("Config: LogLevel=%s", cfg.LogLevel)
	log.Infof("Config: AutoCreateTunnels=%v", cfg.AutoCreateTunnels)
	log.Infof("Config: InterfacePollInterval=%d second(s)", cfg.InterfacePollInterval)

	if os.Geteuid() != 0 {
		log.Fatal("The tunneld service must be run as root.")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	netmonSvc := netmon.NewService(3 * time.Second)
	rsdSvc := rsd.NewService()
	apiSvc := api.NewService(cfg.Host, cfg.Port, cfg.AutoCreateTunnels)

	// Wire subscriptions BEFORE starting producers to avoid missing anything.
	// RSD subscribes to Netmon.
	ifCh, ifUnsub := netmonSvc.Subscribe()
	rsdSvc.AttachNetmon(ifCh, ifUnsub)

	// API subscribes to RSD.
	rsdCh, rsdUnsub := rsdSvc.Subscribe()
	apiSvc.AttachRSD(rsdCh, rsdUnsub)

	// Start in dependency order: netif → rsd → tunnel
	super := runtime.NewSupervisor()
	super.Add("netmon", func(ctx context.Context) error { return netmonSvc.Start(ctx) }, netmonSvc.Close)
	super.Add("rsd", func(ctx context.Context) error { return rsdSvc.Start(ctx) }, rsdSvc.Close)
	super.Add("api", func(ctx context.Context) error { return apiSvc.Start(ctx) }, apiSvc.Close)

	if err := super.Start(ctx); err != nil {
		log.Error("supervisor start failed", "err", err)
		os.Exit(1)
	}
	if err := super.Wait(ctx); err != nil {
		log.Error("supervisor wait failed", "err", err)
		os.Exit(1)
	}
}

func setLogLevel(level string) {
	switch level {
	case "trace":
		log.SetLevel(log.TraceLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}
}
