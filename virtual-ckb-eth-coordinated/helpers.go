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
	"log"
	"time"

	"perun.network/go-perun/channel"
	pclient "perun.network/go-perun/client"
	"perun.network/go-perun/wallet"

	"perun-multiledger-poc/multiledger-virtual-ckb-eth-coordinated/client"
)

// signedStateOf snapshots a channel's current co-signed state, cloning so later
// updates do not mutate the snapshot.
func signedStateOf(ch *client.PaymentChannel) channel.SignedState {
	req := pclient.NewTestChannel(ch.GetChannel()).AdjudicatorReq()
	return channel.SignedState{
		Params: req.Params,
		State:  req.Tx.State.Clone(),
		Sigs:   append([]wallet.Sig(nil), req.Tx.Sigs...),
	}
}

// waitChallenge waits out a dispute/coordinate window in real wall-clock time.
// Both ledgers advance their timers from real block production (CKB blocks; the
// ETH devnet mines every 500 ms), so there is no manual evm_increaseTime.
func waitChallenge(start time.Time, seconds uint64, ledger string) {
	d := time.Duration(seconds+2) * time.Second
	log.Printf("[defended] [+%5.1fs] waiting %s for the %s challenge window…",
		time.Since(start).Seconds(), d, ledger)
	time.Sleep(d)
}
