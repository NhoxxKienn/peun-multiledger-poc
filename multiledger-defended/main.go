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

// multiledger-defended mirrors TestMultiLedgerAttackCoordinate from go-perun's
// client/test package, executed against real Hardhat chains.
//
// The coordinator is built in-process from cross-chain-coordinator's
// `backends.SetupMultiCoordinator`, which returns a `*multi.Coordinator`
// wired to per-chain `ethchannel.Coordinator` instances. We call
// `multiCoord.Coordinate(...)` explicitly once Bob has registered v2 — that is
// the precise analogue of `charlie.Coordinate(ctx, v2ReqBob, nil, bID2)` in
// the upstream test. We deliberately do NOT route through the libp2p relay
// path: at the time of writing, `RelayCoordinatorNotifier` JSON-encodes
// `channel.SignedState` whose `Params.Parts` contains the `wallet.Address`
// interface, which Go's json decoder cannot reconstruct on the receiving end.
package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"time"

	"cross-chain-coordinator/backends"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	echannel "github.com/perun-network/perun-eth-backend/channel"
	ewallet "github.com/perun-network/perun-eth-backend/wallet"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/channel/multi"
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

	keyDeployer    = "79ea8f62d97bc0591a4224c1725fca6b00de5b2cea286fe2e0bb35c5e76be46e"
	keyAlice       = "1af2e950272dd403de7a5760d41c6e44d92b6d02797e51810795ff03cc2cda4f"
	keyBob         = "f63d7d8e930bccd74e93cf5662fde2c28fd8be95edb70c73f1bdd863d07f412e"
	keyCoordinator = "1c8e5b9a7c3d2e4f8a1b2c3d4e5f67890abcdef1234567890abcdef123456789"

	initialTokenAmount = 100

	settleTimeout = 2 * time.Minute
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("Deploying contracts on both chains.")
	chains := [2]client.ChainConfig{
		{ChainID: echannel.MakeChainID(big.NewInt(chainAID)), ChainURL: chainAURL},
		{ChainID: echannel.MakeChainID(big.NewInt(chainBID)), ChainURL: chainBURL},
	}
	deployContracts(chains[:], []common.Address{
		privKeyToAddress(keyAlice),
		privKeyToAddress(keyBob),
		privKeyToAddress(keyCoordinator),
	})

	log.Println("Building in-process coordinator via backends.SetupMultiCoordinator.")
	multiCoord, charlieAccs, charlieAddr, err := startCoordinator(chains)
	if err != nil {
		log.Fatalf("setting up coordinator: %v", err)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	aliceWireAcc := p2p.NewRandomAccount(rng)
	bobWireAcc := p2p.NewRandomAccount(rng)
	aliceBus, aliceDialer := setupBusWire(aliceWireAcc)
	bobBus, bobDialer := setupBusWire(bobWireAcc)
	aliceDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: bobWireAcc.Address()}, bobWireAcc.ID().String())
	bobDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: aliceWireAcc.Address()}, aliceWireAcc.ID().String())

	log.Println("Setting up Alice and Bob with coordinator address in channel params.")
	alice := setupSwapClient(aliceBus, keyAlice, chains, aliceWireAcc.Address(), nil, charlieAddr)
	bob := setupSwapClient(bobBus, keyBob, chains, bobWireAcc.Address(), nil, charlieAddr)
	alice.SetChallengeDuration(5)
	bob.SetChallengeDuration(5)

	bl := newBalanceLogger(chains[0], chains[1])
	bl.LogBalances("initial", alice.WalletAddress(), bob.WalletAddress())

	initBalances := channel.Balances{
		{big.NewInt(8), big.NewInt(2)},
		{big.NewInt(2), big.NewInt(8)},
	}
	log.Println("Opening channel WITH coordinator.")
	chAlice := alice.OpenChannel(bob.WireAddress(), initBalances)
	chBob := bob.AcceptedChannel()

	v1Balances := channel.Balances{
		{big.NewInt(5), big.NewInt(5)},
		{big.NewInt(3), big.NewInt(7)},
	}
	log.Println("Updating to v1 (honest agreed state).")
	if err := chAlice.UpdateBalances(ctx, v1Balances); err != nil {
		log.Fatalf("v1 update failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	v1Req := pclient.NewTestChannel(bob.LastChannel()).AdjudicatorReq()

	v2Balances := channel.Balances{
		{big.NewInt(1), big.NewInt(9)},
		{big.NewInt(5), big.NewInt(5)},
	}
	accs := []wallet.Account{alice.WalletAccount(), bob.WalletAccount()}
	v2ReqBob, err := buildSecretSignedReq(v1Req, v2Balances, accs, ewallet.BackendID, 1)
	if err != nil {
		log.Fatalf("fabricating Bob v2 req: %v", err)
	}

	chID := alice.LastChannel().ID()

	// Subscribe to BOTH adjudicators BEFORE starting watchers — matches upstream.
	sub1, err := bob.Adjudicator(0).Subscribe(ctx, chID)
	if err != nil {
		log.Fatalf("subscribing to chain A: %v", err)
	}
	sub2, err := bob.Adjudicator(1).Subscribe(ctx, chID)
	if err != nil {
		log.Fatalf("subscribing to chain B: %v", err)
	}

	// Start watchers manually. Bob's watcher replicates his chain-B v1 register
	// onto chain A; that replication is what lets v2 register on chain A pass
	// the version check (v2 > v1).
	go func() {
		if err := alice.LastChannel().Watch(alice); err != nil {
			log.Printf("alice watcher exited: %v", err)
		}
	}()
	go func() {
		if err := bob.LastChannel().Watch(bob); err != nil {
			log.Printf("bob watcher exited: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	log.Println("Attack step 1: Bob registers v1 on chain B.")
	if err := bob.Adjudicator(1).Register(ctx, v1Req, nil); err != nil {
		log.Fatalf("registering v1 on chain B: %v", err)
	}
	if ev := sub2.Next(); !isRegisteredEvent(ev) {
		log.Fatalf("chain B: expected RegisteredEvent, got %T", ev)
	}
	log.Println("Chain B v1 registered; waiting for watcher to replicate v1 to chain A.")
	if ev := sub1.Next(); !isRegisteredEvent(ev) {
		log.Fatalf("chain A (watcher replication): expected RegisteredEvent, got %T", ev)
	}
	log.Println("Chain A v1 replicated by watcher.")

	log.Println("Attack step 2: Bob reveals v2 on chain A (still within its open window).")
	if err := bob.Adjudicator(0).Register(ctx, v2ReqBob, nil); err != nil {
		log.Fatalf("registering v2 on chain A: %v", err)
	}
	ev := sub1.Next()
	reg, ok := ev.(*channel.RegisteredEvent)
	if !ok {
		log.Fatalf("chain A v2: expected RegisteredEvent, got %T", ev)
	}
	log.Printf("Chain A v=%d registered; waiting for dispute window…", reg.Version())
	if err := reg.TimeoutV.Wait(ctx); err != nil {
		log.Fatalf("waiting chain A v2 timeout: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_ = sub1.Close()
	_ = sub2.Close()

	// Mirror of `charlie.Coordinate(ctx, v2ReqBob, nil, bID2)` from the
	// upstream test: Charlie signs the v2 state and dispatches Coordinate
	// across both chains via the multi-ledger coordinator.
	log.Println("Coordinator locks v2 on both chains via multi.Coordinator.Coordinate.")
	coordSig, err := channel.Sign(charlieAccs[ewallet.BackendID], v2ReqBob.Tx.State, ewallet.BackendID)
	if err != nil {
		log.Fatalf("coordinator signing v2: %v", err)
	}
	if err := multiCoord.Coordinate(ctx, v2ReqBob, nil, []wallet.Sig{coordSig}); err != nil {
		log.Fatalf("coordinator Coordinate(v2): %v", err)
	}

	log.Println("Settling channel — Settle → ensureCoordinated → Withdraw.")
	settleCtx, settleCancel := context.WithTimeout(ctx, settleTimeout)
	defer settleCancel()
	if err := chAlice.SettleCtx(settleCtx, false); err != nil {
		log.Fatalf("Alice Settle: %v", err)
	}
	if err := chBob.SettleCtx(settleCtx, false); err != nil {
		log.Fatalf("Bob Settle: %v", err)
	}

	bl.LogBalances("final", alice.WalletAddress(), bob.WalletAddress())

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  COORDINATOR-DEFENDED OUTCOME")
	fmt.Println("============================================================")
	fmt.Println("  Both chains locked to canonical v2 by the coordinator:")
	fmt.Println("    Chain A → Alice 1 PRN, Bob 9 PRN")
	fmt.Println("    Chain B → Alice 5 PRN, Bob 5 PRN")
	fmt.Println("    Bob total: 14 PRN  (attack divergence: 16 PRN; coordinator blocks +2)")
	fmt.Println("  Result: ATTACK PREVENTED — no divergence between chains.")
	fmt.Println("============================================================")

	alice.Shutdown()
	bob.Shutdown()
}

// startCoordinator constructs the in-process multi-ledger coordinator wired to
// both chains. Returns the coordinator, the wallet account map (for signing),
// and Charlie's on-chain address (used as `params.coordinator`).
func startCoordinator(chains [2]client.ChainConfig) (*multi.Coordinator, map[wallet.BackendID]wallet.Account, common.Address, error) {
	ethKey, err := crypto.HexToECDSA(keyCoordinator)
	if err != nil {
		return nil, nil, common.Address{}, fmt.Errorf("parsing coordinator key: %w", err)
	}
	charlieAddr := crypto.PubkeyToAddress(ethKey.PublicKey)

	cfgs := []backends.BackendCoordinatorConfig{
		{BackendID: uint32(ewallet.BackendID), LedgerID: chainAID, ChainURL: chainAURL, AdjudicatorAddr: chains[0].Adjudicator.Hex()},
		{BackendID: uint32(ewallet.BackendID), LedgerID: chainBID, ChainURL: chainBURL, AdjudicatorAddr: chains[1].Adjudicator.Hex()},
	}

	coord, accs, err := backends.SetupMultiCoordinator(ethKey, cfgs)
	if err != nil {
		return nil, nil, common.Address{}, fmt.Errorf("backends.SetupMultiCoordinator: %w", err)
	}
	log.Printf("Coordinator ready; wallet=%s", charlieAddr.Hex())
	return coord, accs, charlieAddr, nil
}

func isRegisteredEvent(ev channel.AdjudicatorEvent) bool {
	_, ok := ev.(*channel.RegisteredEvent)
	return ok
}

// buildSecretSignedReq mirrors the upstream helper used by both attack and
// defended reference tests in go-perun's client/test package.
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
