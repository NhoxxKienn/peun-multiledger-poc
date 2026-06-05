# multiledger-virtual-ckb-eth

A Perun **multi-ledger virtual channel** spanning **Nervos CKB and Ethereum**,
routed through an intermediary (Ingrid). This is the **un-coordinated** module:
it shows both the honest settlement and the **stale-state-on-virtual attack**
that succeeds when the parents carry no cross-chain coordinator.

The coordinator-defended counterpart — same topology, attack prevented — is
[`virtual-ckb-eth-coordinated/`](../virtual-ckb-eth-coordinated/README.md).

## Topology

```
            ETH (chainID 1337, :8545)   +   CKB devnet (:8114)
   Alice ──parent ch (multi-ledger)── Ingrid
   Bob   ──parent ch (multi-ledger)── Ingrid
                          │
                    virtual ch (Alice–Bob)
              locked as a sub-channel in BOTH parents
```

Both parents span the **same two ledgers** (ETH + CKBytes). Alice and Bob own
the virtual channel; Ingrid is only the intermediary. There is **no
coordinator** — nothing observes the virtual sub-state across ledgers.

## What it shows (`-mode=`)

### `-mode=cooperative` (default)

Alice and Bob open the virtual channel, exchange an off-chain cross-asset update,
and settle safely through the underlying channel topology. Nobody cheats; the
virtual settles at one consistent version.

### `-mode=attack` — stale-state-on-virtual

Bob holds two validly co-signed virtual states: the initial **vc0** and a later
**vc1** (a cross-asset swap). With no coordinator, he disputes the Bob–Ingrid
parent at the **same parent state** on both ledgers but embeds a **different
virtual sub-state on each**:

- **CKB** registers **vc0** (Bob's CKB-favoured share), and
- **ETH** registers **vc1** (Bob's ETH-favoured share).

Each ledger's withdrawal folds the virtual at its locally registered version, so
the virtual channel settles at **vc0 on CKB and vc1 on ETH** — a divergent
settlement. Bob banks his highest share on each chain; Alice, whose only fresh
witness is vc1, is short-changed. Coordinating just the parent state cannot fix
this — see the coordinated module's recursive `CoordinateVC` defence.

Dispute windows are waited out in real wall-clock time on both ledgers (both
devnets auto-mine), so there is no manual `evm_increaseTime`.

## Setup

Install the ETH chain deps and the CKB devnet tool:

```sh
cd chaineth && npm install && cd ..
npm install -g @offckb/cli

# make the CKB devnet scripts executable
cd chainckb
chmod +x ./setup-devnet.sh ./print_accounts.sh ./fund_omni_accounts.sh ./deploy_contracts.sh ./sudt_helper.sh
cd ..

# the CKB Perun contracts live in a submodule
git submodule update --init --recursive
```

## Run

```sh
# terminal 1 — CKB devnet (offckb, RPC :8114); provisions + funds all actors
cd chainckb && make dev

# terminal 2 — Ethereum (port 8545, chainID 1337, 500 ms blocks)
cd chaineth && npx hardhat node --port 8545

# terminal 3 — pick a mode
go run .                 # cooperative (default)
go run . -mode=attack    # stale-state-on-virtual attack
```

## Balance figures

- **Parent funding** — `4 ETH + 100 CKBytes` per side, per parent.
- **Virtual** — `2 ETH + 50 CKBytes`; the off-chain update swaps half of each
  asset across the two parties (Alice pays Bob on ETH, Bob pays Alice on CKB).

In `-mode=attack` the closing banner reads `ATTACK SUCCEEDED — the virtual
channel settled at a different version on each ledger (divergent settlement)`.
