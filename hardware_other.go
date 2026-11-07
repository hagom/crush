//go:build !linux

package main

// IsRotational siempre devuelve false en sistemas operativos no-Linux (macOS, Windows, BSD).
func IsRotational(path string) bool {
	return false
}
