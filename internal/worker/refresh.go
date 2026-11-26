// Package worker runs the background token-refresh loop so characters'
// access tokens stay valid without a proxy request having to wait on a
// refresh.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

const (
	tickInterval  = time.Minute
	refreshCutoff = 5 * time.Minute // refresh anything expiring within this window
)

// RunTokenRefresh polls for characters whose access token is about to
// expire and refreshes them. It blocks until ctx is canceled.
func RunTokenRefresh(ctx context.Context, s *store.Store, mgr *tokens.Manager, log *slog.Logger) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	refreshDue(ctx, s, mgr, log)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshDue(ctx, s, mgr, log)
		}
	}
}

func refreshDue(ctx context.Context, s *store.Store, mgr *tokens.Manager, log *slog.Logger) {
	cutoff := store.FormatTime(time.Now().Add(refreshCutoff))
	due, err := s.ListCharactersExpiringBefore(cutoff)
	if err != nil {
		log.Error("list characters expiring soon", "error", err)
		return
	}

	for _, char := range due {
		if _, err := mgr.Refresh(ctx, char); err != nil {
			if errors.Is(err, tokens.ErrReauthRequired) {
				// The character is now flagged and won't be listed again
				// until it logs in again, so this logs once, not every tick.
				log.Warn("character must log in again", "character_id", char.ID, "character_name", char.Name, "error", err)
				continue
			}
			log.Error("refresh character token", "character_id", char.ID, "character_name", char.Name, "error", err)
			continue
		}
		log.Info("refreshed character token", "character_id", char.ID, "character_name", char.Name)
	}
}
