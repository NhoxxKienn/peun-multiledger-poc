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
	"sync"
	"time"
)

// CKBTx is one observed CKB transaction's cost (cycles), as reported by the node
// via get_block_by_number(with_cycles=true).
type CKBTx struct {
	Label  string `json:"label"`
	Cycles uint64 `json:"cycles"`
	Block  uint64 `json:"block"`
	Hash   string `json:"hash"`
}

// CKBCycleMeter records the cycle cost of every non-cellbase transaction the CKB
// node commits during a run. It runs a background poller (because CKB commits a
// transaction a few blocks after it is sent) and reads per-transaction cycles
// directly from the block (with_cycles=true), which is deterministic and does not
// depend on tx-pool cycle retention. A scenario brackets each on-chain phase with
// Tip()/Collect(label, fromTip) to attribute cycles to that phase.
type CKBCycleMeter struct {
	rpc *rpcClient

	mu          sync.Mutex
	buf         []CKBTx
	lastScanned uint64
	labeled     []CKBTx

	stop chan struct{}
	done chan struct{}
}

// NewCKBCycleMeter dials the CKB node (it does not start polling until Start).
func NewCKBCycleMeter(rpcURL string) *CKBCycleMeter {
	return &CKBCycleMeter{rpc: newRPCClient(rpcURL)}
}

type ckbBlockResp struct {
	Block struct {
		Header struct {
			Number    string `json:"number"`
			Timestamp string `json:"timestamp"`
		} `json:"header"`
		Transactions []struct {
			Hash string `json:"hash"`
		} `json:"transactions"`
	} `json:"block"`
	Cycles []string `json:"cycles"`
}

// tip returns the node's current tip block number.
func (m *CKBCycleMeter) tip() uint64 {
	var hex string
	if err := m.rpc.call("get_tip_block_number", []any{}, &hex); err != nil {
		return 0
	}
	return hexToUint(hex)
}

// scanBlock records the non-cellbase transactions of block n with their cycles.
func (m *CKBCycleMeter) scanBlock(n uint64) {
	var resp ckbBlockResp
	if err := m.rpc.call("get_block_by_number", []any{uintToHex(n), "0x2", true}, &resp); err != nil {
		return
	}
	// cycles[i] corresponds to transactions[i+1] (the cellbase has no cycles).
	for i, cyc := range resp.Cycles {
		txIdx := i + 1
		if txIdx >= len(resp.Block.Transactions) {
			break
		}
		m.mu.Lock()
		m.buf = append(m.buf, CKBTx{
			Cycles: hexToUint(cyc),
			Block:  n,
			Hash:   resp.Block.Transactions[txIdx].Hash,
		})
		m.mu.Unlock()
	}
}

// Start begins polling the node in the background. Safe to call once.
func (m *CKBCycleMeter) Start() {
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	m.lastScanned = m.tip()
	go func() {
		defer close(m.done)
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				m.drain()
				return
			case <-t.C:
				m.drain()
			}
		}
	}()
}

// drain scans any blocks committed since the last scan.
func (m *CKBCycleMeter) drain() {
	tip := m.tip()
	for n := m.lastScanned + 1; n <= tip; n++ {
		m.scanBlock(n)
		m.lastScanned = n
	}
}

// Stop ends background polling after a final drain.
func (m *CKBCycleMeter) Stop() {
	if m.stop == nil {
		return
	}
	close(m.stop)
	<-m.done
}

// Tip returns the node's current tip (a marker to pass to Collect).
func (m *CKBCycleMeter) Tip() uint64 { return m.tip() }

// Collect labels and returns every recorded transaction in (fromTip, current],
// waiting briefly for the background poller to catch up to the tip first.
func (m *CKBCycleMeter) Collect(label string, fromTip uint64) []CKBTx {
	target := m.tip()
	// Give the poller up to ~3 s to record blocks through the current tip.
	for i := 0; i < 12; i++ {
		m.mu.Lock()
		caught := m.lastScanned >= target
		m.mu.Unlock()
		if caught {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var found []CKBTx
	for _, tx := range m.buf {
		if tx.Block > fromTip && tx.Block <= target {
			tx.Label = label
			found = append(found, tx)
			m.labeled = append(m.labeled, tx)
		}
	}
	return found
}

// Txs returns every labeled transaction collected across all Collect calls.
func (m *CKBCycleMeter) Txs() []CKBTx { return m.labeled }

// MaxBlockCycles reads the consensus per-block cycle budget (handover D9).
func (m *CKBCycleMeter) MaxBlockCycles() uint64 {
	var resp struct {
		MaxBlockCycles string `json:"max_block_cycles"`
	}
	if err := m.rpc.call("get_consensus", []any{}, &resp); err != nil {
		return 0
	}
	return hexToUint(resp.MaxBlockCycles)
}

// BlockTimeMs estimates the median block interval in ms from recent headers
// (handover D8). Returns 0 if it cannot be determined.
func (m *CKBCycleMeter) BlockTimeMs() uint64 {
	tip := m.tip()
	if tip < 6 {
		return 0
	}
	ts := func(n uint64) uint64 {
		// get_header_by_number returns the HeaderView directly (timestamp in ms).
		var h struct {
			Timestamp string `json:"timestamp"`
		}
		if err := m.rpc.call("get_header_by_number", []any{uintToHex(n)}, &h); err != nil {
			return 0
		}
		return hexToUint(h.Timestamp)
	}
	t1 := ts(tip)
	t0 := ts(tip - 5)
	if t1 == 0 || t0 == 0 || t1 <= t0 {
		return 0
	}
	return (t1 - t0) / 5 // header timestamps are in ms.
}
