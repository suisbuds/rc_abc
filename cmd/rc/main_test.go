package main

import (
	"testing"
	"time"
)

func TestWaitForWorker(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		if err := waitForWorker(done, time.Second); err != nil {
			t.Fatalf("waitForWorker() error = %v, want nil", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		done := make(chan struct{})
		startedAt := time.Now()
		if err := waitForWorker(done, 10*time.Millisecond); err == nil {
			t.Fatal("waitForWorker() error = nil, want timeout")
		}
		if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
			t.Fatalf("waitForWorker() elapsed = %s, want bounded wait", elapsed)
		}
	})
}
