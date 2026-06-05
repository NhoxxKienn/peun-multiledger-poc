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
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/nervosnetwork/ckb-sdk-go/v2/rpc"
	ckbsigner "github.com/nervosnetwork/ckb-sdk-go/v2/transaction/signer"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	ethchannel "github.com/perun-network/perun-eth-backend/channel"
	ethwallet "github.com/perun-network/perun-eth-backend/wallet"
	swallet "github.com/perun-network/perun-eth-backend/wallet/simple"
	"perun.network/perun-ckb-backend/backend"
	"perun.network/perun-ckb-backend/channel/adjudicator"
	ckbasset "perun.network/perun-ckb-backend/channel/asset"
	"perun.network/perun-ckb-backend/channel/funder"
	ckbtest "perun.network/perun-ckb-backend/channel/test"
	ckbclient "perun.network/perun-ckb-backend/client"
	"perun.network/perun-ckb-backend/encoding"
	ckbwallet "perun.network/perun-ckb-backend/wallet"
	ckbaddress "perun.network/perun-ckb-backend/wallet/address"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"

	"perun-multiledger-poc/multiledger-virtual-ckb-eth/client"
)

const (
	ethBackendID = wallet.BackendID(1) // ethwallet.BackendID
	ckbBackendID = wallet.BackendID(3) // ckbasset.CKBBackendID
	gasLimit     = uint64(1000000)
)

// Participant bundles one actor's PaymentClient together with the raw per-chain
// handles (ETH + CKB adjudicators and signing accounts). The cooperative demo
// only needs the PaymentClient; the attack and coordinated demos need the raw
// adjudicators to register/withdraw divergent states directly on each ledger.
type Participant struct {
	Name   string
	Client *client.PaymentClient

	// ETH side.
	EthWallet    *swallet.Wallet
	EthAddr      *ethwallet.Address
	EthAcc       accounts.Account
	EthWalletAcc wallet.Account
	EthAdj       *ethchannel.Adjudicator

	// CKB side.
	CkbAccount *ckbwallet.Account
	CkbAdj     *adjudicator.Adjudicator
	CkbPart    *ckbaddress.Participant
}

// WireAddress is the off-chain identity used as a peer reference.
func (p *Participant) WireAddress() map[wallet.BackendID]wire.Address {
	return p.Client.WireAddress()
}

// setupParticipant builds the full ETH + CKB wiring for one actor and returns a
// Participant. ckbKeyPath points at the actor's CKB secp256k1 key
// (chainckb/accounts/<name>.pk); the same ECDSA key (ethKeyHex) is used on ETH.
func setupParticipant(
	bus wire.Bus,
	name string,
	ethKeyHex string,
	ckbKeyPath string,
	nodeURL string,
	chainID uint64,
	ethAdjAddr common.Address,
	ethAssetAddr ethwallet.Address,
	ckbAsset *ckbasset.NervosAsset,
	dep backend.Deployment,
	network types.Network,
) (*Participant, error) {
	// ---------- ETH ----------
	kEth, err := crypto.HexToECDSA(ethKeyHex)
	if err != nil {
		return nil, fmt.Errorf("%s: parse eth key: %w", name, err)
	}
	ethWallet := swallet.NewWallet(kEth)
	ethAddrRaw := crypto.PubkeyToAddress(kEth.PublicKey)
	ethAddr := ethwallet.AsWalletAddr(ethAddrRaw)
	ethAcc := accounts.Account{Address: ethAddrRaw}
	ethWalletAcc, err := ethWallet.Unlock(ethAddr)
	if err != nil {
		return nil, fmt.Errorf("%s: unlock eth account: %w", name, err)
	}

	// Separate adjudicator instance for raw register/withdraw in the attack
	// (the PaymentClient builds its own internally for the watcher).
	cb, err := client.CreateContractBackend(nodeURL, chainID, ethWallet)
	if err != nil {
		return nil, fmt.Errorf("%s: contract backend: %w", name, err)
	}
	ethAdj := ethchannel.NewAdjudicator(cb, ethAdjAddr, ethAddrRaw, ethAcc, gasLimit)

	// ---------- CKB ----------
	ckbKey, err := ckbtest.GetKey(ckbKeyPath)
	if err != nil {
		return nil, fmt.Errorf("%s: read ckb key %s: %w", name, ckbKeyPath, err)
	}
	omniHash := dep.OmniLockScript.CodeHash
	part, authData, err := ckbaddress.NewEthereumParticipantFromPublicKey(ckbKey.PubKey(), omniHash)
	if err != nil {
		return nil, fmt.Errorf("%s: ckb participant: %w", name, err)
	}
	ckbAccount := ckbwallet.NewAccountFromPrivateKey(ckbKey, omniHash, false)
	ckbWallet := ckbwallet.NewEphemeralWallet()
	if err := ckbWallet.AddAccount(ckbAccount); err != nil {
		return nil, fmt.Errorf("%s: add ckb account: %w", name, err)
	}

	rpcClient, err := rpc.Dial(ckbRPCURL)
	if err != nil {
		return nil, fmt.Errorf("%s: dial ckb: %w", name, err)
	}
	signer := backend.NewEVMSignerInstance(part.ToCKBAddress(network), *ckbKey, network, authData)
	txSigner := signer.Signer()
	txSigner.RegisterLockSigner(dep.OmniLockScript.CodeHash, &ckbsigner.OmnilockSigner{})
	ckbCli, err := ckbclient.NewClient(rpcClient, signer, dep)
	if err != nil {
		return nil, fmt.Errorf("%s: ckb client: %w", name, err)
	}
	ckbFunder := funder.NewDefaultFunder(ckbCli, dep)
	ckbFunder.MaxIterationsUntilAbort = 80

	// Multi-ledger asset factory. When the CKB adjudicator reconstructs a channel
	// state from the on-chain molecule encoding (in Subscribe and on the virtual-
	// channel dispute path), it must map each asset row back to the SAME asset
	// objects and backend IDs used at channel open, so multi.Adjudicator routing
	// stays consistent. The single-ledger default factory mislabels the embedded
	// ETH row, which leads to a nil-pointer dereference when the watcher tries to
	// dispute the virtual channel. Mirrors the backend's multiLedgerAssetFactory.
	ethAssetObj := ethchannel.NewAsset(big.NewInt(int64(chainID)), common.Address(ethAssetAddr))
	assetFactory := func(d encoding.AssetDescriptor) (channel.Asset, wallet.BackendID, error) {
		if d.IsCKByte || d.SUDT != nil {
			return ckbAsset, ckbBackendID, nil
		}
		return ethAssetObj, ethBackendID, nil
	}
	ckbAdj := adjudicator.NewAdjudicatorWithAssetFactory(ckbCli, assetFactory)

	// ---------- PaymentClient ----------
	c, err := client.SetupPaymentClient(
		bus, ethWallet, ethAddrRaw, ethAddr, nodeURL, chainID,
		ethAdjAddr, ethAssetAddr, ckbWallet, ckbAccount, ckbAsset, ckbFunder, ckbAdj,
	)
	if err != nil {
		return nil, fmt.Errorf("%s: setup payment client: %w", name, err)
	}

	return &Participant{
		Name:         name,
		Client:       c,
		EthWallet:    ethWallet,
		EthAddr:      ethAddr,
		EthAcc:       ethAcc,
		EthWalletAcc: ethWalletAcc,
		EthAdj:       ethAdj,
		CkbAccount:   ckbAccount,
		CkbAdj:       ckbAdj,
		CkbPart:      part,
	}, nil
}
