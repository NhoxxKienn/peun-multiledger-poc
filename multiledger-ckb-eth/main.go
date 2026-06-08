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

// multiledger-ckb-eth is the CKB↔ETH multi-ledger PAYMENT channel PoC: a single
// 2-party Perun channel between Alice and Bob that spans Ethereum (ETH, via
// Hardhat) and Nervos CKB (CKByte, via a local offckb devnet). It is the
// payment-channel (non-virtual) counterpart of the ETH-ETH multiledger-attack /
// multiledger-defended modules, run on the CKB+ETH substrate. The thesis maps
// its modes to Scenario 1 (attack) and Scenario 2 (coordinated).
//
// Three flows are selected via -mode:
//
//	-mode=cooperative — the honest baseline: open the channel, do an agreed
//	                    cross-asset update, then settle cooperatively (no dispute,
//	                    no coordinator).
//	-mode=attack      — the divergent-settlement attack (POC §2): Bob holds two
//	                    co-signed states (v0, v1) and registers the stale v0 on
//	                    CKB and the newer v1 on ETH, so each ledger settles a
//	                    different version. Bob banks his best share on each chain;
//	                    Alice is short-changed. I1, I2 violated.
//	-mode=coordinated — the parent-only coordinator defence: the channel carries a
//	                    trusted cross-chain coordinator (Charlie); when Bob tries
//	                    the divergence, the coordinator pins ONE canonical version
//	                    on both ledgers, so both settle the same state. I1, I2, I3
//	                    consistent.
//
// There is NO libp2p relay: the coordinated mode drives multi.Coordinator
// .Coordinate inline.
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
	"perun-multiledger-poc/multiledger-ckb-eth/ethereumUtil"
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
	keyCoordinator = "1c8e5b9a7c3d2e4f8a1b2c3d4e5f67890abcdef1234567890abcdef123456789"

	// CKB devnet artefacts (shared with the other CKB↔ETH modules via a chainckb
	// symlink). The same secp256k1 key signs on both ledgers per actor.
	ckbAlicePk    = "./chainckb/accounts/alice.pk"
	ckbBobPk      = "./chainckb/accounts/bob.pk"
	ckbCharliePk  = "./chainckb/accounts/charlie.pk"
	sudtOwnerPath = "./chainckb/accounts/sudt-owner-lock-hash.txt"
	migrations0   = "./chainckb/contract/migrations_0/dev/"
	migrations1   = "./chainckb/contract/migrations_1/dev/"
	migrationsVC  = "./chainckb/contract/migrations_vc/dev/"
	systemScripts = "./chainckb/system_scripts"

	// Channel funding amounts: both parties fund BOTH ledgers (symmetric), which
	// is what lets a cross-asset update settle on every chain.
	parentEth = 4.0
	parentCkb = uint64(100)

	// attackChallenge is the on-chain challenge duration (seconds) used in the
	// attack/coordinated flows. CKB has no evm_increaseTime analog, so dispute and
	// coordinate windows are waited out in wall-clock time — keep it small.
	attackChallenge = uint64(15)
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	mode := flag.String("mode", "cooperative", "settlement mode: cooperative | attack | coordinated")
	flag.Parse()
	if *mode != "cooperative" && *mode != "attack" && *mode != "coordinated" {
		log.Fatalf("unknown -mode %q (want cooperative | attack | coordinated)", *mode)
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
	log.Println("Setting up Alice, Bob.")
	alice, err := setupParticipant(bus, "alice", keyAlice, ckbAlicePk, chainURL, chainID, ethAdjAddr, asset, &ckbAsset, dep, network)
	if err != nil {
		log.Fatalf("setup alice: %v", err)
	}
	bob, err := setupParticipant(bus, "bob", keyBob, ckbBobPk, chainURL, chainID, ethAdjAddr, asset, &ckbAsset, dep, network)
	if err != nil {
		log.Fatalf("setup bob: %v", err)
	}
	log.Println("Participants:", alice.WireAddress(), bob.WireAddress())

	// In coordinated mode, build the in-process trusted coordinator (Charlie) and
	// embed it in the channel params so the channel becomes eligible for
	// coordinated settlement.
	var multiCoord *coordinatorBundle
	if *mode == "coordinated" {
		log.Println("Starting cross-chain coordinator (Charlie).")
		coord, coordAcc, coordAddr, cerr := startCoordinator(ethAdjAddr, dep, network, ckbCharliePk)
		if cerr != nil {
			log.Fatalf("starting coordinator: %v", cerr)
		}
		multiCoord = &coordinatorBundle{coord: coord, acc: coordAcc}
		for _, p := range []*Participant{alice, bob} {
			p.Client.SetCoordinator(coordAddr)
		}
	}

	// The attack and coordinated flows drive each ledger's adjudicator directly
	// with divergent states and wait dispute/coordinate windows out in wall-clock
	// time, so they shrink the challenge window. Both watchers are disabled so the
	// run is deterministic (no watcher racing the hand-driven low-level disputes) —
	// the same choice the un-coordinated CKB↔ETH virtual attack makes. The
	// watcher-insufficiency nuance is the job of the virtual coordinated module
	// (Sc 4), which keeps honest watchers ON; here the parent-level divergence
	// (attack) and its coordinator fix (coordinated) are shown directly.
	if *mode == "attack" || *mode == "coordinated" {
		for _, p := range []*Participant{alice, bob} {
			p.Client.SetChallengeDuration(attackChallenge)
			p.Client.SetAutoWatch(false)
		}
	}

	// Evaluation-metrics bundle (a no-op unless EVAL_OUT is set). Maps the
	// attack/coordinated modes to the thesis scenario IDs they realise.
	thesisSc := map[string]string{"attack": "Sc1", "coordinated": "Sc2"}[*mode]
	m := newMeters("multiledger-ckb-eth", *mode, thesisSc, chainURL, ckbRPCURL, ethAdjAddr.Hex(), ethAssetAddr.Hex())
	m.config("challengeDurationS", attackChallenge)
	m.config("ethChainID", chainID)

	// Log balances before (and record them for the evaluation).
	ethLog := ethereumUtil.NewBalanceLogger(chainURL)
	ethLog.LogBalances(alice.Client.WalletEthAddress(), bob.Client.WalletEthAddress())
	ckbLog := ethereumUtil.NewCKBBalanceLogger(ckbRPCURL)
	ckbLog.LogBalances(alice.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(bob.CkbAccount.Address().(*ckbaddress.Participant))
	recordBalances(m, "pre", alice, bob, ckbLog)

	// Open the single 2-party multi-ledger channel. Bob proposes, so Bob is index
	// 0 (the primary disputer) and Alice is index 1 (secondary) — mirrors the
	// verified Bob-as-proposer semantics of the CKB↔ETH virtual modules.
	log.Println("Opening multi-ledger channel Bob <-> Alice.")
	chBA := bob.Client.OpenChannel(alice.WireAddress(), parentEth, parentCkb)
	chAB := alice.Client.AcceptedChannel()
	log.Printf("Channel opened, id=%x", chBA.ID())

	var outcome string
	switch *mode {
	case "cooperative":
		runCooperative(m, chBA, chAB)
		outcome = "COOPERATIVE OK"
	case "attack":
		runAttack(m, bob, alice, chBA, chAB)
		outcome = "ATTACK SUCCEEDED"
	case "coordinated":
		runCoordinated(m, multiCoord, bob, alice, chBA, chAB)
		outcome = "ATTACK PREVENTED"
	}

	// Log balances after (and record them for the evaluation).
	ethLog.LogBalances(alice.Client.WalletEthAddress(), bob.Client.WalletEthAddress())
	ckbLog.LogBalances(alice.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(bob.CkbAccount.Address().(*ckbaddress.Participant))
	recordBalances(m, "post", alice, bob, ckbLog)
	m.finish(outcome, true)

	alice.Client.Shutdown()
	bob.Client.Shutdown()
}

// recordBalances snapshots Alice's and Bob's ETH (native, wei) and CKB (cell
// capacity, shannons) balances under the given phase ("pre"/"post").
func recordBalances(m *meters, when string, alice, bob *Participant, ckbLog ethereumUtil.CkbBalanceLogger) {
	m.balance("alice_eth_"+when, eval.ETHBalanceWei(chainURL, alice.Client.WalletEthAddress().Hex()))
	m.balance("bob_eth_"+when, eval.ETHBalanceWei(chainURL, bob.Client.WalletEthAddress().Hex()))
	m.balance("alice_ckb_"+when, u64(ckbLog.Capacity(alice.CkbAccount.Address().(*ckbaddress.Participant))))
	m.balance("bob_ckb_"+when, u64(ckbLog.Capacity(bob.CkbAccount.Address().(*ckbaddress.Participant))))
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
