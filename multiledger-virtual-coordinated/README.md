# multiledger-virtual-coordinated

Demonstrates a **coordinator-assisted multi-ledger virtual channel** between
Alice and Bob routed via an intermediary Hub, against two real Hardhat chains.
Mirrors the upstream reference test
`NhoxxKienn/go-perun/client/test/multiledger_virtual_coordinate.go`
(`TestMultiLedgerVirtualCoordinate`), executed against real chains instead of
`MockBackend`. There is **no attacker** — this is the honest coordinated-dispute
happy path.

## Topology

```
                  Asset1 on chain A             Asset2 on chain B
   Alice ──parent ledger ch (multi-ledger)── Hub ──parent ledger ch (multi-ledger)── Bob
                                  │
                            virtual ch (Alice–Bob)
                            locked as sub-channel
                            inside BOTH parent channels

                         Charlie (trusted coordinator)
                co-signs the dispute resolution on both chains
```

Three `SwapClient` participants (Alice, Bob, Hub) plus an in-process
coordinator (Charlie). Both parent ledger channels **and** the virtual channel
carry Charlie's address in their params.

## Why the virtual channel also needs the coordinator

Unlike the upstream `MockBackend`, the real eth Adjudicator enforces — in
`coordinateSingle` → `MultiLedger.canEnterCoordinated` — that **every** channel
it settles via the coordinated path has a coordinator configured
(`params.coordinator != 0`). The virtual sub-channel is settled as part of each
parent's coordinated resolution, so it must carry the coordinator too; without
it the on-chain `coordinate` call reverts `incorrect phase`. The
`SwapClient.OpenVirtualChannel` helper therefore attaches `WithCoordinator` to
the virtual proposal whenever the client was configured with a coordinator.
(The optimistic demo leaves the coordinator unset, so its virtual stays
coordinator-free and follows the plain dispute path.)

## What it shows

1. Alice–Hub and Bob–Hub parent ledger channels open with Charlie's address in
   their params; the Alice–Bob virtual channel opens locked inside both parents
   (also carrying Charlie).
2. One off-chain virtual update to v1 (`Alice [2,8] / Bob [8,2]`).
3. All four parent channel views (Alice's, Hub-Alice's, Bob's, Hub-Bob's) are
   registered on chain in a random order; each registration includes the
   virtual sub-channel's latest state.
4. Charlie co-signs each parent's canonical resolution **together with** the
   virtual sub-channel's signed state and dispatches across both chains via
   `multi.Coordinator.Coordinate`. `ensureCoordinated` waits for the dispute
   window internally, so no manual timeout wait is needed.
5. All four views settle; the adjudicator distributes the virtual funds per the
   coordinated v1 state. Same final outcome as the optimistic on-chain path —
   nobody cheats.

Wall-clock ~15–20 s.

## Run

```bash
# terminal 1 — chain A (port 8545, chainID 1337, 500 ms blocks)
cd multiledger-virtual-coordinated/chain1 && npm install && npx hardhat node --port 8545

# terminal 2 — chain B (port 8546, chainID 1338, 500 ms blocks)
cd multiledger-virtual-coordinated/chain2 && npm install && npx hardhat node --port 8546

# terminal 3
cd multiledger-virtual-coordinated && go run .
```

The chain dirs symlink their `node_modules` into
`multiledger-channel/chain1/node_modules` to save disk; the first `npm install`
you ran in the honest-demo chain1 satisfies these too. Both chains pre-fund
five accounts: deployer, Alice, Bob, Hub, and Charlie — Charlie needs ETH to
pay gas for the on-chain `Coordinate` transactions.

## Balance figures

- **Initial parent funding** — Alice-Hub `[Alice=[10,10], Hub=[10,10]]`;
  Bob-Hub `[Bob=[10,10], Hub=[10,10]]` (per chain A, chain B).
- **Initial virtual** — `[Alice=[5,5], Bob=[5,5]]`.
- **Virtual update v1** — `[Alice=[2,8], Bob=[8,2]]`. Alice transfers 3 PRN on
  chain A to Bob; Bob transfers 3 PRN on chain B to Alice. Atomic across chains.

**Expected per-address net change after settlement**

| Address | Chain A | Chain B |
| --- | --- | --- |
| Alice   | −3      | +3      |
| Bob     | +3      | −3      |
| Hub     |  0      |  0      |

Hub nets to zero on each chain — the two parent channels balance each other.
Identical to the optimistic on-chain outcome; the coordinator only changes
*how* the dispute resolves, not the final balances.
