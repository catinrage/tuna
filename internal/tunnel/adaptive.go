package tunnel

import "time"

type AdaptiveDelay struct {
	min     time.Duration
	max     time.Duration
	current time.Duration
}

func NewAdaptiveDelay(minDelay, maxDelay time.Duration) AdaptiveDelay {
	if minDelay < 0 {
		minDelay = 0
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	return AdaptiveDelay{
		min:     minDelay,
		max:     maxDelay,
		current: minDelay,
	}
}

func (a *AdaptiveDelay) Current() time.Duration {
	return a.current
}

func (a *AdaptiveDelay) Increase() {
	if a.min == 0 || a.max == 0 {
		return
	}
	if a.current >= a.max {
		a.current = a.max
		return
	}
	next := a.current * 2
	if next < a.min {
		next = a.min
	}
	if next > a.max {
		next = a.max
	}
	a.current = next
}

func (a *AdaptiveDelay) Decrease() {
	if a.current <= a.min {
		a.current = a.min
		return
	}
	next := a.current / 2
	if next < a.min {
		next = a.min
	}
	a.current = next
}
