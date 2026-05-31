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
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/perun-network/perun-eth-backend/bindings/peruntoken"
	ethchannel "github.com/perun-network/perun-eth-backend/channel"
	ethwallet "github.com/perun-network/perun-eth-backend/wallet"
	swallet "github.com/perun-network/perun-eth-backend/wallet/simple"

	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
	"perun.network/go-perun/wire/net"
	p2p "perun.network/go-perun/wire/net/libp2p"
	perunio "perun.network/go-perun/wire/perunio/serializer"

	"perun-multiledger-poc/multiledger-channel/client"
)

// deployContracts deploys PerunToken, Adjudicator, and AssetHolderERC20 on every
// chain and writes the resulting addresses back into the supplied configs.
func deployContracts(chains []client.ChainConfig, fundedAddrs []common.Address) {
	k, err := crypto.HexToECDSA(keyDeployer)
	if err != nil {
		panic(err)
	}
	w := swallet.NewWallet(k)

	for i, chain := range chains {
		cb, err := client.CreateContractBackend(chain.ChainURL, chain.ChainID.Int, w)
		if err != nil {
			panic(err)
		}
		acc := accounts.Account{Address: crypto.PubkeyToAddress(k.PublicKey)}

		token, err := ethchannel.DeployPerunToken(context.TODO(), cb, acc, fundedAddrs, big.NewInt(initialTokenAmount))
		if err != nil {
			panic(err)
		}
		chains[i].Token = token

		chains[i].Adjudicator, err = ethchannel.DeployAdjudicator(context.TODO(), cb, acc)
		if err != nil {
			panic(err)
		}

		chains[i].AssetHolder, err = ethchannel.DeployERC20Assetholder(context.TODO(), cb, chains[i].Adjudicator, token, acc)
		if err != nil {
			panic(err)
		}
	}
}

// setupSwapClient builds a SwapClient for the optimistic virtual-channel PoC.
// acceptAll=true is required so the Hub accepts both ledger parent proposals
// and the relayed virtual-channel proposal; autoWatch=true so each participant
// runs a watcher on every channel it holds — including the Hub on both parent
// views — and the on-chain dispute propagates across chains through them.
func setupSwapClient(
	bus wire.Bus,
	privateKey string,
	chains [2]client.ChainConfig,
	waddress wire.Address,
) *client.SwapClient {
	k, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		panic(err)
	}
	w := swallet.NewWallet(k)
	acc := crypto.PubkeyToAddress(k.PublicKey)

	c, err := client.SetupSwapClient(
		bus,
		w,
		acc,
		chains,
		waddress,
		nil,              // no coordinator notifier
		common.Address{}, // no coordinator address
		true,             // acceptAll — Hub must accept ledger + virtual proposals; participants accept asymmetric balances
		true,             // autoWatch — every participant watches all its channels
	)
	if err != nil {
		panic(err)
	}
	return c
}

// setupBusWire creates a libp2p wire.Bus and dialer for the given account.
func setupBusWire(acc *p2p.Account) (wire.Bus, *p2p.Dialer) {
	id := map[wallet.BackendID]wire.Account{ethwallet.BackendID: acc}

	listener := p2p.NewP2PListener(acc)
	dialer := p2p.NewP2PDialer(acc)

	bus := net.NewBus(id, dialer, perunio.Serializer())

	go bus.Listen(listener)
	return bus, dialer
}

// privKeyToAddress derives an Ethereum address from a hex-encoded private key.
func privKeyToAddress(privateKey string) common.Address {
	pk, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		panic(err)
	}
	return crypto.PubkeyToAddress(pk.PublicKey)
}

// balanceLogger prints PRN token balances on both chains for the supplied
// addresses. Tokens are different contracts per chain, so we keep one each.
type balanceLogger struct {
	ethClientA, ethClientB *ethclient.Client
	tokenA, tokenB         common.Address
}

func newBalanceLogger(chainA, chainB client.ChainConfig) balanceLogger {
	a, err := ethclient.Dial(chainA.ChainURL)
	if err != nil {
		panic(err)
	}
	b, err := ethclient.Dial(chainB.ChainURL)
	if err != nil {
		panic(err)
	}
	return balanceLogger{ethClientA: a, ethClientB: b, tokenA: chainA.Token, tokenB: chainB.Token}
}

func (l balanceLogger) LogBalances(label string, addresses ...common.Address) {
	getBals := func(cb bind.ContractBackend, token common.Address) []*big.Int {
		t, err := peruntoken.NewPeruntoken(token, cb)
		if err != nil {
			panic(err)
		}
		bals := make([]*big.Int, len(addresses))
		for i, a := range addresses {
			bals[i], err = t.BalanceOf(&bind.CallOpts{}, a)
			if err != nil {
				log.Fatal(err)
			}
		}
		return bals
	}

	log.Printf("[%s] chain A PRN balances: %v", label, getBals(l.ethClientA, l.tokenA))
	log.Printf("[%s] chain B PRN balances: %v", label, getBals(l.ethClientB, l.tokenB))
}
