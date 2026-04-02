package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"basemap-aio/internal/basemap"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "update":
		if err := runUpdate(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "watch":
		if err := runWatch(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "status":
		if err := runStatus(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "proxy":
		if err := runProxy(os.Args[2:]); err != nil {
			exitErr(err)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "redownload the latest build even if it is already current")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := basemap.UpdaterConfigFromEnv()
	updater := basemap.NewUpdater(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := updater.Update(ctx, *force)
	if err != nil {
		return err
	}

	fmt.Println(result.Key)
	return nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	interval := fs.Duration("interval", 0, "update interval (default: UPDATE_INTERVAL env or 24h)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := basemap.UpdaterConfigFromEnv()
	updater := basemap.NewUpdater(cfg)

	if *interval <= 0 {
		*interval = cfg.UpdateInterval
	}
	if *interval <= 0 {
		*interval = 24 * time.Hour
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := updater.Watch(ctx, *interval); err != nil {
		return err
	}
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := basemap.UpdaterConfigFromEnv()
	status, err := basemap.LoadStatus(cfg)
	if err != nil {
		return err
	}

	return basemap.WriteJSON(os.Stdout, status)
}

func runProxy(args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", "", "listen address")
	upstream := fs.String("upstream", "", "upstream pmtiles server URL")
	apiKey := fs.String("api-key", "", "API key required for access")
	dataDir := fs.String("data-dir", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := basemap.ProxyConfigFromEnv()
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *upstream != "" {
		cfg.UpstreamURL = *upstream
	}
	if *apiKey != "" {
		cfg.APIKey = *apiKey
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return basemap.RunProxy(ctx, cfg)
}

func usage() {
	fmt.Fprintln(os.Stderr, `basemapctl commands:
  update [--force]
  watch [--interval=24h]
  status
  proxy [--listen=:8080] [--upstream=http://tiles:8081]`)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
