# multiledger-channel

The honest baseline: Alice and Bob atomically swap ERC20 tokens across **two
independent Ethereum chains** inside a single multi-ledger Perun channel. No
attacker, no coordinator.

This module's `client/` package is also the **shared library** that the other
ETH↔ETH scenarios (`multiledger-attack`, `multiledger-defended`,
`multiledger-virtual-optimistic`, `multiledger-virtual-coordinated`) import via a
`replace` directive.

## Topology

```
   chain A (chainID 1337, :8545)   +   chain B (chainID 1338, :8546)
                  Alice ── multi-ledger channel ── Bob
```

A single channel spans both chains; each chain holds its own PerunToken (PRN).

## What it shows

**Symmetric funding** — both parties fund **both** chains, because a multi-ledger
party can only be paid out on a chain it deposited into:

|        | chain A | chain B |
| ------ | ------- | ------- |
| Alice  | 20      | 5       |
| Bob    | 5       | 20      |

`PerformSwap` then flips the two parties' shares per chain and marks the state
final, and a cooperative settle withdraws on both chains:

|        | chain A | chain B |
| ------ | ------- | ------- |
| Alice  | 5       | 20      |
| Bob    | 20      | 5       |

So Alice trades 15 PRN on chain A for 15 PRN on chain B with Bob — atomically.

**Expected wallet balances after the run** (each party starts with 100 PRN on
each chain): chain A `[Alice 85, Bob 115]`, chain B `[Alice 115, Bob 85]`.

> An asymmetric "Alice funds only chain A, Bob only chain B" split would strand
> the swapped funds in the asset holders — each party would be owed funds on a
> chain it never deposited into, and the multi-ledger withdraw can't release
> those. Symmetric funding is what makes the swap settle.

## Run

```sh
# terminal 1 — chain A (port 8545, chainID 1337)
cd multiledger-channel/chain1 && npm install && npx hardhat node --port 8545

# terminal 2 — chain B (port 8546, chainID 1338)
cd multiledger-channel/chain2 && npm install && npx hardhat node --port 8546

# terminal 3
cd multiledger-channel && go run .
```

The contracts (PerunToken / Adjudicator / AssetHolderERC20) are deployed fresh on
both chains at startup, so the nodes need no prior setup.

### Using Docker

```sh
docker build -t hardhat-chain1 -f chain1/Dockerfile chain1
docker build -t hardhat-chain2 -f chain2/Dockerfile chain2
docker run -d --name chain1 -p 8545:8545 hardhat-chain1
docker run -d --name chain2 -p 8546:8545 hardhat-chain2
# then: go run .
# stop: docker stop chain1 chain2 && docker rm chain1 chain2
```
