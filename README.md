# perun-multiledger-poc

This repository mimics the spirit of [`perun-examples`](https://github.com/perun-network/perun-examples) and is dedicated to examples, demos, and proof-of-concepts for:

- Perun multiledger payment channels
- Perun multi-ledger virtual channels

## Repository layout

Each scenario ships its own self-contained devnet (`chain1/`, `chain2/` next
to its `go.mod`) so its Hardhat configuration matches its intent:

- **`multiledger-channel/`** uses 5 s + 5 s — the honest baseline: an atomic
  cross-chain swap (symmetric funding) over a single multi-ledger channel. Its
  `client/` package is the shared library the other ETH↔ETH scenarios import.
- **`multiledger-attack/`** uses **5 s (chain A) + 500 ms (chain B)** —
  deliberately asymmetric. The skew is what enables the divergent-settlement
  attack to land even when the honest victim runs a watcher; see that
  module's README for the timing analysis.
- **`multiledger-defended/`** uses 500 ms + 500 ms (symmetric, fast) and a
  short challenge duration so the protected flow completes in ~15 seconds.
  The coordinator's protection logic is independent of block-time skew, so
  fast/symmetric is just for demo ergonomics.
- **`multiledger-virtual-optimistic/`** uses 500 ms + 500 ms (symmetric, fast)
  — a multi-ledger virtual channel (Alice–Bob) routed through an intermediary
  Hub, settled cooperatively, via on-chain dispute, or under the
  stale-state-on-virtual attack (`-mode=`).
- **`multiledger-virtual-coordinated/`** uses 500 ms + 500 ms (symmetric, fast)
  — the same virtual-channel topology, but the parents and the virtual carry a
  trusted coordinator that co-signs the dispute resolution across both chains.

The two **CKB↔ETH** virtual modules instead pair one Hardhat ETH chain
(`chaineth/`, port 8545) with an offckb CKB devnet (`chainckb/`, RPC 8114):

- **`multiledger-virtual-ckb-eth/`** — the un-coordinated cross-chain virtual
  channel: honest settlement and the stale-state-on-virtual attack (`-mode=`),
  with the virtual settling divergently (vc0 on CKB, vc1 on ETH).
- **`virtual-ckb-eth-coordinated/`** — the coordinator-defended counterpart:
  the same attack is mounted with honest watchtowers on and **prevented** by the
  recursive `CoordinateVC`, which pins one virtual version on both ledgers.

Per-scenario devnets also mean you can run each scenario from a fresh chain
state without restarting nodes between unrelated demos.

| Directory                                                                       | What it shows                                                                                       | Coordinator?                                                                  | Run time |
| ------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | -------- |
| [`multiledger-channel/`](multiledger-channel/README.md)                         | Honest atomic cross-chain swap (symmetric funding)                                                  | No                                                                            | ~5 s     |
| [`multiledger-attack/`](multiledger-attack/README.md)                           | Divergent-settlement attack succeeds against an honest watcher                                      | No                                                                            | ~35 s    |
| [`multiledger-defended/`](multiledger-defended/README.md)                       | Coordinator prevents divergence                                                                     | Yes (in-process via `cross-chain-coordinator/backends.SetupMultiCoordinator`) | ~10–20 s |
| [`multiledger-virtual-optimistic/`](multiledger-virtual-optimistic/README.md)   | Virtual channel via Hub: cooperative, on-chain dispute, or stale-state-on-virtual attack (`-mode=`) | No                                                                            | ~10–15 s |
| [`multiledger-virtual-coordinated/`](multiledger-virtual-coordinated/README.md) | Virtual channel via Hub: coordinator co-signs the cross-chain dispute                               | Yes (in-process via `cross-chain-coordinator/backends.SetupMultiCoordinator`) | ~15–20 s |
| [`multiledger-virtual-ckb-eth/`](multiledger-virtual-ckb-eth/README.md)         | CKB↔ETH virtual channel: honest or stale-state-on-virtual attack (`-mode=`); settles divergently    | No                                                                            | ~30–40 s |
| [`virtual-ckb-eth-coordinated/`](virtual-ckb-eth-coordinated/README.md)         | CKB↔ETH virtual channel: attack prevented by recursive `CoordinateVC` (watchtowers on)              | Yes (in-process via `cross-chain-coordinator/backends.SetupMultiCoordinator`) | ~40–60 s |
| [`multiledger-ckb-eth/`](multiledger-ckb-eth/README.md)                         | CKB↔ETH **payment** (parent) channel: cooperative, divergent-settlement attack, or coordinator defence (`-mode=`) | Yes, in `coordinated` mode (in-process via `cross-chain-coordinator/backends.SetupMultiCoordinator`) | ~30–50 s |

The attack model and coordinator design are documented in
[`MULTILEDGER_ATTACK_POC.md`](MULTILEDGER_ATTACK_POC.md).

## Evaluation harness

`eval/` is a stdlib-only metrics module (raw JSON-RPC, no go-perun imports) that each scenario can
import via `replace perun-multiledger-poc/eval => ../eval`; it records per-phase ETH gas / CKB
cycles / latency / state versions / balances to one JSONL line per run when `EVAL_OUT` is set
(`go run .` is unaffected otherwise). `bench/run.sh <module-dir> <mode> <N>` drives N runs of a
scenario and appends to `bench/results/<module>-<mode>.jsonl`; `bench/aggregate` reads those files
and emits `bench/results/tables.md` with median/min/max/std-dev tables (Groups A–D) for the
Chapter 9 evaluation — see `chapter9_data_handover.md`.

## Quick start

Start a scenario's own pair of Hardhat nodes, then run its Go entry point.
The chains do not need to stay up between scenarios — each scenario deploys
fresh PerunToken / Adjudicator / AssetHolder contracts at startup.

```bash
# Pick ONE scenario at a time. Example: the attack PoC.
cd multiledger-attack/chain1 && npm install && npx hardhat node --port 8545   # terminal 1
cd multiledger-attack/chain2 && npm install && npx hardhat node --port 8546   # terminal 2
cd multiledger-attack && go run .                                              # terminal 3
```

Repeat with the relevant `chain1/`/`chain2/` directory for `multiledger-channel`
or `multiledger-defended`. No external network access is required — both the
attack and the coordinator-defended demos run entirely against the local
Hardhat nodes.
