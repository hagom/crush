package main

import (
	"fmt"
	"os"
	"time"
)

// NotifyTaskComplete sends a desktop notification and/or terminal bell upon task completion.
// Triggers automatically if elapsed duration >= 10s, or if forceNotify is true.
func NotifyTaskComplete(title, message string, elapsed time.Duration, forceNotify bool) bool {
	if elapsed < 10*time.Second && !forceNotify {
		return false
	}

	notified := false

	// Desktop notification via notify-send if GUI environment is present
	hasGUI := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
	if hasGUI && hasTool("notify-send") {
		cmd := execCommand("notify-send", "-a", "crush", "-i", "utilities-terminal", title, message)
		if err := cmd.Run(); err == nil {
			notified = true
		}
	}

	// Terminal bell (\a) to notify user in console
	fmt.Fprint(os.Stderr, "\a")

	return notified || forceNotify || elapsed >= 10*time.Second
}
