package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/dmdmdm-nz/tunneld/internal/api"
	"github.com/dmdmdm-nz/tunneld/internal/netmon"
	"github.com/dmdmdm-nz/tunneld/internal/rsd"
	"github.com/dmdmdm-nz/tunneld/internal/runtime"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
	"github.com/dmdmdm-nz/tunneld/internal/tunnelmgr"
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

	if os.Geteuid() != 0 {
		log.Fatal("The tunneld service must be run as root.")
	}

	// Initialize remoted monitoring (macOS only)
	tunnel.InitRemoted()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	netmonSvc := netmon.NewService()
	rsdSvc := rsd.NewService()
	tunnelMgr := tunnelmgr.NewManager(cfg.AutoCreateTunnels)
	apiSvc := api.NewService(cfg.Host, cfg.Port)

	// Wire subscriptions BEFORE starting producers to avoid missing anything.
	// RSD subscribes to Netmon.
	ifCh, ifUnsub := netmonSvc.Subscribe()
	rsdSvc.AttachNetmon(ifCh, ifUnsub)

	// TunnelMgr subscribes to RSD.
	rsdCh, rsdUnsub := rsdSvc.Subscribe()
	tunnelMgr.AttachRSD(rsdCh, rsdUnsub)

	// API attaches to TunnelMgr.
	apiSvc.AttachTunnelMgr(tunnelMgr)

	// Start in dependency order: netmon → rsd → tunnelmgr → api
	super := runtime.NewSupervisor()
	super.Add("netmon", func(ctx context.Context) error { return netmonSvc.Start(ctx) }, netmonSvc.Close)
	super.Add("rsd", func(ctx context.Context) error { return rsdSvc.Start(ctx) }, rsdSvc.Close)
	super.Add("tunnelmgr", func(ctx context.Context) error { return tunnelMgr.Start(ctx) }, tunnelMgr.Close)
	super.Add("api", func(ctx context.Context) error { return apiSvc.Start(ctx) }, apiSvc.Close)

	if err := super.Start(ctx); err != nil {
		log.Error("supervisor start failed", "err", err)
		os.Exit(1)
	}
	if err := super.Wait(ctx); err != nil {
		log.Error("supervisor wait failed", "err", err)
		os.Exit(1)
	}

	// Ensure remoted is resumed on shutdown in case it was left suspended
	if err := tunnel.ForceResumeRemoted(); err != nil {
		log.WithError(err).Warn("Failed to resume remoted service during shutdown")
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
