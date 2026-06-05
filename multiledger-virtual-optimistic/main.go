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

// multiledger-virtual-optimistic demonstrates a multi-ledger virtual channel
// between Alice and Bob routed via Hub, with NO coordinator. Selects between
// three flows via the -mode flag:
//
//	-mode=cooperative — mirrors TestMultiLedgerVirtualHappy: virtual settles
//	                    off-chain (IsFinal=true), parents follow cooperatively.
//	-mode=onchain     — mirrors TestMultiLedgerVirtualDispute: all four parent
//	                    channel views are registered on-chain; the adjudicator
//	                    recursively unwinds the virtual sub-channel at settle.
//	-mode=attack      — stale-state-on-virtual attack: Bob disputes the Bob-Hub
//	                    parent with a DIFFERENT virtual sub-state on each chain
//	                    (vc1 on A, vc0 on B), so the virtual settles divergently
//	                    and Bob over-withdraws. The CKB-free counterpart of
//	                    multiledger-virtual-ckb-eth's attack; runs without any
//	                    coordinator to motivate the recursive CoordinateVC defence
//	                    in multiledger-virtual-coordinated.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"sync"
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
	keyHub      = "9c7d3e8f1a2b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60718293"

	initialTokenAmount = 100
	challengeDuration  = 5
	settleTimeout      = 2 * time.Minute
)

func main() {
	mode := flag.String("mode", "cooperative", "settlement mode: cooperative | onchain | attack")
	flag.Parse()

	switch *mode {
	case "cooperative", "onchain", "attack":
	default:
		log.Fatalf("invalid -mode=%q; expected cooperative, onchain or attack", *mode)
	}
	// The attack relies on the two chains diverging: a live watcher would
	// re-register the multi-ledger tree and re-sync both chains to one virtual
	// version, so it is disabled for -mode=attack only.
	autoWatch := *mode != "attack"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("Optimistic multi-ledger virtual channel demo, mode=%s", *mode)

	log.Println("Deploying contracts on both chains.")
	chains := [2]client.ChainConfig{
		{ChainID: echannel.MakeChainID(big.NewInt(chainAID)), ChainURL: chainAURL},
		{ChainID: echannel.MakeChainID(big.NewInt(chainBID)), ChainURL: chainBURL},
	}
	deployContracts(chains[:], []common.Address{
		privKeyToAddress(keyAlice),
		privKeyToAddress(keyBob),
		privKeyToAddress(keyHub),
	})

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	aliceWire := p2p.NewRandomAccount(rng)
	bobWire := p2p.NewRandomAccount(rng)
	hubWire := p2p.NewRandomAccount(rng)
	aliceBus, aliceDialer := setupBusWire(aliceWire)
	bobBus, bobDialer := setupBusWire(bobWire)
	hubBus, hubDialer := setupBusWire(hubWire)

	// Cross-register dialers so every peer can reach every other peer.
	aliceDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: bobWire.Address()}, bobWire.ID().String())
	aliceDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: hubWire.Address()}, hubWire.ID().String())
	bobDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: aliceWire.Address()}, aliceWire.ID().String())
	bobDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: hubWire.Address()}, hubWire.ID().String())
	hubDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: aliceWire.Address()}, aliceWire.ID().String())
	hubDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: bobWire.Address()}, bobWire.ID().String())

	log.Println("Setting up Alice, Bob, Hub.")
	alice := setupSwapClient(aliceBus, keyAlice, chains, aliceWire.Address(), autoWatch)
	bob := setupSwapClient(bobBus, keyBob, chains, bobWire.Address(), autoWatch)
	hub := setupSwapClient(hubBus, keyHub, chains, hubWire.Address(), autoWatch)
	alice.SetChallengeDuration(challengeDuration)
	bob.SetChallengeDuration(challengeDuration)
	hub.SetChallengeDuration(challengeDuration)

	bl := newBalanceLogger(chains[0], chains[1])
	bl.LogBalances("initial", alice.WalletAddress(), bob.WalletAddress(), hub.WalletAddress())

	// Parent Alice-Hub: Alice [10,10], Hub [10,10] across (chainA, chainB).
	parentBals := channel.Balances{
		{big.NewInt(10), big.NewInt(10)},
		{big.NewInt(10), big.NewInt(10)},
	}
	log.Println("Opening parent ledger channel Alice <-> Hub.")
	chAliceHub := alice.OpenChannel(hub.WireAddress(), parentBals)
	chHubAlice := hub.AcceptedChannel()
	log.Printf("Alice-Hub parent opened, id=%x", chAliceHub.ID())

	log.Println("Opening parent ledger channel Bob <-> Hub.")
	chBobHub := bob.OpenChannel(hub.WireAddress(), parentBals)
	chHubBob := hub.AcceptedChannel()
	log.Printf("Bob-Hub parent opened, id=%x", chBobHub.ID())

	// Open the virtual Alice-Bob channel routed through both parents.
	//
	// indexMaps:
	//   - Alice's view: {0,1} — virtual[0]=Alice maps to parent[0]=Alice, virtual[1]=Bob maps to parent[1]=Hub.
	//   - Bob's view:   {1,0} — virtual[0]=Alice maps to parent[1]=Hub,   virtual[1]=Bob maps to parent[0]=Bob.
	virtualBals := channel.Balances{
		{big.NewInt(5), big.NewInt(5)},
		{big.NewInt(5), big.NewInt(5)},
	}
	parents := []channel.ID{chAliceHub.ID(), chBobHub.ID()}
	indexMaps := [][]channel.Index{{0, 1}, {1, 0}}

	log.Println("Opening virtual channel Alice <-> Bob (sub-channel of both parents).")
	chAliceBob := alice.OpenVirtualChannel(bob.WireAddress(), virtualBals, parents, indexMaps)
	chBobAlice := bob.AcceptedChannel()
	log.Printf("Virtual Alice-Bob opened, id=%x", chAliceBob.ID())

	if *mode == "attack" {
		// The attack captures vc0, advances to vc1 itself, then disputes the
		// Bob-Hub parent divergently across the two chains.
		runAttack(ctx, bob, hub, chBobHub, chHubBob, chAliceBob, chBobAlice)
	} else {
		// Off-chain virtual update: Alice transfers 3 PRN on chain A to Bob and
		// receives 3 PRN on chain B from Bob.  Alice=[2,8], Bob=[8,2].
		virtualV1 := channel.Balances{
			{big.NewInt(2), big.NewInt(8)},
			{big.NewInt(8), big.NewInt(2)},
		}
		log.Println("Off-chain virtual update to v1 (Alice [2,8], Bob [8,2]).")
		if err := chAliceBob.UpdateBalances(ctx, virtualV1); err != nil {
			log.Fatalf("virtual v1 update: %v", err)
		}
		// Let Hub finish persisting the virtual sub-channel into its perun-client
		// registry on BOTH parents (persistVirtualChannel runs async w.r.t. the
		// proposer's OpenVirtualChannel return). This persist is what fires the
		// OnNewChannel hook that starts Hub's watcher on the virtual proxy; the
		// on-chain dispute below relies on that watcher being live before any
		// parent is registered, so the multi-ledger re-register can supply the
		// sub-channel state instead of a nil-Params one.
		time.Sleep(500 * time.Millisecond)

		switch *mode {
		case "cooperative":
			runCooperative(ctx, chAliceBob, chBobAlice, chHubAlice, chHubBob, chAliceHub, chBobHub)
		case "onchain":
			runOnChain(ctx, alice, bob, hub, chAliceHub, chBobHub, chHubAlice, chHubBob, chAliceBob)
		}
	}

	bl.LogBalances("final", alice.WalletAddress(), bob.WalletAddress(), hub.WalletAddress())

	// The attack prints its own divergent-outcome banner in runAttack; the
	// generic optimistic summary below describes only the honest v1 outcome.
	if *mode != "attack" {
		fmt.Println()
		fmt.Println("============================================================")
		fmt.Printf("  OPTIMISTIC VIRTUAL CHANNEL — MODE: %s\n", *mode)
		fmt.Println("============================================================")
		fmt.Println("  Virtual v1: Alice [2,8] / Bob [8,2]; locked in two parents.")
		fmt.Println("  Expected per-address net change (PRN):")
		fmt.Println("    Alice: chain A -3, chain B +3   (Bob's mirror)")
		fmt.Println("    Bob:   chain A +3, chain B -3")
		fmt.Println("    Hub:   chain A  0, chain B  0   (parents net to zero)")
		fmt.Println("============================================================")
	}

	alice.Shutdown()
	bob.Shutdown()
	hub.Shutdown()
}

// runCooperative finalises the virtual channel off-chain (IsFinal=true), both
// virtual peers settle cooperatively, Hub then marks both parents IsFinal=true,
// and finally all four parent views settle in a randomised but sequential order
// (per pair, first call uses secondary=false, second call secondary=true).
// Mirrors TestMultiLedgerVirtualHappy.
func runCooperative(
	ctx context.Context,
	chAliceBob, chBobAlice, chHubAlice, chHubBob, chAliceHub, chBobHub *client.SwapChannel,
) {
	log.Println("[cooperative] Marking virtual channel final off-chain.")
	if err := chAliceBob.Channel().Update(ctx, func(s *channel.State) {
		s.IsFinal = true
	}); err != nil {
		log.Fatalf("marking virtual final: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	settleCtx, settleCancel := context.WithTimeout(ctx, settleTimeout)
	defer settleCancel()

	log.Println("[cooperative] Alice and Bob settle the virtual channel in parallel (both primary).")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := chAliceBob.SettleCtx(settleCtx, false); err != nil {
			log.Fatalf("Alice virtual settle: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := chBobAlice.SettleCtx(settleCtx, false); err != nil {
			log.Fatalf("Bob virtual settle: %v", err)
		}
	}()
	wg.Wait()
	log.Println("[cooperative] Virtual channel settled.")

	// After virtual settles, the parent states carry the updated virtual balances
	// but are not yet final.  Hub explicitly marks both parents IsFinal=true so
	// the parent settles take the concludeFinal off-chain path.
	log.Println("[cooperative] Hub marks both parent channels IsFinal=true.")
	if err := chHubAlice.Channel().Update(ctx, func(s *channel.State) { s.IsFinal = true }); err != nil {
		log.Fatalf("Hub finalising Alice-Hub: %v", err)
	}
	if err := chHubBob.Channel().Update(ctx, func(s *channel.State) { s.IsFinal = true }); err != nil {
		log.Fatalf("Hub finalising Bob-Hub: %v", err)
	}

	// Settle all four parent views sequentially in a random order. Per pair
	// (Alice-Hub & Hub-Alice = pair 0, Bob-Hub & Hub-Bob = pair 1) the first
	// view settled uses primary, the second uses secondary.
	parents := []*client.SwapChannel{chAliceHub, chHubAlice, chBobHub, chHubBob}
	labels := []string{"Alice-Hub", "Hub-Alice", "Bob-Hub", "Hub-Bob"}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	perm := rng.Perm(len(parents))
	log.Printf("[cooperative] Parent settle order: %v", perm)
	isSecondary := [2]bool{false, false}
	for _, i := range perm {
		pair := 0
		if i >= 2 {
			pair = 1
		}
		sec := isSecondary[pair]
		isSecondary[pair] = true
		log.Printf("[cooperative] Settling %s (secondary=%v).", labels[i], sec)
		if err := parents[i].SettleCtx(settleCtx, sec); err != nil {
			log.Fatalf("settling %s: %v", labels[i], err)
		}
	}
	log.Println("[cooperative] Parents settled.")
}

// runOnChain registers all four parent channel views on chain, waits for their
// dispute windows to elapse, then settles each.  At settle, the adjudicator
// recursively unwinds the virtual sub-channel and pays out per the registered
// state — same final outcome as the cooperative path.
func runOnChain(
	ctx context.Context,
	_ *client.SwapClient, // alice — watchers already started via autoWatch
	_ *client.SwapClient, // bob   — likewise
	_ *client.SwapClient, // hub   — likewise (parents tracked via channelsByID)
	chAliceHub, chBobHub, chHubAlice, chHubBob *client.SwapChannel,
	_ *client.SwapChannel, // chAliceBob retained for clarity; not registered directly
) {
	settleCtx, settleCancel := context.WithTimeout(ctx, settleTimeout)
	defer settleCancel()

	parentChs := []*pclient.Channel{
		chAliceHub.Channel(),
		chHubAlice.Channel(),
		chBobHub.Channel(),
		chHubBob.Channel(),
	}
	labels := []string{"Alice-Hub", "Hub-Alice", "Bob-Hub", "Hub-Bob"}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	perm := rng.Perm(len(parentChs))
	log.Printf("[onchain] Register order: %v (random)", perm)
	for _, i := range perm {
		log.Printf("[onchain] Registering parent %s on chain.", labels[i])
		if err := pclient.NewTestChannel(parentChs[i]).Register(ctx); err != nil {
			log.Fatalf("registering %s: %v", labels[i], err)
		}
	}

	// All four watchers (started by autoWatch and the OnNewChannel hook) observe
	// each other's RegisteredEvents and re-register the multi-ledger tree to keep
	// both ledgers in sync. The "invalid version" warnings logged here are
	// benign: a watcher attempting to re-register a state the adjudicator already
	// holds at the same version. Allow the dispute state to settle before we
	// conclude.
	time.Sleep(1 * time.Second)

	// Settle all four views sequentially in a random order. Each parent is one
	// multi-ledger channel viewed by two participants; per pair the first Settle
	// concludes on both chains (primary), the second only withdraws its own funds
	// (secondary). Sequential settle avoids two goroutines racing to conclude the
	// same channel ("already concluded"). Mirrors TestMultiLedgerVirtualDispute.
	chans := []*client.SwapChannel{chAliceHub, chHubAlice, chBobHub, chHubBob}
	log.Println("[onchain] Waiting for dispute windows to elapse, then settling.")
	isSecondary := [2]bool{false, false}
	perm = rng.Perm(len(parentChs))
	log.Printf("[onchain] Settle order: %v (random)", perm)
	for _, i := range perm {
		pair := 0
		if i >= 2 {
			pair = 1
		}
		sec := isSecondary[pair]
		isSecondary[pair] = true
		log.Printf("[onchain] Settling %s (secondary=%v).", labels[i], sec)
		if err := chans[i].SettleCtx(settleCtx, sec); err != nil {
			log.Fatalf("settling %s: %v", labels[i], err)
		}
	}
	log.Println("[onchain] All parents settled.")
}
