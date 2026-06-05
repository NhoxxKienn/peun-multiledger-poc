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
	"perun.network/go-perun/channel/multi"
	pclient "perun.network/go-perun/client"
	"perun.network/go-perun/wallet"

	"perun-multiledger-poc/multiledger-virtual-ckb-eth-coordinated/client"
)

// runDefended is the recursive-coordination defence, run with the honest
// parties' watchtowers ON (the intended Perun way). It demonstrates the PoC's
// central thesis: a parent-channel watchtower is structurally insufficient to
// defend a virtual sub-channel; only the RECURSIVE coordinator can.
//
// The intermediary (Ingrid) only ever co-signs the virtual's FUNDING sub-state
// (vc0); Alice and Bob update the virtual privately (vc1) without her. So when
// Bob registers the stale vc0, Ingrid's watcher has no newer sub-state to offer
// and the contract rejects its refutation ("subChannels too short"). Only the
// recursive coordinator (CoordinateVC) can reach the parent with vc1: it co-signs
// the parent AND the virtual sub-state, pinning ONE canonical vc1 on BOTH ledgers.
//
// The contested parent is withdrawn low-level (not high-level Channel.Settle):
// the intermediary cannot drive the recursive Settle machine without a gatherable
// virtual sub-state, so withdrawal goes through the backend adjudicator with an
// explicit vc1 state-map. The flow is serial (trigger → wait → coordinate → wait
// → withdraw) so the demo never races the live watcher for the CKB channel cell.
func runDefended(
	multiCoord *multi.Coordinator,
	coordAcc map[wallet.BackendID]wallet.Account,
	alice, bob, ingrid *Participant,
	chAB, chBA, chAI, chIA, chBI, chIB *client.PaymentChannel,
) {
	_ = alice
	_ = chAI
	_ = chIA
	ctx := context.Background()

	// Capture vc0, advance V to the canonical vc1 — the same witnesses Bob abuses
	// in the attack. vc1 is what the coordinator will pin on both ledgers.
	ss0 := signedStateOf(chBA)
	log.Printf("[defended] captured virtual state vc0 (v%d).", ss0.State.Version)
	log.Println("[defended] advancing virtual channel to vc1 (cross-asset swap).")
	chAB.SendEthPayment(virtualEth / 2)
	chBA.SendCKBPayment(int64(virtualCkb / 2))
	ss1 := signedStateOf(chBA)
	log.Printf("[defended] captured virtual state vc1 (v%d) — the global-canonical version.", ss1.State.Version)

	parentReqBob := pclient.NewTestChannel(chBI.GetChannel()).AdjudicatorReq()

	start := time.Now()

	// ── Phase 1: ATTACK TRIGGER ──────────────────────────────────────────────
	// Bob registers the STALE vc0 on BOTH ledgers — the only hand-driven dispute
	// in the defended flow; everything after is honest watchers + the coordinator.
	log.Printf("[defended] [+%5.1fs] phase 1: Bob registers the STALE vc0 (v%d) on CKB.",
		time.Since(start).Seconds(), ss0.State.Version)
	if err := bob.CkbAdj.Register(ctx, parentReqBob, []channel.SignedState{ss0}); err != nil {
		log.Fatalf("[defended] Bob CKB register: %v", err)
	}
	waitChallenge(start, attackChallenge, "CKB")
	log.Printf("[defended] [+%5.1fs] phase 1: Bob registers the STALE vc0 (v%d) on ETH.",
		time.Since(start).Seconds(), ss0.State.Version)
	if err := bob.EthAdj.Register(ctx, parentReqBob, []channel.SignedState{ss0}); err != nil {
		log.Fatalf("[defended] Bob ETH register: %v", err)
	}
	waitChallenge(start, attackChallenge, "ETH")

	// Let the honest watchers finish their autonomous reaction before the
	// coordinator touches the Bob-Ingrid cell (Ingrid's watcher can only offer the
	// stale vc0, so it cannot fix the divergence — but we must not race it).
	log.Printf("[defended] [+%5.1fs] letting honest watchers settle their reactions before coordinating…",
		time.Since(start).Seconds())
	time.Sleep(8 * time.Second)

	// ── Phase 2: DEFENCE (recursive coordinate) ──────────────────────────────
	// multi.Coordinator.Coordinate fans ONE virtual sub-state (vc1) to every
	// ledger (ETH + CKB) via CoordinateVC. Charlie co-signs the parent AND vc1,
	// pinning one canonical virtual version on both ledgers over Bob's stale vc0.
	vc1 := channel.SignedState{Params: ss1.Params, State: ss1.State, Sigs: ss1.Sigs}
	parentCoordSig, err := channel.Sign(coordAcc[ethBackendID], parentReqBob.Tx.State, ethBackendID)
	if err != nil {
		log.Fatalf("[defended] coordinator signing parent: %v", err)
	}
	vcCoordSig, err := channel.Sign(coordAcc[ethBackendID], ss1.State, ethBackendID)
	if err != nil {
		log.Fatalf("[defended] coordinator signing vc1: %v", err)
	}
	log.Printf("[defended] [+%5.1fs] phase 2: coordinator runs RECURSIVE CoordinateVC (parent + vc1) on both ledgers.",
		time.Since(start).Seconds())
	if err := multiCoord.Coordinate(ctx, parentReqBob, []channel.SignedState{vc1}, []wallet.Sig{parentCoordSig, vcCoordSig}); err != nil {
		log.Fatalf("[defended] recursive CoordinateVC: %v", err)
	}
	waitChallenge(start, attackChallenge, "CKB")
	waitChallenge(start, attackChallenge, "ETH")

	// ── Phase 3: WITHDRAW (uniform vc1) ──────────────────────────────────────
	// Both ledgers were pinned to the coordinated vc1 in phase 2, so both
	// withdrawals fold the SAME version. Bob is the primary disputer; Ingrid
	// withdraws as secondary (a no-op on CKB, where the conclude pays both parties
	// from the single channel cell; on ETH she still pulls her own allocation).
	smap1 := channel.MakeStateMap()
	smap1.Add(ss1.State)
	parentReqIngrid := pclient.NewTestChannel(chIB.GetChannel()).AdjudicatorReq()
	parentReqIngrid.Secondary = true

	log.Printf("[defended] [+%5.1fs] phase 3: withdrawing uniformly — both ledgers fold the coordinated vc1 (v%d).",
		time.Since(start).Seconds(), ss1.State.Version)
	if err := bob.CkbAdj.Withdraw(ctx, parentReqBob, smap1); err != nil {
		log.Fatalf("[defended] Bob CKB withdraw: %v", err)
	}
	if err := ingrid.CkbAdj.Withdraw(ctx, parentReqIngrid, smap1); err != nil {
		log.Fatalf("[defended] Ingrid CKB withdraw: %v", err)
	}
	if err := bob.EthAdj.Withdraw(ctx, parentReqBob, smap1); err != nil {
		log.Fatalf("[defended] Bob ETH withdraw: %v", err)
	}
	if err := ingrid.EthAdj.Withdraw(ctx, parentReqIngrid, smap1); err != nil {
		log.Fatalf("[defended] Ingrid ETH withdraw: %v", err)
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  RECURSIVE COORDINATION DEFENCE (CoordinateVC) — OUTCOME")
	fmt.Println("============================================================")
	fmt.Printf("  Both ledgers folded the virtual channel at vc1 (v%d)\n", ss1.State.Version)
	fmt.Println("  Bob registered the STALE vc0 on both ledgers, and the honest")
	fmt.Println("  watchtowers (Alice, Ingrid) stayed ON — but the intermediary's")
	fmt.Println("  watchtower could NOT refute it: it never sees the virtual's later")
	fmt.Println("  states, so its refutation was rejected ('subChannels too short').")
	fmt.Println("  Only the RECURSIVE coordinator could fix it: it co-signed the")
	fmt.Println("  parent AND the virtual sub-state, pinning one global-canonical")
	fmt.Println("  vc1 on both ledgers, which both withdrawals then folded.")
	fmt.Println("  Result: ATTACK PREVENTED — uniform settlement on both ledgers.")
	fmt.Println("  Lesson: a parent watchtower cannot defend a virtual sub-channel;")
	fmt.Println("  recursive coordination is required.")
	fmt.Println("============================================================")
}
