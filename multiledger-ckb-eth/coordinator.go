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
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"

	"cross-chain-coordinator/backends"

	"perun.network/go-perun/channel/multi"
	"perun.network/go-perun/wallet"

	ckbbackend "perun.network/perun-ckb-backend/backend"
	ckbtest "perun.network/perun-ckb-backend/channel/test"
	ckbaddress "perun.network/perun-ckb-backend/wallet/address"
)

// startCoordinator builds the in-process trusted coordinator (Charlie). It wires
// one per-chain backend coordinator (ETH + CKB) into a go-perun multi.Coordinator
// using a single ECDSA key shared across both ledgers. There is NO libp2p relay:
// the demo drives multiCoord.Coordinate directly.
//
// Returns:
//   - the multi.Coordinator the demo dispatches Coordinate / CoordinateVC through,
//   - the coordinator's signing accounts keyed by backend (ETH=1, CKB=3) used to
//     produce the coordinator co-signatures over the parent and virtual states,
//   - the coordinator's on-chain addresses keyed by backend, which the demo embeds
//     in the PARENT channel params via client.WithCoordinator so the parents
//     become eligible for coordinated settlement.
func startCoordinator(
	ethAdjAddr common.Address,
	dep ckbbackend.Deployment,
	network types.Network,
	ckbCharliePk string,
) (*multi.Coordinator, map[wallet.BackendID]wallet.Account, map[wallet.BackendID]wallet.Address, error) {
	ethKey, err := crypto.HexToECDSA(keyCoordinator)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing coordinator key: %w", err)
	}

	// Charlie's CKB funding/signing address (omni-lock EVM-auth, same scheme as
	// the participants). The CKB coordinator pays for and signs its on-chain
	// coordinate transactions from this address.
	ckbKey, err := ckbtest.GetKey(ckbCharliePk)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading coordinator ckb key %s: %w", ckbCharliePk, err)
	}
	charliePart, _, err := ckbaddress.NewEthereumParticipantFromPublicKey(ckbKey.PubKey(), dep.OmniLockScript.CodeHash)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deriving coordinator ckb participant: %w", err)
	}
	signerAddr, err := charliePart.ToCKBAddress(network).Encode()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encoding coordinator ckb address: %w", err)
	}

	// The coordinator loads the CKB deployment from a JSON file; serialise the
	// one we already built from the devnet migrations to a temp file.
	depPath, err := writeDeploymentJSON(dep)
	if err != nil {
		return nil, nil, nil, err
	}

	cfgs := []backends.BackendCoordinatorConfig{
		{
			Type: "eth",
			ETH: &backends.ETHBackendConfig{
				LedgerID:        chainID,
				ChainURL:        chainURL,
				AdjudicatorAddr: ethAdjAddr.Hex(),
			},
		},
		{
			Type: "ckb",
			CKB: &backends.CKBBackendConfig{
				RPCURL:         ckbRPCURL,
				DeploymentFile: depPath,
				Network:        "devnet",
				SignerAddress:  signerAddr,
				UseEVMSigner:   true,
			},
		},
	}

	multiCoord, coordAcc, err := backends.SetupMultiCoordinator(ethKey, cfgs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("backends.SetupMultiCoordinator: %w", err)
	}

	// One coordinator entry PER channel backend, both = Charlie's single key.
	// Each backend reads params.Coordinator by its OWN backend ID: the ETH backend
	// reads key 1 (Charlie's ETH address), the CKB backend reads key 3 (Charlie's
	// CKB participant, whose SEC1 pubkey PackCoordinator consumes and from which it
	// re-derives the ETH-form coordinator). go-perun (fork commit 13cc8f0 "fix:
	// channelID") relaxed proposal validation from "exactly 1" to "≤ #backends",
	// requires every coordinator backend ∈ channel backends, and made CalcID
	// iterate backends in ascending ID order so the multi-ledger channel ID is
	// deterministic and both contracts agree on the coordinator coverage.
	coordAddr := map[wallet.BackendID]wallet.Address{
		ethBackendID: coordAcc[ethBackendID].Address(),
		ckbBackendID: coordAcc[ckbBackendID].Address(),
	}
	log.Printf("Coordinator ready; ETH=%s CKB(signer)=%s",
		crypto.PubkeyToAddress(ethKey.PublicKey).Hex(), signerAddr)
	return multiCoord, coordAcc, coordAddr, nil
}

// writeDeploymentJSON serialises the CKB deployment to a temp JSON file in the
// shape the coordinator's loadCKBDeployment expects (Deployment has full json
// tags). Returns the file path.
func writeDeploymentJSON(dep ckbbackend.Deployment) (string, error) {
	raw, err := json.Marshal(dep)
	if err != nil {
		return "", fmt.Errorf("marshalling ckb deployment: %w", err)
	}
	f, err := os.CreateTemp("", "ckb-deployment-*.json")
	if err != nil {
		return "", fmt.Errorf("creating deployment temp file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return "", fmt.Errorf("writing deployment json: %w", err)
	}
	return f.Name(), nil
}
