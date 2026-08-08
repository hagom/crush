package main

import (
	"testing"
	"time"
)

func TestFileProgressAccessors(t *testing.T) {
	fp := &FileProgress{Name: "x"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			_ = fp.Status()
			_ = fp.OutPath()
			_ = fp.StartTime()
		}
	}()
	for i := 0; i < 1000; i++ {
		fp.SetStatus("active")
		fp.SetStatus("done")
		fp.SetOutPath("/tmp/f.gz")
		fp.SetStart(time.Now())
	}
	<-done
	if fp.Status() != "done" {
		t.Errorf("status = %q", fp.Status())
	}
}