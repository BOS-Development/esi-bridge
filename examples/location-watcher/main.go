// Command location-watcher is a small, self-contained example of an external
// application that talks to a running esi-proxy. It walks the full approval
// flow a real app would:
//
//  1. detect that the proxy is running (GET /healthz);
//  2. register itself and request the list_characters permission, waiting for
//     the user to approve it from the proxy's tray/window;
//  3. list the characters and let the user pick one;
//  4. request that character's location and ship-type scopes, again waiting
//     for approval;
//  5. display the character's current ship and location, refreshing each on
//     its own ESI cache timeout (the proxy relays ESI's Expires header, so the
//     app refetches exactly when the cached copy goes stale).
//
// It uses only the standard library and the proxy's public HTTP API, so it
// doubles as a reference an external developer can copy out of the repo.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Scopes requested for the selected character. read_location and
// read_ship_type cover the two pieces of data this app shows; read_structures
// lets it resolve the name of a player-owned structure the character may be
// docked in (public station/system/type names need no scope).
var watchScopes = []string{
	"esi-location.read_location.v1",
	"esi-location.read_ship_type.v1",
	"esi-universe.read_structures.v1",
}

// fallbackInterval is how long to wait before refetching when a response
// carries no usable Expires header, and the floor applied to any Expires that
// has already passed — so a stale/absent header can never spin the loop.
const fallbackInterval = 5 * time.Second

func main() {
	proxyURL := flag.String("proxy", "http://localhost:8085", "base URL of the esi-proxy to connect to")
	appName := flag.String("name", "ESI Location Watcher", "name this app registers with the proxy under")
	pollInterval := flag.Duration("poll", 2*time.Second, "how often to re-check for the user's approval while waiting")
	reset := flag.Bool("reset", false, "forget the saved api_key for this proxy and register anew")
	flag.Parse()

	// Ctrl-C cancels the whole run, including the watch loop, cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *proxyURL, *appName, *pollInterval, *reset); err != nil {
		if ctx.Err() != nil {
			fmt.Println("\nStopped.")
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, proxyURL, appName string, pollInterval time.Duration, reset bool) error {
	c := newClient(proxyURL)

	// 1. Detect the proxy.
	fmt.Printf("Looking for esi-proxy at %s ... ", c.BaseURL)
	if err := c.Healthz(); err != nil {
		fmt.Println("not found.")
		return fmt.Errorf("could not reach the proxy (is esi-proxy running?): %w", err)
	}
	fmt.Println("found.")

	// 2. Register (reusing a saved api_key so permissions survive restarts).
	if err := ensureRegistered(c, appName, reset); err != nil {
		return err
	}

	// 3. Get the list_characters permission, then list and choose.
	if err := ensurePermission(ctx, c, pollInterval, "list_characters"); err != nil {
		return err
	}
	chars, err := c.ListCharacters()
	if err != nil {
		return fmt.Errorf("list characters: %w", err)
	}
	if len(chars) == 0 {
		return fmt.Errorf("the proxy has no characters yet — add one from its tray/window first")
	}
	char, err := chooseCharacter(ctx, chars)
	if err != nil {
		return err
	}

	// 4. Get access to that character's location and ship scopes.
	if err := ensureAccess(ctx, c, pollInterval, char); err != nil {
		return err
	}

	// 5. Watch, refreshing on each endpoint's cache timeout.
	fmt.Printf("\nWatching %s — refreshing on each ESI cache timeout. Press Ctrl-C to stop.\n", char.Name)
	return watch(ctx, c, char)
}

// ensureRegistered loads a previously saved api_key for this proxy, or
// registers for a new one and saves it. Reusing the key means the user only
// approves this app's permissions once, not on every run.
func ensureRegistered(c *Client, appName string, reset bool) error {
	state := loadState()
	if !reset {
		if key := state.keyFor(c.BaseURL); key != "" {
			c.APIKey = key
			return nil
		}
	}

	fmt.Printf("Registering with the proxy as %q ... ", appName)
	if err := c.Register(appName); err != nil {
		fmt.Println("failed.")
		return fmt.Errorf("register: %w", err)
	}
	fmt.Println("done.")

	state.setKey(c.BaseURL, c.APIKey)
	if err := state.save(); err != nil {
		// Non-fatal: we can still run this session, we just won't remember the
		// key next time (the user would re-approve on the next run).
		fmt.Fprintln(os.Stderr, "warning: could not save api_key:", err)
	}
	return nil
}

// ensurePermission requests an app-wide permission and blocks until the user
// approves it, re-checking every pollInterval.
func ensurePermission(ctx context.Context, c *Client, pollInterval time.Duration, perm string) error {
	statuses, err := c.RequestPermissions(perm)
	if err != nil {
		return fmt.Errorf("request %s permission: %w", perm, err)
	}
	if statuses[perm] == "granted" {
		return nil
	}

	fmt.Printf("Waiting for you to approve the %q permission in the proxy's tray/window ...\n", perm)
	for {
		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
		statuses, err := c.RequestPermissions(perm)
		if err != nil {
			return fmt.Errorf("poll %s permission: %w", perm, err)
		}
		if statuses[perm] == "granted" {
			fmt.Printf("Permission %q granted.\n", perm)
			return nil
		}
	}
}

// ensureAccess requests the watch scopes for char and blocks until the user
// approves them.
func ensureAccess(ctx context.Context, c *Client, pollInterval time.Duration, char Character) error {
	status, err := c.RequestAccess(char.ID, watchScopes...)
	if err != nil {
		return fmt.Errorf("request access to %s: %w", char.Name, err)
	}
	if status == "granted" {
		return nil
	}

	fmt.Printf("Waiting for you to approve access to %s in the proxy's tray/window ...\n", char.Name)
	for {
		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
		status, err := c.RequestAccess(char.ID, watchScopes...)
		if err != nil {
			return fmt.Errorf("poll access to %s: %w", char.Name, err)
		}
		if status == "granted" {
			fmt.Printf("Access to %s granted.\n", char.Name)
			return nil
		}
	}
}

// chooseCharacter prints the character list and reads a selection from stdin.
func chooseCharacter(ctx context.Context, chars []Character) (Character, error) {
	fmt.Println("\nCharacters on the proxy:")
	for i, ch := range chars {
		fmt.Printf("  %2d. %s\n", i+1, ch.Name)
	}
	if len(chars) == 1 {
		fmt.Printf("Selecting the only character: %s\n", chars[0].Name)
		return chars[0], nil
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		if ctx.Err() != nil {
			return Character{}, ctx.Err()
		}
		fmt.Printf("Select a character [1-%d]: ", len(chars))
		line, err := reader.ReadString('\n')
		if err != nil {
			return Character{}, fmt.Errorf("read selection: %w", err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || n < 1 || n > len(chars) {
			fmt.Println("Please enter a number from the list.")
			continue
		}
		return chars[n-1], nil
	}
}

// watch refetches the character's ship and location, each refreshing on its
// own ESI cache timeout. Because the proxy relays ESI's Expires header
// (serving a cached copy until then), scheduling the next fetch at the sooner
// of the two expiries means we refetch right as the data can actually change.
func watch(ctx context.Context, c *Client, char Character) error {
	names := newNames(c, char.ID)
	for {
		next := time.Now().Add(fallbackInterval)

		ship, shipRes, err := c.Ship(char.ID)
		if err != nil {
			return fmt.Errorf("get ship: %w", err)
		}
		loc, locRes, err := c.Location(char.ID)
		if err != nil {
			return fmt.Errorf("get location: %w", err)
		}

		render(char, ship, loc, names)

		next = earliest(next, shipRes.Expires, locRes.Expires)
		wait := untilRefresh(time.Now(), next)
		fmt.Printf("  (next refresh in %s)\n", wait.Round(time.Second))
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// render prints one refreshed snapshot of the character's ship and location,
// resolving IDs to names best-effort.
func render(char Character, ship Ship, loc Location, names *Names) {
	fmt.Printf("\n[%s] %s\n", time.Now().Format("15:04:05"), char.Name)
	fmt.Printf("  Ship:     %s (%s)\n", ship.Name, names.shipType(ship.TypeID))
	fmt.Printf("  System:   %s\n", names.system(loc.SolarSystemID))
	switch {
	case loc.StationID != 0:
		fmt.Printf("  Docked:   %s\n", names.station(loc.StationID))
	case loc.StructureID != 0:
		fmt.Printf("  Docked:   %s\n", names.structure(loc.StructureID))
	default:
		fmt.Printf("  Docked:   (in space)\n")
	}
}

// untilRefresh returns how long to wait before refetching, given the target
// instant. It floors the wait at fallbackInterval so an already-expired or
// skewed timestamp can never busy-loop the watcher.
func untilRefresh(now, target time.Time) time.Duration {
	d := target.Sub(now)
	if d < fallbackInterval {
		return fallbackInterval
	}
	return d
}

// earliest returns the soonest non-zero time among the arguments, or base if
// none of the rest is set or sooner.
func earliest(base time.Time, times ...time.Time) time.Time {
	soonest := base
	for _, t := range times {
		if !t.IsZero() && t.Before(soonest) {
			soonest = t
		}
	}
	return soonest
}

// sleep waits for d or until ctx is cancelled, returning ctx.Err() if it was.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
