// Copyright 2026 - See NOTICE file for copyright holders.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package eval

import "time"

// Timer records per-phase latency for the settlement lifecycle (handover Group
// B). Latency is measured around the in-process call that submits and awaits a
// transaction, which is finer-grained than block timestamps.
type Timer struct {
	start time.Time
	last  time.Time
	durMs map[string]float64
	order []string
}

// NewTimer starts the lifecycle clock.
func NewTimer() *Timer {
	now := time.Now()
	return &Timer{start: now, last: now, durMs: map[string]float64{}}
}

// Time runs fn, records its wall-clock duration (ms) under label, and returns
// fn's error. Use it to bracket a single submit-and-await call.
func (t *Timer) Time(label string, fn func() error) error {
	s := time.Now()
	err := fn()
	t.record(label, time.Since(s))
	t.last = time.Now()
	return err
}

// Mark records the elapsed time (ms) since the previous Mark/Time under label
// (for phases that are not a single function call, e.g. a challenge wait).
func (t *Timer) Mark(label string) {
	now := time.Now()
	t.record(label, now.Sub(t.last))
	t.last = now
}

func (t *Timer) record(label string, d time.Duration) {
	if _, ok := t.durMs[label]; !ok {
		t.order = append(t.order, label)
	}
	t.durMs[label] += float64(d.Microseconds()) / 1000.0
}

// TotalMs returns the elapsed time since the timer started, in ms.
func (t *Timer) TotalMs() float64 {
	return float64(time.Since(t.start).Microseconds()) / 1000.0
}

// Durations returns a copy of the recorded per-label durations (ms).
func (t *Timer) Durations() map[string]float64 {
	out := make(map[string]float64, len(t.durMs))
	for k, v := range t.durMs {
		out[k] = v
	}
	return out
}
