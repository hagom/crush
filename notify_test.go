package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestNotifyTaskCompleteCallsNotifySend(t *testing.T) {
	origExec := execCommand
	origLookPath := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLookPath
	}()

	lookPath = func(file string) (string, error) {
		if file == "notify-send" {
			return "/usr/bin/notify-send", nil
		}
		return origLookPath(file)
	}

	called := false
	var capturedArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "notify-send" {
			called = true
			capturedArgs = append([]string(nil), args...)
			return exec.Command("true")
		}
		return origExec(name, args...)
	}

	t.Setenv("DISPLAY", ":0")

	// 1. Duration < 10s and forceNotify false -> should NOT notify
	ok := NotifyTaskComplete("crush", "Tarea rápida", 2*time.Second, false)
	if ok || called {
		t.Errorf("NotifyTaskComplete should not trigger for duration < 10s without forceNotify")
	}

	// 2. Duration >= 10s -> should notify
	called = false
	ok = NotifyTaskComplete("crush", "Compresión finalizada", 15*time.Second, false)
	if !ok || !called {
		t.Errorf("NotifyTaskComplete did not invoke notify-send for >= 10s duration")
	}
	if len(capturedArgs) < 2 {
		t.Errorf("unexpected notify-send args: %v", capturedArgs)
	}

	// 3. forceNotify true even if duration < 10s -> should notify
	called = false
	ok = NotifyTaskComplete("crush", "Forzada", 1*time.Second, true)
	if !ok || !called {
		t.Errorf("NotifyTaskComplete did not invoke notify-send with forceNotify=true")
	}
}
