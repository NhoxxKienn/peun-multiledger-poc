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

package main

import (
	"log"
	"strconv"

	"perun-multiledger-poc/eval"
)

// meters is the per-run evaluation-metrics bundle. Every method is a no-op when
// EVAL_OUT is unset (eval.Enabled() == false), so a plain `go run .` is
// unaffected; with EVAL_OUT set, the run appends one JSON line of gas / cycles /
// latency / versions / balances for the Chapter 9 evaluation.
type meters struct {
	on  bool
	rec *eval.RunRecord
	em  *eval.ETHGasMeter
	cm  *eval.CKBCycleMeter
	t   *eval.Timer
}

// newMeters builds the bundle (and starts the CKB cycle poller) when enabled.
func newMeters(scenario, mode, thesisSc, ethNodeURL, ckbURL, ethAdj, ethAsset string) *meters {
	if !eval.Enabled() {
		return &meters{}
	}
	m := &meters{
		on:  true,
		rec: eval.NewRecord(scenario, mode, thesisSc),
		em:  eval.NewETHGasMeter(ethNodeURL, ethAdj, ethAsset),
		cm:  eval.NewCKBCycleMeter(ckbURL),
		t:   eval.NewTimer(),
	}
	m.cm.Start()
	return m
}

// ethOp brackets an Ethereum on-chain call: records its block range (gas) and
// wall-clock latency under label, then returns the call's error.
func (m *meters) ethOp(label string, fn func() error) error {
	if !m.on {
		return fn()
	}
	b := m.em.Block()
	err := m.t.Time(label, fn)
	m.em.Collect(label, b)
	return err
}

// ckbOp brackets a CKB on-chain call (cycles + latency).
func (m *meters) ckbOp(label string, fn func() error) error {
	if !m.on {
		return fn()
	}
	tip := m.cm.Tip()
	err := m.t.Time(label, fn)
	m.cm.Collect(label, tip)
	return err
}

// bothOp brackets a call that touches BOTH ledgers (e.g. Coordinate).
func (m *meters) bothOp(label string, fn func() error) error {
	if !m.on {
		return fn()
	}
	b := m.em.Block()
	tip := m.cm.Tip()
	err := m.t.Time(label, fn)
	m.em.Collect(label, b)
	m.cm.Collect(label, tip)
	return err
}

// version records a channel state version under name (handover Group C).
func (m *meters) version(name string, v uint64) {
	if !m.on {
		return
	}
	m.rec.Versions[name] = v
}

// config records a configuration constant (handover Group D).
func (m *meters) config(name string, v any) {
	if !m.on {
		return
	}
	m.rec.Config[name] = v
}

// balance records a party/chain balance snapshot under "<key>".
func (m *meters) balance(key, amount string) {
	if !m.on {
		return
	}
	m.rec.Balances[key] = amount
}

// finish assembles the record (gas, cycles, latency, CKB config) and appends it
// to EVAL_OUT. outcome is the banner verdict; ok marks a completed run.
func (m *meters) finish(outcome string, ok bool) {
	if !m.on {
		return
	}
	m.cm.Stop()
	m.rec.OK = ok
	m.rec.Outcome = outcome
	m.rec.ETH = m.em.Txs()
	m.rec.CKB = m.cm.Txs()
	m.rec.Phases = m.t.Durations()
	m.rec.TotalMs = m.t.TotalMs()
	m.rec.Config["ckbMaxBlockCycles"] = m.cm.MaxBlockCycles()
	m.rec.Config["ckbBlockTimeMs"] = m.cm.BlockTimeMs()
	if err := m.rec.Append(); err != nil {
		log.Printf("[eval] append record: %v", err)
	}
}

// u64 formats a uint64 as a string (for balance maps).
func u64(v uint64) string { return strconv.FormatUint(v, 10) }
