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
	"perun.network/go-perun/wallet"

	"perun-multiledger-poc/multiledger-ckb-eth/client"
)

// coordinatorBundle bundles the in-process multi-ledger coordinator with its
// per-backend signing accounts, so the coordinated flow can both dispatch
// Coordinate and produce the coordinator's co-signature over the canonical state.
type coordinatorBundle struct {
	coord *multi.Coordinator
	acc   map[wallet.BackendID]wallet.Account
}

// runCoordinated is the parent-only coordinator defence (thesis Sc 2). Bob mounts
// the same divergence as the attack — register the stale v0 on CKB and the newer
// vN on ETH — but the channel params carry a trusted cross-chain coordinator
// (Charlie). After both dispute windows close, the coordinator runs a parent-only
// Coordinate that co-signs the canonical version (vN = max) and pins it on BOTH
// ledgers, so CKB is bumped from v0 to vN. Both ledgers then withdraw the SAME
// version: the divergence is erased. I1, I2, I3 hold.
func runCoordinated(m *meters, cb *coordinatorBundle, bob, alice *Participant, chBA, chAB *client.PaymentChannel) {
	ctx := context.Background()

	// Agreed cross-asset swap, ETH leg first: the intermediate vCkb (after Alice
	// pays Bob on ETH, before he pays her on CKB) is Bob's CKB-favoured state — the
	// stale version he will abuse. vN (after both legs) is the canonical version the
	// coordinator will pin. Both are non-zero versions (the CKB adjudicator only
	// disputes a post-funding state).
	log.Println("[coordinated] swap leg 1: Alice pays Bob on ETH.")
	chAB.SendEthPayment(parentEth / 2)
	reqBobCkb := adjReqSnapshot(chBA)
	log.Printf("[coordinated] captured vCkb (v%d) — Bob's stale CKB-favoured state.", reqBobCkb.Tx.State.Version)
	log.Println("[coordinated] swap leg 2: Bob pays Alice on CKB.")
	chBA.SendCKBPayment(int64(parentCkb / 2))
	reqBobVN := adjReqSnapshot(chBA)
	reqAliceVN := adjReqSnapshot(chAB)
	reqAliceVN.Secondary = true
	log.Printf("[coordinated] captured vN (v%d) — the canonical version.", reqBobVN.Tx.State.Version)

	emptyMap := channel.MakeStateMap()
	m.version("register_ckb", uint64(reqBobCkb.Tx.State.Version))
	m.version("register_eth", uint64(reqBobVN.Tx.State.Version))
	start := time.Now()

	// ── Phase 1: ATTACK ATTEMPT ───────────────────────────────────────────────
	// Bob registers the stale vCkb on CKB and the newer vN on ETH (the divergence).
	log.Printf("[coordinated] [+%5.1fs] phase 1: Bob registers the stale vCkb (v%d) on CKB.",
		time.Since(start).Seconds(), reqBobCkb.Tx.State.Version)
	if err := m.ckbOp("register_ckb", func() error { return bob.CkbAdj.Register(ctx, reqBobCkb, nil) }); err != nil {
		log.Fatalf("[coordinated] Bob CKB register: %v", err)
	}
	waitChallenge("coordinated", start, attackChallenge, "CKB")
	log.Printf("[coordinated] [+%5.1fs] phase 1: Bob registers the newer vN (v%d) on ETH.",
		time.Since(start).Seconds(), reqBobVN.Tx.State.Version)
	if err := m.ethOp("register_eth", func() error { return bob.EthAdj.Register(ctx, reqBobVN, nil) }); err != nil {
		log.Fatalf("[coordinated] Bob ETH register: %v", err)
	}
	waitChallenge("coordinated", start, attackChallenge, "ETH")

	// ── Phase 2: DEFENCE (parent-only coordinate) ─────────────────────────────
	// The coordinator co-signs the canonical vN and Coordinate fans it to every
	// ledger (ETH + CKB), pinning ONE version on both and bumping CKB off v0.
	coordSig, err := channel.Sign(cb.acc[ethBackendID], reqBobVN.Tx.State, ethBackendID)
	if err != nil {
		log.Fatalf("[coordinated] coordinator signing vN: %v", err)
	}
	log.Printf("[coordinated] [+%5.1fs] phase 2: coordinator pins the canonical vN (v%d) on both ledgers.",
		time.Since(start).Seconds(), reqBobVN.Tx.State.Version)
	m.version("coordinate", uint64(reqBobVN.Tx.State.Version))
	if err := m.bothOp("coordinate", func() error {
		return cb.coord.Coordinate(ctx, reqBobVN, nil, []wallet.Sig{coordSig})
	}); err != nil {
		log.Fatalf("[coordinated] parent-only Coordinate: %v", err)
	}
	waitChallenge("coordinated", start, attackChallenge, "CKB")
	waitChallenge("coordinated", start, attackChallenge, "ETH")

	// ── Phase 3: WITHDRAW (uniform vN) ────────────────────────────────────────
	// Both ledgers were pinned to vN, so both withdrawals fold the SAME version.
	log.Printf("[coordinated] [+%5.1fs] phase 3: withdrawing uniformly at the coordinated vN (v%d).",
		time.Since(start).Seconds(), reqBobVN.Tx.State.Version)
	if err := m.ckbOp("conclude_ckb", func() error { return bob.CkbAdj.Withdraw(ctx, reqBobVN, emptyMap) }); err != nil {
		log.Fatalf("[coordinated] Bob CKB withdraw: %v", err)
	}
	if err := alice.CkbAdj.Withdraw(ctx, reqAliceVN, emptyMap); err != nil {
		log.Fatalf("[coordinated] Alice CKB withdraw: %v", err)
	}
	if err := m.ethOp("conclude_eth", func() error { return bob.EthAdj.Withdraw(ctx, reqBobVN, emptyMap) }); err != nil {
		log.Fatalf("[coordinated] Bob ETH withdraw: %v", err)
	}
	if err := alice.EthAdj.Withdraw(ctx, reqAliceVN, emptyMap); err != nil {
		log.Fatalf("[coordinated] Alice ETH withdraw: %v", err)
	}
	m.version("conclude_ckb", uint64(reqBobVN.Tx.State.Version))
	m.version("conclude_eth", uint64(reqBobVN.Tx.State.Version))

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  COORDINATOR-DEFENDED SETTLEMENT (parent only) — OUTCOME")
	fmt.Println("============================================================")
	fmt.Printf("  Both ledgers settled the channel at the coordinated v%d\n", reqBobVN.Tx.State.Version)
	fmt.Println("  Bob registered the stale vCkb on CKB, but the coordinator co-signed")
	fmt.Println("  the canonical vN and pinned it on both ledgers, bumping CKB off vCkb.")
	fmt.Println("  Result: ATTACK PREVENTED — uniform settlement on both ledgers.")
	fmt.Println("  I1, I2, I3 hold: one canonical version, a coordinate() gate before")
	fmt.Println("  each conclude, and coordination only after both windows closed.")
	fmt.Println("============================================================")
}
