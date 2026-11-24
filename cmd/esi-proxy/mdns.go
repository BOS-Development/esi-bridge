package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/config"
	"github.com/BOS-Development/esi-proxy/internal/discovery"
)

// runMDNS is a debug entry point for exercising mDNS/DNS-SD by hand, separate
// from the normal `run` path that advertises automatically. It supports two
// modes:
//
//	esi-proxy mdns announce   send the service announcement (and keep answering
//	                          queries) for --duration, then withdraw it
//	esi-proxy mdns browse     listen for ESI Bridge services on the network for
//	                          --duration and print what's found
//
// announce defaults the port and base_url to the saved config, so it mirrors
// exactly what the running app would publish; override with the flags to test
// other values.
func runMDNS(args []string) error {
	fs := flag.NewFlagSet("mdns", flag.ExitOnError)
	port := fs.Int("port", 0, "port to advertise (default: the configured proxy port)")
	baseURL := fs.String("base-url", "", "base_url TXT value to advertise (default: derived from the configured bind host and port)")
	duration := fs.Duration("duration", 15*time.Second, "how long to announce or browse (0 = until Ctrl-C)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: esi-proxy mdns <announce|browse> [flags]

  announce   send the mDNS announcement and answer queries
  browse     discover ESI Bridge services on the local network

flags:
`)
		fs.PrintDefaults()
	}

	// The mode is the first positional arg; flags follow it.
	mode := "announce"
	if len(args) > 0 && !isFlag(args[0]) {
		mode, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	switch mode {
	case "announce":
		return mdnsAnnounce(log, *port, *baseURL, *duration)
	case "browse":
		return mdnsBrowse(*duration)
	default:
		fs.Usage()
		return fmt.Errorf("unknown mdns mode %q (want announce or browse)", mode)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func mdnsAnnounce(log *slog.Logger, port int, baseURL string, duration time.Duration) error {
	// Fill in whatever wasn't overridden from the saved config, so by default
	// this announces exactly what the running app would.
	var ip net.IP
	if port == 0 || baseURL == "" {
		dataDir, err := config.Dir()
		if err != nil {
			return fmt.Errorf("resolve config dir: %w", err)
		}
		cfg, err := config.Load(dataDir)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if port == 0 {
			port = cfg.Port
		}
		if baseURL == "" {
			var lanIP net.IP
			if isWildcard(cfg.BindHost) {
				lanIP = discovery.LANIP()
			}
			baseURL, ip = connectTarget(cfg.BindHost, port, lanIP)
		}
	}

	stop, err := discovery.Advertise(log, port, baseURL, ip)
	if err != nil {
		return fmt.Errorf("advertise: %w", err)
	}
	defer stop()

	fmt.Printf("Announcing %s on port %d (base_url=%s). ", discovery.ServiceType, port, baseURL)
	if duration == 0 {
		fmt.Println("Press Ctrl-C to stop.")
	} else {
		fmt.Printf("Withdrawing in %s (Ctrl-C to stop sooner).\n", duration)
	}

	waitForDeadlineOrSignal(duration)
	fmt.Println("Withdrawn.")
	return nil
}

// mdnsBrowse shells out to the host OS's own browse tool (the same system
// responder the proxy now registers with), streaming its output. Using the
// native tool keeps the debug view consistent with what the OS and other
// devices actually see.
func mdnsBrowse(duration time.Duration) error {
	if duration == 0 {
		duration = 15 * time.Second // browsing forever isn't useful; cap it
	}

	name, args, err := browseCommand()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	fmt.Printf("Browsing via %q for %s (Ctrl-C to stop)...\n", name+" "+strings.Join(args, " "), duration)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	// A deadline/Ctrl-C kill is the normal way an open-ended browse ends, so
	// don't surface that as a failure.
	if ctx.Err() != nil {
		return nil
	}
	if runErr != nil {
		return fmt.Errorf("run %s: %w", name, runErr)
	}
	return nil
}

// browseCommand returns the OS-native DNS-SD browse tool and its arguments.
func browseCommand() (string, []string, error) {
	switch runtime.GOOS {
	case "darwin", "windows":
		return "dns-sd", []string{"-B", discovery.ServiceType, "local."}, nil
	case "linux":
		return "avahi-browse", []string{"-rt", discovery.ServiceType}, nil
	default:
		return "", nil, fmt.Errorf("mdns browse is not supported on %s", runtime.GOOS)
	}
}

// waitForDeadlineOrSignal blocks until duration elapses (if non-zero) or an
// interrupt/termination signal arrives.
func waitForDeadlineOrSignal(duration time.Duration) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if duration == 0 {
		<-ctx.Done()
		return
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
