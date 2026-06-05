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

// multiledger-virtual-ckb-eth demonstrates a multi-ledger virtual channel
// between Alice and Bob routed via an intermediary Ingrid, where each parent
// ledger channel spans Ethereum (ETH, via Hardhat) and Nervos CKB (CKByte, via
// a local offckb devnet). It is the CKB↔ETH counterpart of the ETH-ETH
// multiledger-virtual-optimistic module. There is NO coordinator here.
//
// Two flows are selected via -mode:
//
//	-mode=cooperative — the upstream happy path: open parents + virtual, then
//	                    finalize the virtual off-chain and settle everything
//	                    cooperatively (no on-chain dispute).
//	-mode=attack      — the divergent-settlement attack (POC §11.5): Bob
//	                    registers the agreed state on CKB and a fabricated
//	                    higher-version state on ETH for the Bob-Ingrid parent,
//	                    so each ledger pays out a different version. Mirrors the
//	                    ETH-ETH multiledger-attack mechanics across CKB+ETH.
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

	"perun-multiledger-poc/multiledger-virtual-ckb-eth/client"
	"perun-multiledger-poc/multiledger-virtual-ckb-eth/ethereumUtil"
)

const (
	chainURL  = "ws://127.0.0.1:8545"
	chainID   = 1337
	ckbRPCURL = "http://127.0.0.1:8114"

	// ETH private keys (also used, byte-for-byte, as the CKB omni-lock keys in
	// chainckb/accounts/<name>.pk so each actor has one identity per chain).
	keyDeployer = "79ea8f62d97bc0591a4224c1725fca6b00de5b2cea286fe2e0bb35c5e76be46e"
	keyAlice    = "1af2e950272dd403de7a5760d41c6e44d92b6d02797e51810795ff03cc2cda4f"
	keyBob      = "f63d7d8e930bccd74e93cf5662fde2c28fd8be95edb70c73f1bdd863d07f412e"
	keyIngrid   = "9c7d3e8f1a2b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f60718293"

	// CKB devnet artefacts written by `cd chainckb && make dev` (the fork's
	// devnet). The CKB ledger uses the phase-0 Perun-script deployment
	// (migrations_0); migrations_vc holds the virtual-channel scripts (VCTS/VCLS).
	// migrations_1 (the fork's second-CKB-ledger deployment) is unused here — our
	// second ledger is Ethereum.
	ckbAlicePk    = "./chainckb/accounts/alice.pk"
	ckbBobPk      = "./chainckb/accounts/bob.pk"
	ckbIngridPk   = "./chainckb/accounts/ingrid.pk"
	sudtOwnerPath = "./chainckb/accounts/sudt-owner-lock-hash.txt"
	migrations0   = "./chainckb/contract/migrations_0/dev/"
	migrations1   = "./chainckb/contract/migrations_1/dev/"
	migrationsVC  = "./chainckb/contract/migrations_vc/dev/"
	systemScripts = "./chainckb/system_scripts"

	// Channel funding amounts (per the upstream example).
	parentEth  = 4.0
	parentCkb  = uint64(100)
	virtualEth = 2.0
	virtualCkb = uint64(50)

	// attackChallenge is the on-chain challenge duration (seconds) used in
	// -mode=attack. Dispute windows are waited out in wall-clock time on both
	// ledgers (no evm_increaseTime), so keep it small. (POC §13.)
	attackChallenge = uint64(15)
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	mode := flag.String("mode", "cooperative", "settlement mode: cooperative | attack")
	flag.Parse()
	if *mode != "cooperative" && *mode != "attack" {
		log.Fatalf("unknown -mode %q (want cooperative | attack)", *mode)
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

	// In attack mode the dispute window must be small enough to wait out in
	// wall-clock time (CKB cannot fast-forward). Set it before any channel is
	// proposed, since the challenge duration is fixed in the channel params.
	if *mode == "attack" {
		for _, p := range []*Participant{alice, bob, ingrid} {
			p.Client.SetChallengeDuration(attackChallenge)
			// The attack drives each ledger's adjudicator directly with divergent
			// virtual sub-states; an honest watcher would race those disputes.
			p.Client.SetAutoWatch(false)
		}
	}

	// Log balances before.
	ethLog := ethereumUtil.NewBalanceLogger(chainURL)
	ethLog.LogBalances(alice.Client.WalletEthAddress(), bob.Client.WalletEthAddress(), ingrid.Client.WalletEthAddress())
	ckbLog := ethereumUtil.NewCKBBalanceLogger(ckbRPCURL)
	ckbLog.LogBalances(alice.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(bob.CkbAccount.Address().(*ckbaddress.Participant))

	// Open the two parent multi-ledger ledger channels (Alice-Ingrid, Bob-Ingrid).
	log.Println("Opening parent channel Alice <-> Ingrid.")
	chAI := alice.Client.OpenChannel(ingrid.WireAddress(), parentEth, parentCkb)
	chIA := ingrid.Client.AcceptedChannel()
	log.Printf("Alice-Ingrid opened, id=%x", chAI.ID())

	log.Println("Opening parent channel Bob <-> Ingrid.")
	chBI := bob.Client.OpenChannel(ingrid.WireAddress(), parentEth, parentCkb)
	chIB := ingrid.Client.AcceptedChannel()
	log.Printf("Bob-Ingrid opened, id=%x", chBI.ID())

	// Open the virtual Alice <-> Bob channel locked inside both parents.
	log.Println("Opening virtual channel Alice <-> Bob.")
	chAB := alice.Client.OpenVirtualChannel(bob.WireAddress(), virtualEth, virtualCkb, chAI.ID(), chBI.ID())
	chBA := bob.Client.AcceptedChannel()
	log.Printf("Virtual Alice-Bob opened, id=%x", chAB.ID())

	switch *mode {
	case "cooperative":
		runCooperative(chAB, chBA, chAI, chIA, chBI, chIB)
	case "attack":
		runAttack(alice, bob, ingrid, chAB, chBA, chAI, chIA, chBI, chIB)
	}

	// Log balances after.
	ethLog.LogBalances(alice.Client.WalletEthAddress(), bob.Client.WalletEthAddress(), ingrid.Client.WalletEthAddress())
	ckbLog.LogBalances(alice.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(bob.CkbAccount.Address().(*ckbaddress.Participant))
	ckbLog.LogBalances(ingrid.CkbAccount.Address().(*ckbaddress.Participant))

	alice.Client.Shutdown()
	bob.Client.Shutdown()
	ingrid.Client.Shutdown()
}

// runCooperative mirrors the upstream happy path: finalize the virtual channel
// off-chain, settle both virtual views, then settle the four parent views.
func runCooperative(chAB, chBA, chAI, chIA, chBI, chIB *client.PaymentChannel) {
	log.Println("Closing virtual channel cooperatively.")
	chAB.Finalize()

	chs := []*client.PaymentChannel{chAB, chBA}
	done := make(chan struct{}, len(chs))
	for _, ch := range chs {
		go func(ch *client.PaymentChannel) {
			ch.Settle(false)
			done <- struct{}{}
		}(ch)
	}
	for range chs {
		<-done
	}
	log.Println("Virtual channel closed.")

	log.Println("Settling parent channels.")
	chAI.Settle(false)
	chIA.Settle(true)
	chBI.Settle(false)
	chIB.Settle(true)
	log.Println("Parent channels settled.")
}

// parseSUDTOwnerLockArg reads the SUDT owner lock hash that the CKB devnet wrote
// to chainckb/accounts/sudt-owner-lock-hash.txt (a single "0x…"-prefixed hex
// line). deployment.GetDeployment strips the leading "0x" and decodes the rest.
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
