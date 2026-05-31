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

// multiledger-virtual-coordinated mirrors go-perun's
// TestMultiLedgerVirtualCoordinate against two real Hardhat chains.
//
// Topology: Alice and Bob each hold a multi-ledger parent ledger channel with
// an intermediary Hub; a virtual Alice-Bob channel is locked as a sub-channel
// inside BOTH parents. The parent channels carry a trusted coordinator (Charlie)
// in their params. After all four parent views are registered on-chain, Charlie
// co-signs the canonical resolution for each parent together with the virtual
// sub-channel's latest signed state, dispatching across both chains via
// multi.Coordinator.Coordinate. The watchers observe the CoordinatedEvent and
// settle completes with no divergence. There is no attacker here — this is the
// honest coordinated-dispute happy path.
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
	keyHub         = "9c7d3e8f1a2b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60718293"
	keyCoordinator = "1c8e5b9a7c3d2e4f8a1b2c3d4e5f67890abcdef1234567890abcdef123456789"

	initialTokenAmount = 100
	challengeDuration  = 5
	settleTimeout      = 2 * time.Minute
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("Coordinated multi-ledger virtual channel demo.")

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

	log.Println("Building in-process coordinator via backends.SetupMultiCoordinator.")
	multiCoord, charlieAccs, charlieAddr, err := startCoordinator(chains)
	if err != nil {
		log.Fatalf("setting up coordinator: %v", err)
	}

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

	log.Println("Setting up Alice, Bob, Hub with coordinator address in parent params.")
	alice := setupSwapClient(aliceBus, keyAlice, chains, aliceWire.Address(), charlieAddr)
	bob := setupSwapClient(bobBus, keyBob, chains, bobWire.Address(), charlieAddr)
	hub := setupSwapClient(hubBus, keyHub, chains, hubWire.Address(), charlieAddr)
	alice.SetChallengeDuration(challengeDuration)
	bob.SetChallengeDuration(challengeDuration)
	hub.SetChallengeDuration(challengeDuration)

	bl := newBalanceLogger(chains[0], chains[1])
	bl.LogBalances("initial", alice.WalletAddress(), bob.WalletAddress(), hub.WalletAddress())

	// Parent ledger channels: Alice [10,10] / Hub [10,10] across (chainA, chainB).
	parentBals := channel.Balances{
		{big.NewInt(10), big.NewInt(10)},
		{big.NewInt(10), big.NewInt(10)},
	}
	log.Println("Opening parent ledger channel Alice <-> Hub (with coordinator).")
	chAliceHub := alice.OpenChannel(hub.WireAddress(), parentBals)
	chHubAlice := hub.AcceptedChannel()
	log.Printf("Alice-Hub parent opened, id=%x", chAliceHub.ID())

	log.Println("Opening parent ledger channel Bob <-> Hub (with coordinator).")
	chBobHub := bob.OpenChannel(hub.WireAddress(), parentBals)
	chHubBob := hub.AcceptedChannel()
	log.Printf("Bob-Hub parent opened, id=%x", chBobHub.ID())

	// Open the virtual Alice-Bob channel routed through both parents. The virtual
	// proposal carries NO coordinator (only the parents do) — see
	// openMultiLedgerVirtualChannels in the upstream reference.
	//   indexMaps: Alice's view {0,1}, Bob's view {1,0}.
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

	// Off-chain virtual update v1: Alice [2,8], Bob [8,2].
	virtualV1 := channel.Balances{
		{big.NewInt(2), big.NewInt(8)},
		{big.NewInt(8), big.NewInt(2)},
	}
	log.Println("Off-chain virtual update to v1 (Alice [2,8], Bob [8,2]).")
	if err := chAliceBob.UpdateBalances(ctx, virtualV1); err != nil {
		log.Fatalf("virtual v1 update: %v", err)
	}
	// Let Hub persist its virtual proxy on both parents and the OnNewChannel hook
	// start Hub's watcher on it, so the multi-ledger re-register can supply the
	// sub-channel state during the dispute below.
	time.Sleep(500 * time.Millisecond)

	// Build the virtual channel's signed sub-state to feed into Coordinate. Each
	// parent has the virtual locked; the coordinator co-signs the parent state
	// together with this sub-state so the adjudicator resolves the whole tree.
	vtcReq := pclient.NewTestChannel(chAliceBob.Channel()).AdjudicatorReq()
	virtualSignedState := channel.SignedState{
		Params: vtcReq.Params,
		State:  vtcReq.Tx.State,
		Sigs:   vtcReq.Tx.Sigs,
	}
	virtualSubStates := []channel.SignedState{virtualSignedState}
	reqAlice := pclient.NewTestChannel(chAliceHub.Channel()).AdjudicatorReq()
	reqBob := pclient.NewTestChannel(chBobHub.Channel()).AdjudicatorReq()

	// Register all four parent channel views on chain in a random order. Each
	// registration includes the virtual sub-channel's latest state via
	// registerDispute -> gatherSubChannelStates.
	parentChs := []*pclient.Channel{
		chAliceHub.Channel(), chHubAlice.Channel(),
		chBobHub.Channel(), chHubBob.Channel(),
	}
	labels := []string{"Alice-Hub", "Hub-Alice", "Bob-Hub", "Hub-Bob"}
	perm := rng.Perm(len(parentChs))
	log.Printf("[coordinated] Register order: %v (random)", perm)
	for _, i := range perm {
		log.Printf("[coordinated] Registering parent %s on chain.", labels[i])
		if err := pclient.NewTestChannel(parentChs[i]).Register(ctx); err != nil {
			log.Fatalf("registering %s: %v", labels[i], err)
		}
	}

	// Let RegisteredEvents propagate and the watchers re-register the multi-ledger
	// tree for cross-ledger sync ("invalid version" warnings below are benign).
	time.Sleep(1 * time.Second)

	// Coordinator co-signs the resolution for each parent. The signing mirrors
	// the test-only MultiLedgerCoordinator.Coordinate wrapper: sign the parent's
	// state, then each virtual sub-state, with Charlie's account, then dispatch
	// across both ledgers via the multi-ledger coordinator. ensureCoordinated
	// waits for the dispute window internally, so no manual timeout wait is
	// needed here.
	bID := wallet.BackendID(ewallet.BackendID)
	virtualSig, err := channel.Sign(charlieAccs[bID], virtualSignedState.State, bID)
	if err != nil {
		log.Fatalf("coordinator signing virtual sub-state: %v", err)
	}

	log.Println("[coordinated] Coordinator co-signs Alice-Hub (parent + virtual sub-state).")
	parentSigA, err := channel.Sign(charlieAccs[bID], reqAlice.Tx.State, bID)
	if err != nil {
		log.Fatalf("coordinator signing Alice-Hub: %v", err)
	}
	if err := multiCoord.Coordinate(ctx, reqAlice, virtualSubStates, []wallet.Sig{parentSigA, virtualSig}); err != nil {
		log.Fatalf("Coordinate(Alice-Hub): %v", err)
	}

	log.Println("[coordinated] Coordinator co-signs Bob-Hub (parent + virtual sub-state).")
	parentSigB, err := channel.Sign(charlieAccs[bID], reqBob.Tx.State, bID)
	if err != nil {
		log.Fatalf("coordinator signing Bob-Hub: %v", err)
	}
	if err := multiCoord.Coordinate(ctx, reqBob, virtualSubStates, []wallet.Sig{parentSigB, virtualSig}); err != nil {
		log.Fatalf("Coordinate(Bob-Hub): %v", err)
	}

	// Let watchers process the CoordinatedEvent and set machine phases to
	// Coordinated, so each Settle's ensureCoordinated returns immediately.
	time.Sleep(1 * time.Second)

	// Settle all four parent views sequentially in a random order. Per pair the
	// first Settle concludes on both chains (primary), the second only withdraws
	// (secondary).
	chans := []*client.SwapChannel{chAliceHub, chHubAlice, chBobHub, chHubBob}
	settleCtx, settleCancel := context.WithTimeout(ctx, settleTimeout)
	defer settleCancel()
	isSecondary := [2]bool{false, false}
	perm = rng.Perm(len(chans))
	log.Printf("[coordinated] Settle order: %v (random)", perm)
	for _, i := range perm {
		pair := 0
		if i >= 2 {
			pair = 1
		}
		sec := isSecondary[pair]
		isSecondary[pair] = true
		log.Printf("[coordinated] Settling %s (secondary=%v).", labels[i], sec)
		if err := chans[i].SettleCtx(settleCtx, sec); err != nil {
			log.Fatalf("settling %s: %v", labels[i], err)
		}
	}
	log.Println("[coordinated] All parents settled.")

	_ = chBobAlice // Bob's virtual view; settled implicitly via the parents.

	bl.LogBalances("final", alice.WalletAddress(), bob.WalletAddress(), hub.WalletAddress())

	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("  COORDINATED VIRTUAL CHANNEL OUTCOME")
	fmt.Println("============================================================")
	fmt.Println("  Virtual v1: Alice [2,8] / Bob [8,2]; locked in two parents.")
	fmt.Println("  Charlie co-signed both parents + the virtual sub-state across")
	fmt.Println("  both chains via multi.Coordinator.Coordinate.")
	fmt.Println("  Expected per-address net change (PRN):")
	fmt.Println("    Alice: chain A -3, chain B +3")
	fmt.Println("    Bob:   chain A +3, chain B -3")
	fmt.Println("    Hub:   chain A  0, chain B  0   (parents net to zero)")
	fmt.Println("============================================================")

	alice.Shutdown()
	bob.Shutdown()
	hub.Shutdown()
}

// startCoordinator constructs the in-process multi-ledger coordinator wired to
// both chains. Returns the coordinator, the wallet account map (for signing),
// and Charlie's on-chain address (used as params.coordinator on the parents).
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
