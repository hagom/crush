package main

import (
	"testing"
)

func TestTestNoFiles(t *testing.T) {
	opts := TestOptions{}
	err := DoTest(nil, opts)
	if err == nil {
		t.Error("DoTest with nil files should error")
	}
}
