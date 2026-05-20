# perun-multiledger-poc

This repository mimics the spirit of [`perun-examples`](https://github.com/perun-network/perun-examples) and is dedicated to examples, demos, and proof-of-concepts for:

- Perun multiledger payment channels
- Perun multi-ledger virtual channels

## Repository layout

- `examples/multiledger-channel`: demo scenario and implementation notes for a direct multi-ledger channel
- `examples/multiledger-virtual-channel`: demo scenario and implementation notes for a multi-ledger virtual channel

## Goal

Provide a small, practical playground to iterate on multi-ledger channel flows before turning them into complete runnable examples.

## Next steps

1. Implement runnable nodes and a coordinator for the direct multiledger channel demo.
2. Add an intermediary-assisted virtual channel flow across two ledgers.
3. Add scripted demo runs and expected output traces, similar to `perun-examples`.
