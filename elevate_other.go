//go:build !windows

package main

// ensureElevated is a no-op off Windows: Linux/macOS privilege handling lives
// in runOneTimeSetup (cap_net_admin / setuid on the core binary).
func ensureElevated() bool { return true }

// elevatedNow is always true here: hak istimewa TUN diatur di core, bukan di UI.
func elevatedNow() bool { return true }
