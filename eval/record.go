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

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// envOut is the environment variable that enables instrumentation: when it names
// a file, each run appends one JSON line to it; when unset, all recording is a
// no-op so a plain `go run .` is unaffected.
const envOut = "EVAL_OUT"

// Enabled reports whether evaluation recording is on (EVAL_OUT is set).
func Enabled() bool { return os.Getenv(envOut) != "" }

// RunRecord is the machine-readable result of a single scenario run. Field names
// are semantic (not handover IDs); the aggregator maps them to A/B/C/D IDs per
// scenario.
type RunRecord struct {
	Scenario  string `json:"scenario"`           // module name, e.g. "multiledger-ckb-eth"
	Mode      string `json:"mode"`               // cooperative | attack | coordinated
	ThesisSc  string `json:"thesisSc,omitempty"` // e.g. "Sc1"
	Run       int    `json:"run"`                // run index (set by the harness via EVAL_RUN)
	StartedAt string `json:"startedAt"`          // RFC3339
	OK        bool   `json:"ok"`                 // run completed without a fatal error
	Outcome   string `json:"outcome,omitempty"`  // banner verdict, e.g. "ATTACK SUCCEEDED"

	ETH     []ETHTx            `json:"eth,omitempty"`     // per-operation ETH gas
	CKB     []CKBTx            `json:"ckb,omitempty"`     // per-operation CKB cycles
	Phases  map[string]float64 `json:"phases,omitempty"`  // per-phase latency (ms)
	TotalMs float64            `json:"totalMs,omitempty"` // end-to-end latency (ms)

	Versions map[string]uint64 `json:"versions,omitempty"` // register_eth, register_ckb, coordinate, conclude_eth/ckb …
	Balances map[string]string `json:"balances,omitempty"` // "<party>_<chain>_<when>" -> big-int string
	Config   map[string]any    `json:"config,omitempty"`   // challenge durations, block times, maxBlockCycles …
}

// NewRecord seeds a record for the given scenario/mode, reading the run index
// from EVAL_RUN if present.
func NewRecord(scenario, mode, thesisSc string) *RunRecord {
	run := 0
	if v := os.Getenv("EVAL_RUN"); v != "" {
		_ = json.Unmarshal([]byte(v), &run)
	}
	return &RunRecord{
		Scenario:  scenario,
		Mode:      mode,
		ThesisSc:  thesisSc,
		Run:       run,
		StartedAt: time.Now().Format(time.RFC3339),
		Versions:  map[string]uint64{},
		Balances:  map[string]string{},
		Config:    map[string]any{},
		Phases:    map[string]float64{},
	}
}

var appendMu sync.Mutex

// Append writes the record as one JSON line to the EVAL_OUT file. It is a no-op
// when EVAL_OUT is unset. Errors are returned (the caller may log-and-ignore so
// instrumentation never breaks a run).
func (r *RunRecord) Append() error {
	out := os.Getenv(envOut)
	if out == "" {
		return nil
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	appendMu.Lock()
	defer appendMu.Unlock()
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}
