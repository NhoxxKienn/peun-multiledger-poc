# multiledger-virtual-optimistic

Demonstrates a **multi-ledger virtual channel** between Alice and Bob routed
via an intermediary Hub, against two real Hardhat chains. No attacker, no
cross-chain coordinator. Selects between two no-attacker flows via a CLI flag.

Mirrors the upstream reference tests
`NhoxxKienn/go-perun/client/test/multiledger_virtual_happy.go` (cooperative)
and `multiledger_virtual_dispute.go` (on-chain), executed against real chains
instead of `MockBackend`.

## Topology

```
                  Asset1 on chain A             Asset2 on chain B
   Alice ──parent ledger ch (multi-ledger)── Hub ──parent ledger ch (multi-ledger)── Bob
                                  │
                            virtual ch (Alice–Bob)
                            locked as sub-channel
                            inside BOTH parent channels
```

Three `SwapClient` participants:
- **Alice** — proposes the Alice-Hub parent and the Alice-Bob virtual.
- **Hub** — accepts both parents; holds the virtual locked-state representation of Bob in Alice-Hub, and of Alice in Bob-Hub.
- **Bob** — proposes the Bob-Hub parent and accepts the Alice-Bob virtual.

Both parents span chain A (chainID 1337, port 8545) and chain B (chainID 1338, port 8546). The virtual channel has no on-chain presence of its own; it is locked as a sub-channel inside both parents.

## What it shows

1. **Cooperative mode** (`-mode=cooperative`)
   - Alice marks the virtual channel `IsFinal=true` off-chain.
   - Alice and Bob settle the virtual cooperatively (no on-chain Register).
   - Hub follows: both parents settle cooperatively.
   - Wall-clock ≤ 10 s; no `RegisteredEvent` in logs.

2. **On-chain mode** (`-mode=onchain`)
   - All four parent channel views (Alice's, Hub-Alice's, Bob's, Hub-Bob's) are registered on chain in random order via `client.NewTestChannel(parent).Register(ctx)`.
   - Each view's dispute window closes; the adjudicator recursively distributes virtual sub-channel funds at settle.
   - Same final outcome as the cooperative path — nobody cheats.
   - Wall-clock ~15 s.

## Run

```bash
# terminal 1 — chain A (port 8545, chainID 1337, 500 ms blocks)
cd multiledger-virtual-optimistic/chain1 && npm install && npx hardhat node --port 8545

# terminal 2 — chain B (port 8546, chainID 1338, 500 ms blocks)
cd multiledger-virtual-optimistic/chain2 && npm install && npx hardhat node --port 8546

# terminal 3 — pick one mode
cd multiledger-virtual-optimistic
go run . -mode=cooperative
go run . -mode=onchain
```

The chain dirs symlink their `node_modules` into `multiledger-channel/chain1/node_modules` to save disk; the first `npm install` you ran in the honest-demo chain1 satisfies these too.

## Balance figures

- **Initial parent funding** — Alice-Hub `[Alice=[10,10], Hub=[10,10]]`; Bob-Hub `[Bob=[10,10], Hub=[10,10]]` (per chain A, chain B).
- **Initial virtual** — `[Alice=[5,5], Bob=[5,5]]`.
- **Virtual update v1** — `[Alice=[2,8], Bob=[8,2]]`. Alice transfers 3 PRN on chain A to Bob; Bob transfers 3 PRN on chain B to Alice. Atomic across chains.

**Expected per-address net change after settlement (both modes)**

| Address | Chain A | Chain B |
| --- | --- | --- |
| Alice   | −3      | +3      |
| Bob     | +3      | −3      |
| Hub     |  0      |  0      |

Hub nets to zero on each chain — the two parent channels balance each other.
