package worker

import (
	"testing"
	"time"
)

func TestBackoffDelayUsesAttemptAndBoundedJitter(t *testing.T) {
	backoff := Backoff{Base: 5 * time.Second, Max: time.Minute}

	tests := []struct {
		name    string
		attempt int
		random  float64
		want    time.Duration
	}{
		{name: "lower jitter", attempt: 1, random: 0, want: 4 * time.Second},
		{name: "neutral jitter", attempt: 2, random: 0.5, want: 10 * time.Second},
		{name: "upper jitter", attempt: 3, random: 1, want: 24 * time.Second},
		{name: "maximum cap", attempt: 10, random: 1, want: time.Minute},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := backoff.Delay(test.attempt, test.random); got != test.want {
				t.Fatalf("Delay() = %s, want %s", got, test.want)
			}
		})
	}
}
