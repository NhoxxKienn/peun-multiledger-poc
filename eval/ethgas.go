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

import "strings"

// ETHTx is one observed Ethereum transaction's cost.
type ETHTx struct {
	Label    string `json:"label"`    // scenario-supplied phase label
	GasUsed  uint64 `json:"gasUsed"`  // receipt.gasUsed
	CallData int    `json:"callData"` // len(input) in bytes
	Selector string `json:"selector"` // first 4 bytes of input (0x…)
	Block    uint64 `json:"block"`
	Hash     string `json:"hash"`
	To       string `json:"to"`
}

// ETHGasMeter scans the Hardhat node for transactions sent to the watched Perun
// contracts (adjudicator / asset holder) and records their gas. Because the
// devnet only carries this scenario's traffic, a block-range scan around each
// phase attributes the gas to that phase precisely — no ABI decoding required.
type ETHGasMeter struct {
	rpc     *rpcClient
	watched map[string]bool // lower-case 0x addresses we care about
	txs     []ETHTx
}

// NewETHGasMeter dials the node and watches the given contract addresses.
func NewETHGasMeter(nodeURL string, watched ...string) *ETHGasMeter {
	w := make(map[string]bool, len(watched))
	for _, a := range watched {
		if a != "" {
			w[strings.ToLower(a)] = true
		}
	}
	return &ETHGasMeter{rpc: newRPCClient(nodeURL), watched: w}
}

// Block returns the current block height (a marker to pass to Collect).
func (m *ETHGasMeter) Block() uint64 {
	var hex string
	if err := m.rpc.call("eth_blockNumber", []any{}, &hex); err != nil {
		return 0
	}
	return hexToUint(hex)
}

type ethBlock struct {
	Transactions []struct {
		Hash  string `json:"hash"`
		To    string `json:"to"`
		Input string `json:"input"`
	} `json:"transactions"`
}

type ethReceipt struct {
	GasUsed string `json:"gasUsed"`
	Status  string `json:"status"`
}

// Collect scans blocks (fromBlock+1 .. tip) for transactions to the watched
// contracts, records each under label, and returns the ones found in this call.
func (m *ETHGasMeter) Collect(label string, fromBlock uint64) []ETHTx {
	tip := m.Block()
	var found []ETHTx
	for n := fromBlock + 1; n <= tip; n++ {
		var blk ethBlock
		if err := m.rpc.call("eth_getBlockByNumber", []any{uintToHex(n), true}, &blk); err != nil {
			continue
		}
		for _, tx := range blk.Transactions {
			if tx.To == "" || !m.watched[strings.ToLower(tx.To)] {
				continue
			}
			var rc ethReceipt
			if err := m.rpc.call("eth_getTransactionReceipt", []any{tx.Hash}, &rc); err != nil {
				continue
			}
			e := ETHTx{
				Label:    label,
				GasUsed:  hexToUint(rc.GasUsed),
				CallData: hexByteLen(tx.Input),
				Selector: selectorOf(tx.Input),
				Block:    n,
				Hash:     tx.Hash,
				To:       strings.ToLower(tx.To),
			}
			found = append(found, e)
			m.txs = append(m.txs, e)
		}
	}
	return found
}

// Txs returns every transaction recorded across all Collect calls.
func (m *ETHGasMeter) Txs() []ETHTx { return m.txs }

func uintToHex(n uint64) string {
	const digits = "0123456789abcdef"
	if n == 0 {
		return "0x0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n&0xf]
		n >>= 4
	}
	return "0x" + string(buf[i:])
}

func selectorOf(input string) string {
	s := strings.TrimPrefix(input, "0x")
	if len(s) < 8 {
		return ""
	}
	return "0x" + s[:8]
}
