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
	"log"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
)

// HandleProposal is the callback for incoming channel proposals.
//
// Ledger channel proposals are accepted as long as they are well-formed 2-party
// proposals over the expected assets (both parties may fund both chains).
// Virtual channel proposals are only accepted when acceptAll=true (used by the
// 3-party virtual-channel demos).
func (c *SwapClient) HandleProposal(p client.ChannelProposal, r *client.ProposalResponder) {
	switch lcp := p.(type) {
	case *client.LedgerChannelProposalMsg:
		c.handleLedgerProposal(lcp, r)
	case *client.VirtualChannelProposalMsg:
		if !c.acceptAll {
			r.Reject(context.TODO(), fmt.Sprintf("Virtual proposals require acceptAll=true; got %T", p)) //nolint:errcheck
			return
		}
		c.handleVirtualProposal(lcp, r)
	default:
		r.Reject(context.TODO(), fmt.Sprintf("Unsupported proposal type: %T", p)) //nolint:errcheck
	}
}

func (c *SwapClient) handleLedgerProposal(lcp *client.LedgerChannelProposalMsg, r *client.ProposalResponder) {
	err := func() error {
		if lcp.NumPeers() != 2 {
			return fmt.Errorf("Invalid number of participants: %d", lcp.NumPeers())
		}

		if err := channel.AssertAssetsEqual(lcp.InitBals.Assets, c.currencies[:]); err != nil {
			return fmt.Errorf("Invalid assets: %v", err)
		}

		// Both parties may fund both chains (symmetric funding), which is what
		// lets a cross-chain swap settle on every chain. We accept any well-formed
		// 2-party proposal over the expected assets; go-perun still validates that
		// the funding agreement is consistent with the balances.
		return nil
	}()
	if err != nil {
		r.Reject(context.TODO(), err.Error()) //nolint:errcheck
		return
	}

	accept := lcp.Accept(c.account, client.WithRandomNonce())
	ch, err := r.Accept(context.TODO(), accept)
	if err != nil {
		fmt.Printf("Error accepting ledger channel proposal: %v\n", err)
		return
	}

	c.trackChannel(ch)
	if c.autoWatch {
		c.startWatching(ch)
	}
	c.channels <- newSwapChannel(ch, c.currencies)
}

func (c *SwapClient) handleVirtualProposal(vcp *client.VirtualChannelProposalMsg, r *client.ProposalResponder) {
	if vcp.NumPeers() != 2 {
		r.Reject(context.TODO(), fmt.Sprintf("Virtual proposal: expected 2 peers, got %d", vcp.NumPeers())) //nolint:errcheck
		return
	}

	accept := vcp.Accept(c.account, client.WithRandomNonce())
	ch, err := r.Accept(context.TODO(), accept)
	if err != nil {
		fmt.Printf("Error accepting virtual channel proposal: %v\n", err)
		return
	}

	c.trackChannel(ch)
	if c.autoWatch {
		c.startWatching(ch)
	}
	c.channels <- newSwapChannel(ch, c.currencies)
}

// HandleUpdate is the callback for incoming channel updates.
func (c *SwapClient) HandleUpdate(cur *channel.State, next client.ChannelUpdate, r *client.UpdateResponder) {
	// We accept every update that increases our balance.
	err := func() error {
		err := channel.AssertAssetsEqual(cur.Assets, next.State.Assets)
		if err != nil {
			return fmt.Errorf("Invalid assets: %v", err)
		}

		// Attack and defended scenarios drive non-final balance updates that
		// are neither swaps nor finalizing; skip the swap-pattern check there.
		if c.acceptAll {
			return nil
		}

		// Check the swap of the two currencies.
		for _, currency := range c.currencies {
			myIdx := 1 - next.ActorIdx // This works because we are in a two-party channel.
			// Get our and the peer's current balance.
			ourCurBal := cur.Balance(myIdx, currency)
			peerCurBal := cur.Balance(next.ActorIdx, currency)
			// Get our and the peer's balance of the next proposed state.
			ourNextBal := next.State.Balance(myIdx, currency)
			peerNextBal := next.State.Balance(next.ActorIdx, currency)

			// Check that the balances are "swapped".
			if !(ourCurBal.Cmp(peerNextBal) == 0 && ourNextBal.Cmp(peerCurBal) == 0) {
				return fmt.Errorf("invalid swap of balances")
			}
		}

		if !next.State.IsFinal {
			return fmt.Errorf("swap update must be final")
		}

		return nil
	}()
	if err != nil {
		r.Reject(context.TODO(), err.Error()) //nolint:errcheck // It's OK if rejection fails.
	}

	// Send the acceptance message.
	err = r.Accept(context.TODO())
	if err != nil {
		panic(err)
	}
}

// HandleAdjudicatorEvent is the callback for smart contract events.
func (c *SwapClient) HandleAdjudicatorEvent(e channel.AdjudicatorEvent) {
	log.Printf("Adjudicator event: type = %T, client = %v", e, c.account)
}
