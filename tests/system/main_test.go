package main

import "testing"

func TestPaintHonorsColorEnvironment(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		t.Setenv("FORCE_COLOR", "")
		if got := paint(ansiGreen, "PASS"); got != "PASS" {
			t.Fatalf("paint() = %q, want plain text", got)
		}
	})

	t.Run("forced", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("FORCE_COLOR", "1")
		if got := paint(ansiGreen, "PASS"); got != ansiGreen+"PASS"+ansiReset {
			t.Fatalf("paint() = %q, want ANSI-colored text", got)
		}
	})
}
