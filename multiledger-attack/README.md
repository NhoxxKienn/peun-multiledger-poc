# multiledger-attack

Standalone proof-of-concept of the **divergent-settlement attack** described in
`MULTILEDGER_ATTACK_POC.md` §2 and the upstream `TestMultiLedgerAttackNoCoordinator`
reference test in `NhoxxKienn/go-perun/client/test/multiledger_attack_coordinate.go`.

This scenario ships its own devnet (`chain1/`, `chain2/`) deliberately
configured with **asymmetric block production**:

- **chain1** (chain A, port 8545, chainID 1337) — 5 s auto-mine interval
  ("slow L1"). Bob commits the secret v2 here.
- **chain2** (chain B, port 8546, chainID 1338) — 500 ms auto-mine interval
  ("fast L2"). Bob commits the honest v1 here.

The skew is what makes the attack realistic. **Alice runs an honest watcher**
(`autoWatch=true` in `main.go`). When Bob registers v1 on chain B, Alice's
watcher replicates v1 to chain A almost immediately — but Alice's replication
tx then has to wait up to one full chain-A block (5 s) to be included. That
gap anchors chain A's dispute timer ~5 s later than chain B's, so by the time
chain B's dispute window has closed and Bob submits v2 to chain A, chain A's
window is still open. The on-chain challenge duration is **22 s** to leave a
clean ≥2 s margin past the next chain-A block boundary.

Without this skew (both chains at the same cadence), the chain-A dispute
window closes within fractions of a second of chain B's, and Bob's v2 register
hits "refutation timeout passed". The thesis (see `MULTILEDGER_ATTACK_POC.md`
§3) calls this out explicitly: the safe behaviour depends on the watcher
replicating within the reaction budget, and only succeeds when the two chains
are roughly in lockstep. Real cross-chain deployments are not in lockstep, and
this PoC simulates that.

## What it shows

1. Alice and Bob open a multi-ledger Perun channel **without a coordinator**.
   Alice's watcher is **on** (the realistic, defending-side configuration).
2. They legitimately agree on v1 (Alice [5, 3] / Bob [5, 7]).
3. Bob fabricates a secret v2 (Alice [1, 5] / Bob [9, 5]) using Alice's
   extracted signature — Alice's client never sees it.
4. Bob registers v1 on chain B (fast). Alice's watcher fires and submits v1
   to chain A — confirmed only on the next 5 s chain-A block.
5. Bob waits for chain B's 22 s dispute window to close.
6. Bob registers v2 on chain A. Because chain A's timer was anchored ~5 s
   later than chain B's, v2 lands inside chain A's still-open dispute window
   and refutes the replicated v1 (v2 > v1).
7. Both windows elapse; everyone withdraws. Each chain pays out its locally
   registered state — chain A at v2, chain B at v1 — leaving Bob with 16 PRN
   instead of the honest 12 PRN.

## Run

```bash
# terminal 1 — chain A (port 8545, chainID 1337)
cd multiledger-attack/chain1 && npm install && npx hardhat node --port 8545

# terminal 2 — chain B (port 8546, chainID 1338)
cd multiledger-attack/chain2 && npm install && npx hardhat node --port 8546

# terminal 3 — the attack
cd multiledger-attack && go run .
```

Wall-clock duration: ~35 seconds. The 22 s challenge duration is measured in
EVM `block.timestamp` units, which advance faster than wall-clock seconds on
the 500 ms chain B (~+1 per block, ~2× wall) and slightly faster than wall on
the 5 s chain A. The two effective dispute windows therefore complete in
considerably less than 22 + 22 wall-seconds.

## Expected output (final block)

```
============================================================
  DIVERGENT-SETTLEMENT ATTACK — OUTCOME
============================================================
  Honest baseline (both chains at v1):  Alice 8 PRN, Bob 12 PRN
  Observed (chain A at v2, chain B at v1):
    Chain A → Alice 1 PRN, Bob 9 PRN
    Chain B → Alice 3 PRN, Bob 7 PRN
    Bob total: 16 PRN  (honest baseline: 12 PRN)
  Result: ATTACK SUCCEEDED  (Bob +4, Alice -4)
============================================================
```
