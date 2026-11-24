package buildinfo

// BundledClientID is the EVE SSO client_id of the ESI Bridge application
// registered by the project. It's used unless the user ticks "Use a custom
// EVE SSO Client ID" in Settings. EVE SSO with PKCE has no client secret, so a
// client_id is not sensitive and is safe to ship in the binary.
//
// It's injected at build time rather than committed: the scripts/build-*
// scripts read it from a gitignored .clientid file at the repo root and pass
//
//	-ldflags "-X github.com/BOS-Development/esi-proxy/internal/buildinfo.BundledClientID=<id>"
//
// When it's empty (e.g. a local build with no ID filled in), the app falls
// back to requiring a custom client_id.
var BundledClientID = ""
