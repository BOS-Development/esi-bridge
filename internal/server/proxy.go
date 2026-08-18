package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/BOS-Development/esi-proxy/internal/cache"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
)

// hopByHopHeaders are stripped when relaying the ESI response back to the
// caller, per RFC 7230 §6.1.
var hopByHopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	app, err := s.authenticateApp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	characterID, err := strconv.ParseInt(r.PathValue("character_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid character_id", http.StatusBadRequest)
		return
	}

	if _, err := s.store.GetActiveGrant(app.ID, characterID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = s.store.UpsertPendingGrant(app.ID, characterID, nil)
			http.Error(w, "access to this character has not been approved yet; call /api/request-access or check the tray", http.StatusForbidden)
			return
		}
		s.log.Error("get active grant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	esiPath := "/" + r.PathValue("esi_path")
	cacheable := r.Method == http.MethodGet
	key := cache.Key(characterID, r.Method, esiPath, r.URL.RawQuery)

	if cacheable {
		if entry, ok := s.cache.Get(key); ok {
			writeUpstream(w, entry.StatusCode, entry.Header, entry.Body)
			return
		}
	}

	accessToken, err := s.tokenMgr.EnsureAccessToken(r.Context(), characterID)
	if err != nil {
		s.log.Error("ensure access token", "character_id", characterID, "error", err)
		http.Error(w, "could not obtain a valid access token for this character", http.StatusBadGateway)
		return
	}

	resp, err := s.esiClient.Forward(r.Context(), accessToken, r.Method, esiPath, r.URL.RawQuery, r.Body)
	if err != nil {
		if errors.Is(err, esi.ErrRateLimited) {
			http.Error(w, "esi error limit exhausted, try again shortly", http.StatusTooManyRequests)
			return
		}
		s.log.Error("forward to esi", "error", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.log.Error("read esi response body", "error", err)
		http.Error(w, "upstream response error", http.StatusBadGateway)
		return
	}

	if cacheable && resp.StatusCode < 400 {
		if expiresAt := cache.ParseExpires(resp.Header); !expiresAt.IsZero() {
			s.cache.Set(key, cache.Entry{StatusCode: resp.StatusCode, Header: resp.Header, Body: body, ExpiresAt: expiresAt})
		}
	}

	writeUpstream(w, resp.StatusCode, resp.Header, body)
}

func writeUpstream(w http.ResponseWriter, status int, header http.Header, body []byte) {
	dst := w.Header()
	for k, v := range header {
		dst[k] = v
	}
	for _, h := range hopByHopHeaders {
		dst.Del(h)
	}
	w.WriteHeader(status)
	w.Write(body)
}
