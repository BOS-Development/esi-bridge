// Command esi-proxy runs a local proxy for EVE Online's ESI API: it holds
// character SSO tokens, caches ESI responses, and gates external
// applications behind a tray-based approval flow. See README.md.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/cache"
	"github.com/BOS-Development/esi-proxy/internal/config"
	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/server"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
	"github.com/BOS-Development/esi-proxy/internal/tray"
	"github.com/BOS-Development/esi-proxy/internal/urlscheme"
	"github.com/BOS-Development/esi-proxy/internal/worker"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], urlscheme.Scheme+"://") {
		// Windows/Linux invoke us as a brand-new process with the
		// eveauth-proxy:// callback URL as argv[1] (macOS instead delivers
		// it to the already-running process via an Apple Event — see
		// internal/urlscheme). This invocation's only job is to hand the
		// URL to that already-running instance and exit; it must not touch
		// the database, HTTP server, or tray.
		if err := runForwardCallback(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "forward callback failed:", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--window":
			// Child invocation that hosts the management UI in a native
			// window. Dispatched here, before run() starts any goroutine, so
			// it keeps the process's initial OS thread for the native UI run
			// loop (see internal/window).
			if err := runWindow(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "window failed:", err)
				os.Exit(1)
			}
			return
		case "export":
			if err := runExport(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "export failed:", err)
				os.Exit(1)
			}
			return
		case "import":
			if err := runImport(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "import failed:", err)
				os.Exit(1)
			}
			return
		case "mdns":
			if err := runMDNS(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "mdns:", err)
				os.Exit(1)
			}
			return
		case "serve-ui", "--serve-ui":
			// Dev-only: start just the HTTP server so the management UI at
			// /app can be previewed in a browser, without the tray.
			if err := runServeUI(log); err != nil {
				log.Error("serve-ui", "error", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			printUsage()
			return
		}
	}

	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`esi-proxy — local proxy for EVE Online's ESI API

Usage:
  esi-proxy                                  run the proxy (tray + HTTP server)
  esi-proxy export --out=bundle.enc          export accounts/characters/tokens
  esi-proxy import --in=bundle.enc           import a bundle onto this machine
  esi-proxy mdns announce [--duration D]     debug: send the mDNS announcement by hand
  esi-proxy mdns browse   [--duration D]     debug: discover esi-proxy services on the LAN
  esi-proxy serve-ui                         dev: serve the management UI only (no tray), at /app
  esi-proxy --window --url URL               internal: host the management UI in a native
                                              window (spawned by the running instance)
  esi-proxy eveauth-proxy://callback?...     internal: invoked by the OS to deliver
                                              an SSO callback to the running instance`)
}

// runForwardCallback is the entry point for a process invocation whose only
// job is handing an eveauth-proxy:// callback URL to the already-running
// instance. It never touches the database, HTTP server, or tray.
func runForwardCallback(rawURL string) error {
	dataDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return forwardCallback(cfg.Port, rawURL)
}

// forwardCallback replays an eveauth-proxy://callback URL's query string
// against the running instance's unchanged GET /sso/callback handler.
func forwardCallback(port int, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse callback url: %w", err)
	}
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/sso/callback?%s", port, u.RawQuery))
	if err != nil {
		return fmt.Errorf("forward to running instance on port %d (is esi-proxy running?): %w", port, err)
	}
	defer resp.Body.Close()
	return nil
}

func run(log *slog.Logger) error {
	dataDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.EVEClientID == "" {
		log.Warn("eve_client_id is not set; use \"Set EVE Client ID\" in the tray before adding characters")
	}

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	masterKey, err := cryptox.MasterKey(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}

	urlscheme.Register(log)

	st := store.New(conn)
	redirectURL := urlscheme.Scheme + "://callback"
	sso := esi.NewSSOHolder(esi.NewSSO(cfg.EVEClientID, cfg.EVESSOBaseURL, redirectURL, server.LoginScopes))
	esiClient := esi.NewClient(cfg.ESIBaseURL)
	respCache := cache.New()
	tokenMgr := tokens.NewManager(st, sso, masterKey)

	srv := server.New(st, sso, esiClient, respCache, tokenMgr, log, cfg.DataDir, cfg.EVESSOBaseURL, redirectURL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// netw owns the HTTP listener and the mDNS advert, which lets external
	// apps (Pyfa, MCO, …) discover the proxy without a hand-configured
	// host:port. It rebinds both live when LAN access is toggled in the tray.
	netw := newNetwork(log, cfg.Port, srv.Handler(), srv.SetBindHost)
	if err := netw.Start(ctx, cfg.BindHost); err != nil {
		log.Error("http server", "error", err)
	}

	go worker.RunTokenRefresh(ctx, st, tokenMgr, log)

	go sweepCache(ctx, respCache)

	// The management UI lives in a native window hosted by a child process
	// (see window.go / internal/window). Open it on launch so the app
	// presents a window like any desktop app, and hand the tray the same
	// launcher so "Open ESI Proxy" can re-open it after it's been closed. The
	// loopback /app page stays reachable from a browser regardless.
	winLauncher := newWindowLauncher(cfg.Port, log)
	go winLauncher.Open()
	defer winLauncher.Close()

	// onCocoaReady fires from inside the tray's onReady — after the native
	// event loop (and on macOS, NSApplication's own startup) has finished.
	// On macOS this is what receives an eveauth-proxy:// callback URL when
	// the OS delivers it to this already-running process (an Apple Event)
	// rather than spawning a new one (Windows/Linux instead go through
	// runForwardCallback in a separate process). Installing it any earlier
	// (e.g. before systray.Run) doesn't work: NSApplication's own default
	// URL-event handler is installed during its startup and would silently
	// overwrite ours. No-op on non-macOS.
	onCocoaReady := func() {
		urlscheme.InstallHandler(func(rawURL string) {
			if err := forwardCallback(cfg.Port, rawURL); err != nil {
				log.Error("handle eveauth-proxy callback", "error", err)
			}
		})
	}

	// systray.Run must be called on the process's main goroutine: it pins
	// itself to the initial OS thread (via the systray package's own
	// init()), and on macOS Cocoa's run loop only works on that thread.
	// Quitting the tray (Quit clicked, or ctx canceled) is what unblocks
	// this call; shutdown continues below once it returns.
	tray.New(st, srv, log, cfg.Port, onCocoaReady, winLauncher.Open).Run(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	netw.Shutdown(shutdownCtx)

	return nil
}

// runServeUI starts only the HTTP server — no tray, no mDNS advertising, no
// token-refresh worker — bound to localhost, so the management UI at /app can
// be previewed in a browser during development. It uses the real config and
// database, so characters/grants/settings behave exactly as in the full app
// (only SSO login won't complete, since that needs the .app bundle's custom
// URL scheme). Blocks until interrupted.
func runServeUI(log *slog.Logger) error {
	dataDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	masterKey, err := cryptox.MasterKey(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}

	st := store.New(conn)
	redirectURL := urlscheme.Scheme + "://callback"
	sso := esi.NewSSOHolder(esi.NewSSO(cfg.EVEClientID, cfg.EVESSOBaseURL, redirectURL, server.LoginScopes))
	esiClient := esi.NewClient(cfg.ESIBaseURL)
	respCache := cache.New()
	tokenMgr := tokens.NewManager(st, sso, masterKey)
	srv := server.New(st, sso, esiClient, respCache, tokenMgr, log, cfg.DataDir, cfg.EVESSOBaseURL, redirectURL)

	// Always localhost for the preview — the UI surface is loopback-only
	// regardless, and this keeps the dev server off the network.
	addr := fmt.Sprintf("localhost:%d", cfg.Port)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server", "error", err)
			stop()
		}
	}()

	fmt.Printf("ESI Proxy management UI (dev, no tray): http://%s/app\n", addr)
	fmt.Println("Press Ctrl-C to stop.")

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func sweepCache(ctx context.Context, c *cache.Cache) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Sweep()
		}
	}
}
