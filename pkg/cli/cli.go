package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/dmdmdm-nz/tunneld/pkg/version"
)

// Config holds the application configuration from CLI flags
type Config struct {
	Port              int
	Host              string
	AutoCreateTunnels bool
	LogLevel          string
}

// ParseFlags parses command line arguments and returns a Config
func ParseFlags() *Config {
	cfg := &Config{}

	flag.IntVar(&cfg.Port, "port", 60105, "Port to listen on") // 60-105 is leetspeek for go-ios :-D
	flag.StringVar(&cfg.Host, "host", "127.0.0.1", "Host to bind to")
	flag.BoolVar(&cfg.AutoCreateTunnels, "auto-create-tunnels", false, "Automatically create tunnels for new network interfaces")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "Log level (trace, debug, info, warn, error)")
	showVersion := flag.Bool("version", false, "Show version information")

	flag.Parse()

	if *showVersion {
		fmt.Printf("tunneld version %s (commit: %s, built at: %s)\n",
			version.Version,
			version.CommitHash,
			version.BuildTime)
		os.Exit(0)
	}

	return cfg
}

// String returns a string representation of the Config
func (c *Config) String() string {
	return fmt.Sprintf("Host: %s, Port: %d, AutoCreateTunnels: %t, LogLevel: %s", c.Host, c.Port, c.AutoCreateTunnels, c.LogLevel)
}
