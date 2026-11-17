package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/browser"
	"github.com/BOS-Development/esi-proxy/internal/window"
)

// runWindow is the entry point for the `--window` child invocation: it hosts
// the management UI in a native window and blocks until the user closes it.
// main() dispatches here before starting any goroutine, so this runs on the
// process's initial OS thread — the thread the native UI run loop requires
// (see internal/window).
func runWindow(args []string) error {
	fs := flag.NewFlagSet("window", flag.ExitOnError)
	url := fs.String("url", "", "management UI URL to display in the window")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" {
		return errors.New("--window requires --url")
	}
	return window.Open("ESI Proxy", *url)
}

// windowLauncher opens and tracks the management window child process on
// behalf of the main (tray + server) process. The window can't share the
// tray's process because both need the single main-thread native run loop, so
// it's spawned as `esi-proxy --window --url …` of this same binary.
type windowLauncher struct {
	exe  string
	port int
	log  *slog.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

func newWindowLauncher(port int, log *slog.Logger) *windowLauncher {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = os.Args[0]
	}
	return &windowLauncher{exe: exe, port: port, log: log}
}

// Open shows the management UI. With a native window available it spawns the
// window child process (unless one is already open); otherwise — a CGO-less
// build — it falls back to the user's browser so the UI stays reachable. It
// first waits briefly for the loopback server to answer, so the window never
// opens onto a connection error during startup.
func (l *windowLauncher) Open() {
	appURL := fmt.Sprintf("http://localhost:%d/app", l.port)

	if !window.Supported {
		if err := browser.Open(appURL); err != nil {
			l.log.Error("open management UI in browser", "error", err)
		}
		return
	}

	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		// A window is already open. Another process's window can't be
		// refocused portably, so leave the existing one in place rather than
		// spawning a second.
		l.log.Info("management window already open")
		return
	}
	l.mu.Unlock()

	l.waitReady()

	cmd := exec.Command(l.exe, "--window", "--url", appURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		l.log.Error("open management window", "error", err)
		return
	}

	l.mu.Lock()
	l.cmd = cmd
	l.running = true
	l.mu.Unlock()

	// Reap the child and clear state when the user closes the window, so a
	// later Open spawns a fresh one.
	go func() {
		_ = cmd.Wait()
		l.mu.Lock()
		l.running = false
		l.cmd = nil
		l.mu.Unlock()
	}()
}

// Close terminates the window child process if one is running. Called on
// shutdown so quitting from the tray takes the window down with it instead of
// leaving an orphaned window behind.
func (l *windowLauncher) Close() {
	l.mu.Lock()
	cmd := l.cmd
	l.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// waitReady polls the loopback /healthz endpoint until it answers or a short
// deadline passes, so the window doesn't briefly show a connection error when
// opened during startup. It returns regardless once the deadline is hit —
// opening onto an error is better than hanging.
func (l *windowLauncher) waitReady() {
	healthURL := fmt.Sprintf("http://localhost:%d/healthz", l.port)
	client := &http.Client{Timeout: 300 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
