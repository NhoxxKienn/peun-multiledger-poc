# multiledger-ckb-eth

A Perun **multi-ledger payment channel** (no virtual layer, no intermediary)
spanning **Nervos CKB and Ethereum**, directly between two parties — Alice and
Bob. This is the CKB↔ETH realisation of the thesis's **Sc 1 / Sc 2** parent-only
scenarios: the divergent-settlement attack against an honest watcher, and the
cross-chain coordinator that prevents it.

It is the same parent-channel construction the virtual modules
(`multiledger-virtual-ckb-eth/`, `virtual-ckb-eth-coordinated/`) already build
on — just without the intermediary/virtual layer on top.

## Topology

```
            ETH (chainID 1337, :8545)   +   CKB devnet (:8114)
   Alice ──────────── parent channel (multi-ledger) ──────────── Bob
```

Both parties fund **both** ledgers symmetrically (`4 ETH + 100 CKBytes` each),
so a cross-asset swap settles cleanly on either side — see "Symmetric funding
matters" in the repo root `MULTILEDGER_ATTACK_POC.md`.

## What it shows (`-mode=`)

### `-mode=cooperative` (default)

Alice and Bob exchange off-chain cross-asset payments (ETH one way, CKB the
other) and settle cooperatively. Nobody disputes; both ledgers conclude at the
same final version.

### `-mode=attack` — divergent settlement (thesis Sc 1, no coordinator)

Bob holds two validly co-signed channel states at different versions (v1 after
the ETH leg, v2 after the CKB leg). With watchtowers off and no coordinator, he
registers the **stale v1** (CKB-favoured) on CKB and the **fabricated v2**
(ETH-favoured) on ETH, waits out both challenge windows in real wall-clock
time, and withdraws divergently — banking his best share on each chain.
**I1 (consistent registered version) and I2 (consistent settlement) are
violated.** Banner: `ATTACK SUCCEEDED`.

> Registering must start from a non-zero (post-funding) state — the CKB PCTS
> adjudicator only opens a dispute for v ≥ 1; v0 leaves the cell un-disputed
> and force-close fails with error 66 (`StatusNotDisputed`).

### `-mode=coordinated` — coordinator-defended (thesis Sc 2, parent-only)

Same divergent registration as the attack, but the parents carry a
cross-chain coordinator (Charlie). After the divergent registration, the
coordinator's parent-only `Coordinate` co-signs and pins **one canonical
version** on both ledgers; both parties then withdraw uniformly at that
version. **I1, I2, I3 hold.** Banner: `ATTACK PREVENTED`.

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
go run .                    # cooperative (default)
go run . -mode=attack       # divergent-settlement attack — Sc 1
go run . -mode=coordinated  # coordinator-defended — Sc 2
```

## Balance figures

- **Funding** — `4 ETH + 100 CKBytes` per side (symmetric).
- **Off-chain swap** — Alice pays Bob `parentEth/2` ETH, Bob pays Alice
  `parentCkb/2` CKBytes (cross-asset, so the channel ends at version 2).

## Evaluation metrics

When `EVAL_OUT=<path.jsonl>` is set (and optionally `EVAL_RUN=<index>`), each
run appends one JSON record (gas/cycles per phase, latency, registered/settled
versions, pre/post balances, config) for the Chapter 9 evaluation — see
`../eval/`, `../bench/`, and `../chapter9_data_handover.md`. Plain `go run .`
is unaffected.
