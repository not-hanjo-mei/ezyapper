package filewatch

import (
	"sync"
	"time"
)

// Debouncer coalesces bursts of Trigger calls into one call of fn, made once
// no Trigger has arrived for the debounce delay. Editors usually produce
// several events per save (truncate + write, or write temp + rename + chmod),
// and reloading on each of them would parse half-written files.
//
// fn never runs concurrently with itself: a trigger that arrives while fn is
// running schedules exactly one more run after it returns.
type Debouncer struct {
	fn func()

	mu      sync.Mutex
	delay   time.Duration
	timer   *time.Timer
	running bool
	pending bool
	stopped bool
}

// NewDebouncer returns a Debouncer that calls fn delay after the last Trigger.
func NewDebouncer(delay time.Duration, fn func()) *Debouncer {
	return &Debouncer{fn: fn, delay: delay}
}

// SetDelay changes the debounce delay for triggers that arrive afterwards.
func (d *Debouncer) SetDelay(delay time.Duration) {
	d.mu.Lock()
	d.delay = delay
	d.mu.Unlock()
}

// Trigger (re)starts the debounce timer.
func (d *Debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, d.fire)
}

func (d *Debouncer) fire() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	if d.running {
		d.pending = true
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	for {
		d.fn()
		d.mu.Lock()
		if !d.pending || d.stopped {
			d.running = false
			d.pending = false
			d.mu.Unlock()
			return
		}
		d.pending = false
		d.mu.Unlock()
	}
}

// Stop cancels any pending call. A call already running is not interrupted.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
}
