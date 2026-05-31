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

package client

import (
	"context"
	"fmt"
	"sync"

	ethchannel "github.com/perun-network/perun-eth-backend/channel"
	ethwallet "github.com/perun-network/perun-eth-backend/wallet"
	swallet "github.com/perun-network/perun-eth-backend/wallet/simple"
	p2p "perun.network/go-perun/wire/net/libp2p"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/channel/multi"
	"perun.network/go-perun/client"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/watcher/local"
	"perun.network/go-perun/wire"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
)

const (
	txFinalityDepth = 1 // Number of blocks required to confirm a transaction.
)

// ChainConfig is used to hold all information needed about a specific chain.
type ChainConfig struct {
	ChainID     ethchannel.ChainID
	ChainURL    string
	Token       common.Address // The address of the deployed ERC20 token.
	Adjudicator common.Address // The address of the deployed Adjudicator contract.
	AssetHolder common.Address // The address of the deployed AssetHolder contract.
}

// SwapClient is a channel client for swaps.
type SwapClient struct {
	perunClient   *client.Client                      // The core Perun client.
	account       map[wallet.BackendID]wallet.Address // The account we use for on-chain and off-chain transactions.
	walletAcc     wallet.Account                      // Unlocked account used for off-chain state signing.
	currencies    [2]channel.Asset                    // The currencies of the different chains we support.
	channels      chan *SwapChannel                   // Accepted payment channels (cap 10 to support 3-party virtual-channel flows where Hub accepts multiple proposals in flight).
	waddresss     map[wallet.BackendID]wire.Address   // The wire address of the client, used for off-chain communication.
	adjudicators  [2]*ethchannel.Adjudicator          // Per-chain adjudicators, exposed for direct attack-flow access.
	coordinator   map[wallet.BackendID]wallet.Address // Optional trusted coordinator address (nil = no coordinator).
	lastChannel   *client.Channel                     // Last opened or accepted channel (for tests/attack flow).
	channelsByID  map[channel.ID]*client.Channel      // Indexed lookup for multi-channel flows (virtual demos).
	channelsByIDM sync.Mutex                          // Guards channelsByID against concurrent OpenChannel / HandleProposal.
	watching      map[channel.ID]struct{}             // Set of channel IDs already handed to a watcher (dedup).
	watchingM     sync.Mutex                          // Guards watching.
	acceptAll     bool                                // When true, accept any 2-party proposal and any update (attack/defended/virtual flows).
	autoWatch     bool                                // When true, automatically Watch() every opened/accepted channel (default behavior).
	challengeDur  uint64                              // On-chain challenge duration in seconds; default 10. Set via SetChallengeDuration.
}

// SetupSwapClient creates a new swap client. A non-zero `coordinator` enables
// multi-ledger coordination on every channel proposed by this client; the
// zero-value (empty address) disables it (current honest-demo behavior).
func SetupSwapClient(
	bus wire.Bus, // bus is used of off-chain communication.
	w *swallet.Wallet, // w is the wallet used for signing transactions.
	acc common.Address, // acc is the address of the account to be used for signing transactions.
	chains [2]ChainConfig, // chains represent the two chains the client should be able to use.
	waddress wire.Address, // waddress is the wire address of the client, used for off-chain communication.
	coordinatorNotifier *p2p.RelayCoordinatorNotifier, // coordinatorNotifier is used for coordination communication.
	coordinator common.Address, // Optional trusted coordinator address; zero-value means "no coordinator".
	acceptAll bool, // When true, the proposal/update handlers accept any well-formed message (used by attack/defended demos).
	autoWatch bool, // When true, automatically Watch() every opened/accepted channel. Disable for the attack demo where the watcher's replication breaks the divergent-register flow.
) (*SwapClient, error) {
	// The multi-funder and multi-adjudicator will be registered with a funder /
	// adjudicators for each chain.
	multiFunder := multi.NewFunder()
	multiAdjudicator := multi.NewAdjudicator()

	var assets [2]channel.Asset
	for i, chain := range chains {
		assets[i] = ethchannel.NewAsset(chain.ChainID.Int, chain.AssetHolder)
	}

	var adjs [2]*ethchannel.Adjudicator
	for i, chain := range chains {
		// Create Ethereum client and contract backend.
		cb, err := CreateContractBackend(chain.ChainURL, chain.ChainID.Int, w)
		if err != nil {
			return nil, fmt.Errorf("creating contract backend: %w", err)
		}

		// Validate contracts.
		err = ethchannel.ValidateAdjudicator(context.TODO(), cb, chain.Adjudicator)
		if err != nil {
			return nil, fmt.Errorf("validating adjudicator: %w", err)
		}
		err = ethchannel.ValidateAssetHolderERC20(context.TODO(), cb, chain.AssetHolder, chain.Adjudicator, chain.Token)
		if err != nil {
			return nil, fmt.Errorf("validating adjudicator: %w", err)
		}

		// Setup funder.
		funder := ethchannel.NewFunder(cb)
		// Register the asset on the funder.
		dep := ethchannel.NewERC20Depositor(chain.Token, 100000)
		ethAcc := accounts.Account{Address: acc}
		funder.RegisterAsset(*assets[i].(*ethchannel.Asset), dep, ethAcc)
		// We have to register the asset of the other chain too, but use a
		// NoOpDepositor there since this funder can ignore it.
		funder.RegisterAsset(*assets[1-i].(*ethchannel.Asset), ethchannel.NewNoOpDepositor(), ethAcc)

		// assetID is the ID of the asset on the chain, which is used to
		assetID := ethchannel.MakeLedgerBackendID(chain.ChainID.Int)

		// Register the funder on the multi-funder.
		multiFunder.RegisterFunder(assetID, funder)

		// Setup adjudicator.
		adj := ethchannel.NewAdjudicator(cb, chain.Adjudicator, acc, ethAcc, 10000000)
		// Register the adjudicator on the multi-adjudicator.
		multiAdjudicator.RegisterAdjudicator(assetID, adj)
		adjs[i] = adj
	}

	// Setup dispute watcher.
	watcher, err := local.NewWatcher(multiAdjudicator)
	if err != nil {
		return nil, fmt.Errorf("intializing watcher: %w", err)
	}

	// Setup Perun client.
	walletAddr := ethwallet.AsWalletAddr(acc)
	addresses := map[wallet.BackendID]wire.Address{1: waddress}
	ethWallet := map[wallet.BackendID]wallet.Wallet{1: w}
	perunClient, err := client.New(addresses, bus, multiFunder, multiAdjudicator, ethWallet, watcher)
	if err != nil {
		return nil, errors.WithMessage(err, "creating client")
	}

	// Setup coordinator notifier. Guard the typed nil so the Perun client does
	// not store a non-nil interface wrapping a nil concrete pointer (which
	// crashes when Watch later calls NotifyWatchLedgerChannel on a coordinated
	// channel).
	if coordinatorNotifier != nil {
		perunClient.EnableCoordinationNotifier(coordinatorNotifier)
	}

	// Setup Accounts
	account := map[wallet.BackendID]wallet.Address{1: walletAddr}

	// Unlock the wallet account so callers can sign off-chain states directly
	// (used by attack flows to fabricate v2 with both participants' signatures).
	walletAcc, err := w.Unlock(walletAddr)
	if err != nil {
		return nil, fmt.Errorf("unlocking wallet account: %w", err)
	}

	// Translate the optional coordinator address into the map form expected by
	// `client.WithCoordinator`. A zero address disables coordination.
	var coordMap map[wallet.BackendID]wallet.Address
	if coordinator != (common.Address{}) {
		coordMap = map[wallet.BackendID]wallet.Address{1: ethwallet.AsWalletAddr(coordinator)}
	}

	// Create client and start request handler.
	c := &SwapClient{
		perunClient:  perunClient,
		account:      account,
		walletAcc:    walletAcc,
		currencies:   assets,
		channels:     make(chan *SwapChannel, 10), // cap 10: Hub accepts 2 parent + 1 virtual proposals in quick succession.
		waddresss:    addresses,
		adjudicators: adjs,
		coordinator:  coordMap,
		channelsByID: make(map[channel.ID]*client.Channel),
		watching:     make(map[channel.ID]struct{}),
		acceptAll:    acceptAll,
		autoWatch:    autoWatch,
		challengeDur: 10, // default; callers may override via SetChallengeDuration before OpenChannel.
	}

	// Auto-watch virtual / sub-channels the moment the Perun client adds them to
	// its registry. The proposer and acceptor of a virtual channel already start
	// a watcher on their own handle, but an intermediary Hub receives its proxy
	// view through the internal persistVirtualChannel path (no explicit handle),
	// so without this hook the Hub never watches the virtual sub-channel. When a
	// multi-ledger parent is later re-registered for cross-ledger sync, the
	// watcher's retrieveLatestSubStates then cannot supply the sub-channel state
	// and a nil-Params SignedState reaches the eth adjudicator (panic in
	// ToEthParams). Ledger channels are skipped here — they are watched by the
	// explicit OpenChannel / HandleProposal paths, preserving their exact timing
	// for the honest / attack / defended demos. startWatching dedups so the
	// proposer's / acceptor's own virtual watch is not started twice.
	perunClient.OnNewChannel(func(ch *client.Channel) {
		if c.autoWatch && !ch.IsLedgerChannel() {
			c.startWatching(ch)
		}
	})

	go perunClient.Handle(c, c)

	return c, nil
}

// trackChannel records ch under its channel ID for later lookup via Channel().
// Safe to call concurrently from OpenChannel, OpenVirtualChannel, and HandleProposal.
func (c *SwapClient) trackChannel(ch *client.Channel) {
	c.channelsByIDM.Lock()
	defer c.channelsByIDM.Unlock()
	c.channelsByID[ch.ID()] = ch
	c.lastChannel = ch
}

// Channel returns the previously opened or accepted Perun channel with the
// given ID, or nil if no such channel is tracked. Used by 3-party virtual
// channel flows where Hub holds multiple parent channels simultaneously.
func (c *SwapClient) Channel(id channel.ID) *client.Channel {
	c.channelsByIDM.Lock()
	defer c.channelsByIDM.Unlock()
	return c.channelsByID[id]
}

// OpenChannel opens a new channel with the specified peer and funding.
func (c *SwapClient) OpenChannel(peer map[wallet.BackendID]wire.Address, balances channel.Balances) *SwapChannel {
	// We define the channel participants. The proposer has always index 0. Here
	// we use the on-chain addresses as off-chain addresses, but we could also
	// use different ones.
	participants := []map[wallet.BackendID]wire.Address{c.waddresss, peer}

	// We create an initial allocation which defines the starting balances.
	initAlloc := channel.NewAllocation(2, []wallet.BackendID{1, 1}, c.currencies[0], c.currencies[1])
	initAlloc.Balances = balances

	// Prepare the channel proposal by defining the channel parameters.
	challengeDuration := c.challengeDur
	var opts []client.ProposalOpts
	if c.coordinator != nil {
		opts = append(opts, client.WithCoordinator(c.coordinator))
	}
	proposal, err := client.NewLedgerChannelProposal(
		challengeDuration,
		c.account,
		initAlloc,
		participants,
		opts...,
	)
	if err != nil {
		panic(err)
	}

	// Send the proposal.
	ch, err := c.perunClient.ProposeChannel(context.TODO(), proposal)
	if err != nil {
		panic(err)
	}
	c.trackChannel(ch)

	// Start the on-chain event watcher. It automatically handles disputes.
	if c.autoWatch {
		c.startWatching(ch)
	}

	return newSwapChannel(ch, c.currencies)
}

// OpenVirtualChannel proposes a virtual channel routed through one or more
// parent ledger channels. The peer's wire address, initial balances, the
// channel IDs of the parents, and the per-peer indexMaps must be supplied.
// indexMaps[i] maps virtual-channel indices to the parent-channel indices in
// peer i's view (see go-perun docs for the exact semantics).
//
// This is the proposer side; the responder side is handled in
// HandleProposal via a VirtualChannelProposalMsg switch when acceptAll=true.
func (c *SwapClient) OpenVirtualChannel(
	peer map[wallet.BackendID]wire.Address,
	balances channel.Balances,
	parents []channel.ID,
	indexMaps [][]channel.Index,
) *SwapChannel {
	participants := []map[wallet.BackendID]wire.Address{c.waddresss, peer}

	initAlloc := channel.NewAllocation(2, []wallet.BackendID{1, 1}, c.currencies[0], c.currencies[1])
	initAlloc.Balances = balances

	// Carry the coordinator into the virtual channel's params when one is
	// configured. The eth Adjudicator's coordinateSingle path requires every
	// channel it settles — including the virtual sub-channel — to have a
	// coordinator (MultiLedger.canEnterCoordinated checks params.coordinator);
	// without it the on-chain coordinated dispute reverts "incorrect phase".
	// The optimistic demo leaves c.coordinator nil, so its virtual stays
	// coordinator-free and follows the plain dispute path.
	var opts []client.ProposalOpts
	if c.coordinator != nil {
		opts = append(opts, client.WithCoordinator(c.coordinator))
	}
	proposal, err := client.NewVirtualChannelProposal(
		c.challengeDur,
		c.account,
		initAlloc,
		participants,
		parents,
		indexMaps,
		opts...,
	)
	if err != nil {
		panic(err)
	}

	ch, err := c.perunClient.ProposeChannel(context.TODO(), proposal)
	if err != nil {
		panic(err)
	}
	c.trackChannel(ch)

	if c.autoWatch {
		c.startWatching(ch)
	}

	return newSwapChannel(ch, c.currencies)
}

// Adjudicator returns the per-chain Ethereum adjudicator backing this client.
// chainIdx must be 0 (chain A) or 1 (chain B).
func (c *SwapClient) Adjudicator(chainIdx int) *ethchannel.Adjudicator {
	return c.adjudicators[chainIdx]
}

// WalletAccount returns the unlocked wallet account used for off-chain state
// signing. Attack flows use this to sign fabricated states.
func (c *SwapClient) WalletAccount() wallet.Account {
	return c.walletAcc
}

// Asset returns the multi-ledger asset backing chainIdx (0 = chain A, 1 = B).
func (c *SwapClient) Asset(chainIdx int) channel.Asset {
	return c.currencies[chainIdx]
}

// LastChannel returns the most recently opened or accepted Perun channel.
// Used by attack flows to retrieve the channel state for fabricating updates.
func (c *SwapClient) LastChannel() *client.Channel {
	return c.lastChannel
}

// SetChallengeDuration overrides the on-chain challenge duration (seconds)
// applied to every channel this client opens. Must be called before OpenChannel.
func (c *SwapClient) SetChallengeDuration(seconds uint64) {
	c.challengeDur = seconds
}

// startWatching starts the dispute watcher for the specified channel. It is
// idempotent: a channel watched once (whether via an explicit Open/Accept path
// or the OnNewChannel hook) is never handed to a second watcher goroutine.
func (c *SwapClient) startWatching(ch *client.Channel) {
	c.watchingM.Lock()
	if _, ok := c.watching[ch.ID()]; ok {
		c.watchingM.Unlock()
		return
	}
	c.watching[ch.ID()] = struct{}{}
	c.watchingM.Unlock()

	go func() {
		err := ch.Watch(c)
		if err != nil {
			fmt.Printf("Watcher returned with error: %v", err)
		}
	}()
}

// AcceptedChannel returns the next accepted channel.
func (c *SwapClient) AcceptedChannel() *SwapChannel {
	return <-c.channels
}

// Shutdown gracefully shuts down the client.
func (c *SwapClient) Shutdown() {
	c.perunClient.Close()
}
