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

// Package eval is a stdlib-only evaluation-metrics layer for the PoC scenarios.
// It speaks raw JSON-RPC to the Ethereum (Hardhat) and Nervos CKB (offckb) nodes
// to record per-operation on-chain cost (ETH gas / CKB cycles), captures
// per-phase latency with a monotonic timer, and emits one machine-readable JSON
// line per run. It deliberately imports neither go-perun nor the chain backends,
// so it can be shared by every scenario module via a `replace` directive without
// pinning any dependency versions.
//
// All instrumentation is a no-op unless the EVAL_OUT environment variable names
// an output file, so a plain `go run .` of a scenario is unaffected.
package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// rpcClient is a minimal JSON-RPC 2.0 client over HTTP, used for both the
// Ethereum and CKB nodes.
type rpcClient struct {
	url  string
	http *http.Client
	id   int
}

func newRPCClient(url string) *rpcClient {
	// Hardhat and offckb both serve JSON-RPC over HTTP; normalise a ws:// URL.
	url = strings.Replace(url, "ws://", "http://", 1)
	url = strings.Replace(url, "wss://", "https://", 1)
	return &rpcClient{url: url, http: &http.Client{Timeout: 10 * time.Second}}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call invokes method with params and decodes the result into out (which may be
// nil to ignore the result).
func (c *rpcClient) call(method string, params []any, out any) error {
	c.id++
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: c.id, Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.http.Post(c.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("rpc %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var rr rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return fmt.Errorf("rpc %s: decode: %w", method, err)
	}
	if rr.Error != nil {
		return fmt.Errorf("rpc %s: %d %s", method, rr.Error.Code, rr.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(rr.Result, out); err != nil {
			return fmt.Errorf("rpc %s: unmarshal result: %w", method, err)
		}
	}
	return nil
}

// hexToUint parses a "0x"-prefixed hex string (or empty/"0x") into a uint64.
func hexToUint(s string) uint64 {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0
	}
	return v
}

// hexByteLen returns the number of bytes encoded by a "0x"-prefixed hex string.
func hexByteLen(s string) int {
	s = strings.TrimPrefix(s, "0x")
	return len(s) / 2
}
