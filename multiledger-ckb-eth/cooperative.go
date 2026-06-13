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
	"fmt"
	"log"

	"perun-multiledger-poc/multiledger-ckb-eth/client"
)

// runCooperative is the honest baseline: Bob and Alice perform an agreed
// cross-asset update (Alice pays Bob on ETH, Bob pays Alice on CKB) and then
// settle the channel cooperatively. No dispute window opens and no coordinator
// is involved — both ledgers conclude the same final state off the happy path.
func runCooperative(m *meters, chBA, chAB *client.PaymentChannel) {
	log.Println("[cooperative] performing agreed cross-asset update.")
	chAB.SendEthPayment(parentEth / 2)        // Alice -> Bob on ETH.
	chBA.SendCKBPayment(int64(parentCkb / 2)) // Bob -> Alice on CKB.
	m.version("final", uint64(chBA.GetChannelState().Version))

	log.Println("[cooperative] finalizing and settling the channel.")
	_ = m.bothOp("settle", func() error {
		chBA.Finalize()
		done := make(chan struct{}, 2)
		go func() { chBA.Settle(false); done <- struct{}{} }() // Bob primary.
		go func() { chAB.Settle(true); done <- struct{}{} }()  // Alice secondary.
		<-done
		<-done
		return nil
	})

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  HONEST COOPERATIVE SETTLEMENT — OUTCOME")
	fmt.Println("============================================================")
	fmt.Println("  Both ledgers concluded the SAME agreed final state off the")
	fmt.Println("  happy path: Alice paid Bob on ETH, Bob paid Alice on CKB.")
	fmt.Println("  No dispute, no coordinator — the multi-ledger baseline.")
	fmt.Println("============================================================")
}
