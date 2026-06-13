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

	"perun-multiledger-poc/multiledger-ckb-eth/client"
)

// adjReqSnapshot captures a channel's current co-signed state as a standalone
// AdjudicatorReq, cloning the state and signatures so later off-chain updates to
// the live channel do not mutate the snapshot. The snapshot can be registered /
// withdrawn on either ledger's raw adjudicator.
func adjReqSnapshot(ch *client.PaymentChannel) channel.AdjudicatorReq {
	req := pclient.NewTestChannel(ch.GetChannel()).AdjudicatorReq()
	req.Tx.State = req.Tx.State.Clone()
	req.Tx.Sigs = append([]wallet.Sig(nil), req.Tx.Sigs...)
	return req
}

// waitChallenge waits out a dispute/coordinate window in real wall-clock time.
// Both ledgers advance their timers from real block production (CKB blocks; the
// ETH devnet mines every 500 ms), so there is no manual evm_increaseTime.
func waitChallenge(tag string, start time.Time, seconds uint64, ledger string) {
	d := time.Duration(seconds+2) * time.Second
	log.Printf("[%s] [+%5.1fs] waiting %s for the %s challenge window…",
		tag, time.Since(start).Seconds(), d, ledger)
	time.Sleep(d)
}
