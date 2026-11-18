//go:build !windows

package main

// ensureElevated is a no-op off Windows: Linux/macOS privilege handling lives
// in runOneTimeSetup (cap_net_admin / setuid on the core binary).
func ensureElevated() bool { return true }
