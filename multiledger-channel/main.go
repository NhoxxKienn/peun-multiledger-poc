// Copyright 2022 PolyCrypt GmbH
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
	"log"
	"math/big"
	"math/rand"
	"time"

	echannel "github.com/perun-network/perun-eth-backend/channel"
	ewallet "github.com/perun-network/perun-eth-backend/wallet"
	p2p "perun.network/go-perun/wire/net/libp2p"

	"perun-multiledger-poc/multiledger-channel/client"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
)

const (
	// The URLs and IDs of the two chains.
	chainAURL = "ws://127.0.0.1:8545"
	chainAID  = 1337
	chainBURL = "ws://127.0.0.1:8546"
	chainBID  = 1338

	// Private keys.
	keyDeployer    = "79ea8f62d97bc0591a4224c1725fca6b00de5b2cea286fe2e0bb35c5e76be46e"
	keyAlice       = "1af2e950272dd403de7a5760d41c6e44d92b6d02797e51810795ff03cc2cda4f"
	keyBob         = "f63d7d8e930bccd74e93cf5662fde2c28fd8be95edb70c73f1bdd863d07f412e"
	keyCoordinator = "1c8e5b9a7c3d2e4f8a1b2c3d4e5f67890abcdef1234567890abcdef1234567890"

	initialTokenAmount = 100
)

// main runs a demo of a multi-ledger channel. It assumes that two blockchain
// nodes are available at `chainAURL` and `chainBURL` and that the accounts
// corresponding to the specified secret keys are provided with sufficient funds.
func main() {
	// Deploy contracts.
	log.Println("Deploying contracts.")

	chainA := client.ChainConfig{
		ChainID:  echannel.MakeChainID(big.NewInt(chainAID)),
		ChainURL: chainAURL,
	}
	chainB := client.ChainConfig{
		ChainID:  echannel.MakeChainID(big.NewInt(chainBID)),
		ChainURL: chainBURL,
	}

	chains := [2]client.ChainConfig{chainA, chainB}

	// deployContracts will set the contract addresses of the chain
	// configurations after it deployed them.
	deployContracts(chains[:])

	//TODO: setup coordinator and register chains and contracts on it.

	// Setup bus.
	aliceWireAcc := p2p.NewRandomAccount(rand.New(rand.NewSource(time.Now().UnixNano())))
	// aliceCoordinatorNotifier := p2p.NewRelayCoordinatorNotifier(aliceWireAcc)
	aliceBus, aliceDialer := setupBusWire(aliceWireAcc)

	bobWireAcc := p2p.NewRandomAccount(rand.New(rand.NewSource(time.Now().UnixNano())))
	// bobCoordinatorNotifier := p2p.NewRelayCoordinatorNotifier(bobWireAcc)
	bobBus, _ := setupBusWire(bobWireAcc)
	aliceDialer.Register(map[wallet.BackendID]wire.Address{ewallet.BackendID: bobWireAcc.Address()}, bobWireAcc.ID().String())

	// Setup clients.
	log.Println("Setting up clients.")
	alice := setupPaymentClient(aliceBus, keyAlice, chains, aliceWireAcc.Address(), nil)
	bob := setupPaymentClient(bobBus, keyBob, chains, bobWireAcc.Address(), nil)

	// Print balances before transactions.
	l := newBalanceLogger(chains[0], chains[1])
	l.LogBalances(alice.WalletAddress(), bob.WalletAddress())

	// Open channel, transact, close.
	log.Println("Opening channel and depositing funds.")

	// Symmetric funding: both parties fund BOTH chains. In a multi-ledger channel
	// a party can only be paid out on a chain it deposited into, so an asymmetric
	// "Alice funds chain A, Bob funds chain B" split would leave the swapped funds
	// stranded in the asset holders. Here each party funds both chains with
	// unequal shares, and PerformSwap flips the two parties' shares per chain — a
	// real cross-chain swap that settles cleanly on both chains.
	//
	//   chain A: Alice 20, Bob 5   -> after swap  Alice 5,  Bob 20
	//   chain B: Alice 5,  Bob 20  -> after swap  Alice 20, Bob 5
	balances := channel.Balances{
		{big.NewInt(20), big.NewInt(5)},
		{big.NewInt(5), big.NewInt(20)},
	}

	chAlice := alice.OpenChannel(bob.WireAddress(), balances)
	chBob := bob.AcceptedChannel()

	log.Println("Performing the swap...")
	chAlice.PerformSwap()

	log.Println("Settling channel.")
	chAlice.Settle() // Conclude and withdraw.
	chBob.Settle()   // Withdraw.

	// Print balances after transactions.
	l.LogBalances(alice.WalletAddress(), bob.WalletAddress())

	// Cleanup.
	alice.Shutdown()
	bob.Shutdown()
}
