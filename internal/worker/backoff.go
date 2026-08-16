package worker

import "time"

type Backoff struct {
	Base time.Duration
	Max  time.Duration
}

func (b Backoff) Delay(attempt int, random float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := b.Base
	for index := 1; index < attempt && delay < b.Max; index++ {
		if delay > b.Max/2 {
			delay = b.Max
			break
		}
		delay *= 2
	}
	if delay >= b.Max {
		return b.Max
	}

	jittered := time.Duration(float64(delay) * (0.8 + 0.4*random))
	if jittered > b.Max {
		return b.Max
	}
	return jittered
}
