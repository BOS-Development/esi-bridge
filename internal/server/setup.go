package server

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

// handleClientIDForm serves a small local page for entering the EVE SSO
// client_id — the tray has no native text-input widget, so this is opened
// in the browser (the same way SSO login itself already requires a browser)
// instead of requiring the user to hand-edit config.json.
func (s *Server) handleClientIDForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<html><body style="font-family:sans-serif;max-width:32em;margin:4em auto;line-height:1.5">
<h2>esi-proxy: EVE SSO Client ID</h2>
<p>Register an application at
<a href="https://developers.eveonline.com">developers.eveonline.com</a> with
connection type <b>Authentication &amp; API Access</b> and callback URL
<code>%s</code>, then paste its Client ID below.</p>
<form method="POST">
<input name="client_id" value="%s" size="40" style="font-family:monospace;font-size:1em;padding:0.4em">
<button type="submit" style="padding:0.4em 1em">Save</button>
</form>
</body></html>`, html.EscapeString(s.redirectURL), html.EscapeString(s.ClientID()))
}

func (s *Server) handleSetClientID(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}

	clientID := strings.TrimSpace(r.FormValue("client_id"))
	if clientID == "" {
		http.Error(w, "client_id must not be empty", http.StatusBadRequest)
		return
	}

	if err := s.SetClientID(clientID); err != nil {
		s.log.Error("save client id", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.log.Info("eve client id updated")
	writeCallbackPage(w, "Client ID saved. You can close this window and use \"Add Character\" now.")
}
