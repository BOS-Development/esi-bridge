// Package buildinfo reports facts about how the binary was built.
//
// Builds are development builds by default. A production release is built
// with the "release" build tag:
//
//	go build -tags release ./cmd/esi-bridge
//
// Development builds show extra developer-only options in the management UI
// (such as sending a test notification). Release builds leave them out
// entirely. The tag decides this at compile time, so a release binary has no
// way to turn them on.
package buildinfo
