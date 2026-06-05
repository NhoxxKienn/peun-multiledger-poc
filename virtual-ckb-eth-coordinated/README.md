# virtual-ckb-eth-coordinated

The coordinator-defended counterpart of
[`multiledger-virtual-ckb-eth/`](../multiledger-virtual-ckb-eth/README.md).
Same CKB↔ETH multi-ledger virtual-channel topology, but the two parent channels
carry a trusted cross-chain coordinator (Charlie). It demonstrates the PoC's
central thesis: **a parent-channel watchtower cannot defend a virtual
sub-channel — only recursive coordination can.**

Bob mounts the same stale-state-on-virtual attack as the un-coordinated module,
**with the honest parties' watchtowers ON**, and it is prevented.

## Topology

```
            ETH (chainID 1337, :8545)   +   CKB devnet (:8114)
   Alice ──parent ch (multi-ledger, coordinated)── Ingrid
   Bob   ──parent ch (multi-ledger, coordinated)── Ingrid
                          │
                    virtual ch (Alice–Bob)
              locked as a sub-channel in BOTH parents

                 Charlie (trusted coordinator)
        in each PARENT's params; co-signs the resolution
        of the parent AND the virtual sub-state on both ledgers
```

Both parents span the **same two ledgers** (ETH + CKBytes). Alice and Bob own
the virtual channel; Ingrid is only the intermediary — she co-signs the
virtual's *funding* sub-state (vc0) at open but never the later updates (vc1).
Charlie is embedded in each parent's params, never in the virtual's.

## What it shows (the defended flow)

1. **Attack trigger.** The virtual is advanced off-chain to vc1 (a cross-asset
   swap). Bob then registers the **stale vc0** on the Bob–Ingrid parent on
   **both** ledgers — the same divergence that succeeds in the un-coordinated
   module.
2. **Honest watchtowers react, and fail gracefully.** Alice and Ingrid run live
   watchtowers. But Ingrid never held vc1, so her watcher has no newer virtual
   sub-state to refute Bob's vc0 with — the contract rejects its refutation
   (`"subChannels too short"`). The parent-layer watchtower is structurally
   insufficient.
3. **Recursive coordination (the fix).** The coordinator runs `CoordinateVC`,
   co-signing the parent **and** the canonical virtual sub-state vc1, fanning the
   same vc1 to **both** ledgers via `multi.Coordinator.Coordinate`. This pins one
   global-canonical virtual version over Bob's stale vc0.
4. **Uniform withdraw.** Both ledgers fold the same vc1, so the virtual channel
   settles identically on CKB and ETH. **Attack prevented.**

Only the attacker (Bob) runs without a watcher — his own watcher would refute
his own stale state. The flow is serial (trigger → wait → coordinate → wait →
withdraw) so the demo never races a live watcher for the CKB channel cell.

## Why recursive coordination, not just a watchtower

A watchtower is **liveness-bounded**: to refute a stale registration it must
detect and act within each chain's challenge window — a large cross-chain
finality gap can defeat it. Recursive coordination has **no timing assumption**:
the coordinator co-signs one canonical virtual sub-state for every ledger, so
both settle uniformly regardless of window skew. And because the adjudicator
forces any `coordinate` of a VC-locking parent to also carry the virtual
sub-state (and a coordinator signature per sub-channel), a coordinated parent
**forces its virtual sub-channel into coordination by construction** — there is
no "coordinate the parent only" move. See
[`MULTILEDGER_ATTACK_POC.md` §11.7](../MULTILEDGER_ATTACK_POC.md).

## Run

```bash
# terminal 1 — CKB devnet (offckb, RPC :8114); provisions + funds all actors
cd virtual-ckb-eth-coordinated/chainckb && make dev

# terminal 2 — Ethereum (port 8545, chainID 1337, 500 ms blocks)
cd virtual-ckb-eth-coordinated/chaineth && npm install && npx hardhat node --port 8545

# terminal 3
cd virtual-ckb-eth-coordinated && go run .
```

`chainckb/` is a symlink to the un-coordinated module's CKB devnet, so a single
`make dev` provisions the shared accounts and Perun script cells. The coordinator
**Charlie** has his own key (`chainckb/accounts/charlie.pk`) and is funded by the
devnet's `fund_omni_accounts.sh`; he also holds native ETH on the Hardhat chain
(the 5th pre-funded account in `chaineth/hardhat.config.js`) to pay gas for the
on-chain coordinate transactions. The ETH contracts (Adjudicator + asset holder)
are deployed fresh at startup.

## Expected outcome

Both ledgers fold the virtual channel at **vc1** (the cross-asset swap), so the
virtual settles uniformly — Alice receives her swapped balance on both chains
instead of being short-changed. The closing banner reads
`ATTACK PREVENTED — uniform settlement on both ledgers`.

Compare with [`multiledger-virtual-ckb-eth/`](../multiledger-virtual-ckb-eth/README.md),
where the same attack on un-coordinated parents settles divergently (CKB folds
vc0, ETH folds vc1) and succeeds.
