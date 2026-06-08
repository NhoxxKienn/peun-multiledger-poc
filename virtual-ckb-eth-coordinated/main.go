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

// multiledger-virtual-ckb-eth-coordinated is the COORDINATED counterpart of the
// multiledger-virtual-ckb-eth module: the two parent multi-ledger channels
// (Alice-Ingrid, Bob-Ingrid) carry a trusted cross-chain coordinator (Charlie)
// in their params, and a virtual Alice-Bob channel is locked inside both.
//
// It demonstrates the recursive-coordination DEFENCE: Bob attempts the
// stale-state-on-virtual attack, but the adjudicator forces any coordination of a
// VC-locking parent to also co-sign the virtual sub-state, and the coordinator
// pins ONE global-canonical virtual version on both ledgers via CoordinateVC. The
// virtual channel settles uniformly and the attack is prevented. (The divergent
// attack itself is shown in the un-coordinated multiledger-virtual-ckb-eth
// module.)
//
// There is NO libp2p relay: the demo drives multi.Coordinator.Coordinate inline.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	ethwallet "github.com/perun-network/perun-eth-backend/wallet"
	"perun.network/go-perun/wire"
	ckbasset "perun.network/perun-ckb-backend/channel/asset"
	ckbtest "perun.network/perun-ckb-backend/channel/test"
	ckbaddress "perun.network/perun-ckb-backend/wallet/address"

	"perun-multiledger-poc/eval"
	"perun-multiledger-poc/multiledger-virtual-ckb-eth-coordinated/ethereumUtil"
)

const (
	chainURL = "ws://127.0.0.1:8545"
	chainID  = 1337
	// ckbRPCURL is declared in setup.go (shared package-level const).

	// ETH private keys (also used, byte-for-byte, as the CKB omni-lock keys in
	// chainckb/accounts/<name>.pk so each actor has one identity per chain).
	keyDeployer    = "79ea8f62d97bc0591a4224c1725fca6b00de5b2cea286fe2e0bb35c5e76be46e"
	keyAlice       = "1af2e950272dd403de7a5760d41c6e44d92b6d02797e51810795ff03cc2cda4f"
	keyBob         = "f63d7d8e930bccd74e93cf5662fde2c28fd8be95edb70c73f1bdd863d07f412e"
	keyIngrid      = "9c7d3e8f1a2b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60718293"
	keyCoordinator = "1c8e5b9a7c3d2e4f8a1b2c3d4e5f67890abcdef1234567890abcdef123456789"

	// CKB devnet artefacts (shared with the optimistic module via a chainckb
	// symlink). The same secp256k1 key signs on both ledgers per actor.
	ckbAlicePk    = "./chainckb/accounts/alice.pk"
	ckbBobPk      = "./chainckb/accounts/bob.pk"
	ckbIngridPk   = "./chainckb/accounts/ingrid.pk"
	ckbCharliePk  = "./chainckb/accounts/charlie.pk"
	sudtOwnerPath = "./chainckb/accounts/sudt-owner-lock-hash.txt"
	migrations0   = "./chainckb/contract/migrations_0/dev/"
	migrations1   = "./chainckb/contract/migrations_1/dev/"
	migrationsVC  = "./chainckb/contract/migrations_vc/dev/"
	systemScripts = "./chainckb/system_scripts"

	// Channel funding amounts (match the optimistic module).
	parentEth  = 4.0
	parentCkb  = uint64(100)
	virtualEth = 2.0
	virtualCkb = uint64(50)

	// attackChallenge is the on-chain challenge duration (seconds). CKB has no
	// evm_increaseTime analog, so dispute/coordinate windows are waited out in
	// wall-clock time — keep it small.
	attackChallenge = uint64(15)
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	// This module has a single flow (the recursive-coordination defence). The
	// -mode flag is accepted only so the evaluation harness (bench/run.sh) can
	// drive every scenario uniformly; the sole valid value is "defended".
	mode := flag.String("mode", "defended", "settlement mode: defended")
	flag.Parse()
	if *mode != "defended" {
		log.Fatalf("unknown -mode %q (this module only supports: defended)", *mode)
	}

	// Deploy the ETH Perun contracts (adjudicator + ETH asset holder).
	log.Println("Deploying ETH contracts.")
	ethAdjAddr, ethAssetAddr := ethereumUtil.DeployContracts(chainURL, chainID, keyDeployer)
	log.Println("ETH adjudicator:", ethAdjAddr.Hex())
	log.Println("ETH asset holder:", ethAssetAddr.Hex())
	asset := *ethwallet.AsWalletAddr(ethAssetAddr)

	// Load the CKB deployment (Perun script cells) produced by the devnet.
	sudtOwnerLockArg, err := parseSUDTOwnerLockArg(sudtOwnerPath)
	if err != nil {
		log.Fatalf("reading sudt owner lock arg: %v", err)
	}
	dep, _, err := ckbtest.GetDeployment(migrations0, migrations1, migrationsVC, systemScripts, sudtOwnerLockArg)
	if err != nil {
		log.Fatalf("getting CKB deployment: %v", err)
	}

	// Shared CKB asset (CKBytes) and network.
	ckbBytesAsset := ckbasset.NewCKBytesAsset()
	ckbCCID := ckbasset.MakeCCID(ckbasset.MakeContractID("03"))
	ckbAsset := ckbasset.NewNervosAsset(*ckbBytesAsset, ckbCCID)
	network := types.NetworkTest

	bus := wire.NewLocalBus()
	log.Println("Setting up Alice, Bob, Ingrid.")
	alice, err := setupParticipant(bus, "alice", keyAlice, ckbAlicePk, chainURL, chainID, ethAdjAddr, asset, &ckbAsset, dep, network)
	if err != nil {
		log.Fatalf("setup alice: %v", err)
	}
	bob, err := setupParticipant(bus, "bob", keyBob, ckbBobPk, chainURL, chainID, ethAdjAddr, asset, &ckbAsset, dep, network)
	if err != nil {
		log.Fatalf("setup bob: %v", err)
	}
	ingrid, err := setupParticipant(bus, "ingrid", keyIngrid, ckbIngridPk, chainURL, chainID, ethAdjAddr, asset, &ckbAsset, dep, network)
	if err != nil {
		log.Fatalf("setup ingrid: %v", err)
	}
	log.Println("Participants:", alice.WireAddress(), bob.WireAddress(), ingrid.WireAddress())

	// Build the in-process trusted coordinator (Charlie) over both ledgers.
	log.Println("Starting cross-chain coordinator (Charlie).")
	multiCoord, coordAcc, coordAddr, err := startCoordinator(ethAdjAddr, dep, network, ckbCharliePk)
	if err != nil {
		log.Fatalf("starting coordinator: %v", err)
	}

	// Configure every participant before any channel is proposed: embed the
	// coordinator in the PARENT params, and shrink the challenge window so CKB's
	// wall-clock dispute/coordinate timer is tractable.
	for _, p := range []*Participant{alice, bob, ingrid} {
		p.Client.SetCoordinator(coordAddr)
		p.Client.SetChallengeDuration(attackChallenge)
	}
	// The honest parties (Alice the victim, Ingrid the intermediary) keep their
	// watchtower ON — the intended Perun defence: they watch their channels and
	// react to Bob's stale on-chain registration. Only the attacker (Bob) runs
	// without a watcher, since his own watcher would refute his own stale state.
	bob.Client.SetAutoWatch(false)

	// Evaluation-metrics bundle (a no-op unless EVAL_OUT is set). This module
	// realises thesis Scenario 4 (coordinator active, with virtual sub-channel).
	m := newMeters("virtual-ckb-eth-coordinated", "defended", "Sc4", chainURL, ckbRPCURL, ethAdjAddr.Hex(), ethAssetAddr.Hex())
	m.config("challengeDurationS", attackChallenge)
	m.config("ethChainID", chainID)

	// Log balances before (and record them for the evaluation).
	ethLog := ethereumUtil.NewBalanceLogger(chainURL)
	ethLog.LogBalances(alice.Client.WalletEthAddress(), bob.Client.WalletEthAddress(), ingrid.Client.WalletEthAddress())
	ckbLog := ethereumUtil.NewCKBBalanceLogger(ckbRPCURL)
	ckbLog.LogBalances(alice.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(bob.CkbAccount.Address().(*ckbaddress.Participant))
	recordBalances(m, "pre", alice, bob, ingrid, ckbLog)

	// Open the two parent multi-ledger channels (coordinated) and the virtual one.
	log.Println("Opening parent channel Alice <-> Ingrid (coordinated).")
	chAI := alice.Client.OpenChannel(ingrid.WireAddress(), parentEth, parentCkb)
	chIA := ingrid.Client.AcceptedChannel()
	log.Printf("Alice-Ingrid opened, id=%x", chAI.ID())

	log.Println("Opening parent channel Bob <-> Ingrid (coordinated).")
	chBI := bob.Client.OpenChannel(ingrid.WireAddress(), parentEth, parentCkb)
	chIB := ingrid.Client.AcceptedChannel()
	log.Printf("Bob-Ingrid opened, id=%x", chBI.ID())

	log.Println("Opening virtual channel Alice <-> Bob.")
	chAB := alice.Client.OpenVirtualChannel(bob.WireAddress(), virtualEth, virtualCkb, chAI.ID(), chBI.ID())
	chBA := bob.Client.AcceptedChannel()
	log.Printf("Virtual Alice-Bob opened, id=%x", chAB.ID())

	runDefended(m, multiCoord, coordAcc, alice, bob, ingrid, chAB, chBA, chAI, chIA, chBI, chIB)

	// Log balances after (and record them for the evaluation).
	ethLog.LogBalances(alice.Client.WalletEthAddress(), bob.Client.WalletEthAddress(), ingrid.Client.WalletEthAddress())
	ckbLog.LogBalances(alice.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(bob.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(ingrid.CkbAccount.Address().(*ckbaddress.Participant))
	recordBalances(m, "post", alice, bob, ingrid, ckbLog)
	m.finish("ATTACK PREVENTED", true)

	alice.Client.Shutdown()
	bob.Client.Shutdown()
	ingrid.Client.Shutdown()
}

// recordBalances snapshots Alice's, Bob's and Ingrid's ETH (native, wei) and CKB
// (cell capacity, shannons) balances under the given phase ("pre"/"post"). The
// intermediary (Ingrid) balances support the financial-neutrality observation.
func recordBalances(m *meters, when string, alice, bob, ingrid *Participant, ckbLog ethereumUtil.CkbBalanceLogger) {
	for _, p := range []*Participant{alice, bob, ingrid} {
		m.balance(p.Name+"_eth_"+when, eval.ETHBalanceWei(chainURL, p.Client.WalletEthAddress().Hex()))
		m.balance(p.Name+"_ckb_"+when, u64(ckbLog.Capacity(p.CkbAccount.Address().(*ckbaddress.Participant))))
	}
}

// parseSUDTOwnerLockArg reads the SUDT owner lock hash the CKB devnet wrote to
// chainckb/accounts/sudt-owner-lock-hash.txt (a single "0x…"-prefixed hex line).
func parseSUDTOwnerLockArg(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	arg := strings.TrimSpace(string(data))
	if arg == "" {
		return "", fmt.Errorf("%s is empty — run `make dev` in chainckb to generate it", path)
	}
	return arg, nil
}
