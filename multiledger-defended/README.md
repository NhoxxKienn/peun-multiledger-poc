# multiledger-defended

Standalone proof-of-concept that the **cross-chain coordinator** prevents the
divergent-settlement attack demonstrated by `../multiledger-attack/`. Mirrors
the upstream `TestMultiLedgerAttackCoordinate` reference test in
`NhoxxKienn/go-perun/client/test/multiledger_attack_coordinate.go`.

The coordinator is built in-process from
`cross-chain-coordinator/backends.SetupMultiCoordinator`, which returns a
`*multi.Coordinator` wired to per-chain `ethchannel.Coordinator` instances.
After Bob registers v2 and its dispute window closes, `main.go` calls
`multiCoord.Coordinate(...)` explicitly — the direct analogue of
`charlie.Coordinate(ctx, v2ReqBob, nil, bID2)` in the upstream test. We
deliberately skip the libp2p relay path because
`RelayCoordinatorNotifier` JSON-encodes `channel.SignedState` whose
`Params.Parts` field contains the `wallet.Address` interface, which Go's JSON
decoder cannot reconstruct on the receiving end.

This scenario ships its own devnet (`chain1/`, `chain2/`) configured for fast
block production (500 ms auto-mine interval, 10× faster than the attack
devnet) and a short 5 s on-chain challenge duration so the whole demo
completes in roughly 15 seconds of wall clock.

## What it shows

1. Alice and Bob open a multi-ledger Perun channel **with a trusted coordinator
   (Charlie)** — `params.coordinator` is set to Charlie's wallet address.
2. They agree on v1 (Alice [5, 3] / Bob [5, 7]).
3. Bob fabricates v2 with Alice's extracted signature and registers v1 on
   chain B; Bob's watcher replicates v1 to chain A.
4. Bob registers v2 on chain A (still inside chain A's open window).
5. Once chain A's dispute window has closed, the in-process coordinator calls
   `Coordinate(v2)` on both chains. The contract locks both chains to v2.
6. `Settle` → `ensureCoordinated` → `Withdraw` produces the uniform v2 outcome:
   Alice 6 PRN total, Bob 14 PRN total — no divergence.

## Run

```bash
# terminal 1 — chain A (port 8545, chainID 1337, fast mining)
cd multiledger-defended/chain1 && npm install && npx hardhat node --port 8545

# terminal 2 — chain B (port 8546, chainID 1338, fast mining)
cd multiledger-defended/chain2 && npm install && npx hardhat node --port 8546

# terminal 3 — the defended demo
cd multiledger-defended && go run .
```

Wall-clock duration: ~10–20 seconds. No external network access required
(the libp2p relay is bypassed).

The module pins Go 1.25 via the `cross-chain-coordinator` dependency
(`toolchain go1.25.10` directive). Modern Go installs auto-fetch it.

## Expected output (final block)

```
============================================================
  COORDINATOR-DEFENDED OUTCOME
============================================================
  Both chains locked to canonical v2 by the coordinator:
    Chain A → Alice 1 PRN, Bob 9 PRN
    Chain B → Alice 5 PRN, Bob 5 PRN
    Bob total: 14 PRN  (attack divergence: 16 PRN; coordinator blocks +2)
  Result: ATTACK PREVENTED — no divergence between chains.
============================================================
```
