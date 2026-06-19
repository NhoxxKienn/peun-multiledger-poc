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
	m *meters,
	multiCoord *multi.Coordinator,
	coordAcc map[wallet.BackendID]wallet.Account,
	alice, bob, ingrid *Participant,
	chAB, chBA, chAI, chIA, chBI, chIB *client.PaymentChannel,
) {
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
	// The Alice–Ingrid parent (the intermediary's OTHER leg) is settled alongside
	// Bob–Ingrid so the intermediary nets to ~0 (financial neutrality). The CKB
	// virtual-channel machinery forces the ordering: DisputeVC needs BOTH parent
	// cells live (perun-ckb-backend client/ckbclient.go), and a coordinated parent
	// can only be force-closed via the coordinated path (contract error 115,
	// CoordinatedSettlementRequired) — never a plain time-lock. So both parents must
	// be disputed and coordinated BEFORE either is force-closed. vc1 is the single
	// canonical sub-state both legs fold.
	parentReqAlice := pclient.NewTestChannel(chAI.GetChannel()).AdjudicatorReq()
	vc1 := channel.SignedState{Params: ss1.Params, State: ss1.State, Sigs: ss1.Sigs}

	m.version("register_ckb", uint64(ss0.State.Version))
	m.version("register_eth", uint64(ss1.State.Version))
	start := time.Now()

	// ── Phase 1: ATTACK TRIGGER (divergent registration) ─────────────────────
	// The DIVERGENT stale-state attack (identical to the un-coordinated
	// multiledger-virtual-ckb-eth/attack.go): Bob disputes the Bob–Ingrid parent at
	// the SAME parent state on both ledgers but embeds a DIFFERENT virtual sub-state
	// on each — the stale vc0 on CKB and the newer (privately held) vc1 on ETH — so
	// without a coordinator the virtual would settle divergently and pay Bob his
	// highest share on each chain. Bob is the only hand-driven disputer; everything
	// after is honest watchers + the coordinator, which pins ONE canonical vc1 on
	// both ledgers (phase 2) and so heals the divergence.
	log.Printf("[defended] [+%5.1fs] phase 1: Bob registers the STALE vc0 (v%d) on CKB.",
		time.Since(start).Seconds(), ss0.State.Version)
	if err := m.ckbOp("register_ckb", func() error {
		return bob.CkbAdj.Register(ctx, parentReqBob, []channel.SignedState{ss0})
	}); err != nil {
		log.Fatalf("[defended] Bob CKB register: %v", err)
	}
	waitChallenge(start, attackChallenge, "CKB")
	log.Printf("[defended] [+%5.1fs] phase 1: Bob registers the newer vc1 (v%d) on ETH — divergent.",
		time.Since(start).Seconds(), ss1.State.Version)
	if err := m.ethOp("register_eth", func() error {
		return bob.EthAdj.Register(ctx, parentReqBob, []channel.SignedState{ss1})
	}); err != nil {
		log.Fatalf("[defended] Bob ETH register: %v", err)
	}
	waitChallenge(start, attackChallenge, "ETH")

	// ── Phase 1b: the intermediary's watcher replicates vc0 onto Alice–Ingrid ──
	// Ingrid co-signed ONLY the virtual's FUNDING sub-state vc0 (never the hidden
	// vc1 that Alice and Bob updated privately). When Bob's divergent attack hits the
	// chains, Ingrid's honest watchtower — which stays ON (only Bob's is disabled) —
	// autonomously disputes her OTHER parent (Alice–Ingrid) with the only sub-state
	// she holds, vc0, keeping both of her legs consistent. This is observable in the
	// Alice–Ingrid channel reaches `disputed=true` here, during phase 1, before any
	// hand-driven settlement. Crucially the watcher can only offer vc0 — it cannot
	// refute to vc1 ("subChannels too short"); only the coordinator can progress
	// vc0 → vc1 on BOTH parents (phase 2/2b). So no hand-driven dispute of
	// Alice–Ingrid is issued here: an explicit call is a redundant no-op on CKB
	// ("Dispute not needed", the parent is already disputed) and on ETH collides
	// with the watcher on Ingrid's account (nonce reuse). The watcher must also act
	// while BOTH parent cells are live — phase 3 consumes the Bob–Ingrid cell,
	// after which a DisputeVC of Alice–Ingrid is impossible (CKB DisputeVC reads
	// both parents) — which is why the 8 s settle below precedes any force-close.
	log.Printf("[defended] [+%5.1fs] letting honest watchers settle their reactions before coordinating…",
		time.Since(start).Seconds())
	time.Sleep(8 * time.Second)

	// ── Phase 2: DEFENCE (recursive coordinate) ──────────────────────────────
	// multi.Coordinator.Coordinate fans ONE virtual sub-state (vc1) to every
	// ledger (ETH + CKB) via CoordinateVC. Charlie co-signs the parent AND vc1,
	// pinning one canonical virtual version on both ledgers over Bob's stale vc0.
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
	m.version("coordinate", uint64(ss1.State.Version))
	if err := m.bothOp("coordinate", func() error {
		return multiCoord.Coordinate(ctx, parentReqBob, []channel.SignedState{vc1}, []wallet.Sig{parentCoordSig, vcCoordSig})
	}); err != nil {
		log.Fatalf("[defended] recursive CoordinateVC: %v", err)
	}
	waitChallenge(start, attackChallenge, "CKB")
	waitChallenge(start, attackChallenge, "ETH")

	// ── Phase 2b: coordinate the Alice–Ingrid leg (neutrality setup) ──────────
	// The intermediary's other parent is coordinated too, so it also REQUIRES the
	// coordinator to settle (contract error 115). Pin the same canonical vc1 on it,
	// gating its later force-close. Non-fatal — see phase 1b.
	aliceParentCoordSig, err := channel.Sign(coordAcc[ethBackendID], parentReqAlice.Tx.State, ethBackendID)
	if err != nil {
		log.Printf("[defended] coordinator signing A-I parent: %v", err)
	} else {
		log.Printf("[defended] [+%5.1fs] phase 2b: coordinator pins vc1 on the Alice–Ingrid leg (both ledgers).",
			time.Since(start).Seconds())
		if err := multiCoord.Coordinate(ctx, parentReqAlice, []channel.SignedState{vc1}, []wallet.Sig{aliceParentCoordSig, vcCoordSig}); err != nil {
			log.Printf("[defended] CoordinateVC (A-I): %v", err)
		}
		waitChallenge(start, attackChallenge, "CKB")
		waitChallenge(start, attackChallenge, "ETH")
	}

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
	// Versions actually folded: vc0=v%d (stale, registered by Bob), vc1=v%d
	// (coordinated, withdrawn) — logged so a run evidences the canonical version.
	log.Printf("[defended] folded versions: vc0=v%d (registered) → vc1=v%d (concluded/withdrawn).",
		ss0.State.Version, ss1.State.Version)

	// Both withdrawals on a ledger fold the SAME coordinated vc1, so each ledger is
	// bracketed once (mirroring the register_*/coordinate phases): one latency
	// phase, one summed cost row, and one folded version per ledger.
	m.version("conclude_ckb", uint64(ss1.State.Version))
	if err := m.ckbOp("conclude_ckb", func() error {
		if err := bob.CkbAdj.Withdraw(ctx, parentReqBob, smap1); err != nil {
			return fmt.Errorf("Bob CKB withdraw: %w", err)
		}
		if err := ingrid.CkbAdj.Withdraw(ctx, parentReqIngrid, smap1); err != nil {
			return fmt.Errorf("Ingrid CKB withdraw: %w", err)
		}
		return nil
	}); err != nil {
		log.Fatalf("[defended] conclude CKB: %v", err)
	}

	m.version("conclude_eth", uint64(ss1.State.Version))
	if err := m.ethOp("conclude_eth", func() error {
		if err := bob.EthAdj.Withdraw(ctx, parentReqBob, smap1); err != nil {
			return fmt.Errorf("Bob ETH withdraw: %w", err)
		}
		if err := ingrid.EthAdj.Withdraw(ctx, parentReqIngrid, smap1); err != nil {
			return fmt.Errorf("Ingrid ETH withdraw: %w", err)
		}
		return nil
	}); err != nil {
		log.Fatalf("[defended] conclude ETH: %v", err)
	}

	// ── Phase 4: force-close the Alice–Ingrid leg (financial neutrality) ─────
	// The leg was disputed (phase 1b) and coordinated (phase 2b) while both parent
	// cells were live, so it is now a SECOND force-close: phase 3's first
	// force-close re-emitted the VC cell with FirstForceClose=true (perun-ckb-backend
	// handler.go), and this consumes it, folding the same vc1 into Alice's and
	// Ingrid's payouts. A plain time-lock close is rejected for a coordinated parent
	// (contract error 115, CoordinatedSettlementRequired), which is why the leg had
	// to go through the coordinator above. Serial, so we never race Alice's/Ingrid's
	// live watchers. NOT metered — conclude_* measures the contested Bob–Ingrid
	// settlement; this only releases Ingrid's funds for the neutrality check.
	// Non-fatal: a failure here must not discard the run's conclude_* / balance data.
	parentReqIngridAI := pclient.NewTestChannel(chIA.GetChannel()).AdjudicatorReq()
	parentReqIngridAI.Secondary = true
	log.Printf("[defended] [+%5.1fs] phase 4: force-close the Alice–Ingrid leg (Alice primary, Ingrid secondary).",
		time.Since(start).Seconds())
	if err := alice.CkbAdj.Withdraw(ctx, parentReqAlice, smap1); err != nil {
		log.Printf("[defended] Alice CKB withdraw (A-I): %v", err)
	}
	if err := ingrid.CkbAdj.Withdraw(ctx, parentReqIngridAI, smap1); err != nil {
		log.Printf("[defended] Ingrid CKB withdraw (A-I): %v", err)
	}
	if err := alice.EthAdj.Withdraw(ctx, parentReqAlice, smap1); err != nil {
		log.Printf("[defended] Alice ETH withdraw (A-I): %v", err)
	}
	if err := ingrid.EthAdj.Withdraw(ctx, parentReqIngridAI, smap1); err != nil {
		log.Printf("[defended] Ingrid ETH withdraw (A-I): %v", err)
	}
	log.Printf("[defended] [+%5.1fs] phase 4: Alice–Ingrid leg settled — intermediary now neutral.",
		time.Since(start).Seconds())

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
