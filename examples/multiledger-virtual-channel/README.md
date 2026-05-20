# Multiledger Virtual Channel Demo (POC)

This example folder is for a Perun multi-ledger virtual channel proof-of-concept.

## Intended scenario

- Two endpoint participants open a virtual channel through an intermediary.
- The virtual channel is backed by channels that may span multiple ledgers.
- Endpoints exchange off-chain updates and settle safely through the underlying channel topology.

## Planned deliverables

- Minimal runnable virtual channel demo app
- Scripted end-to-end flow (funding, virtual updates, settlement)
- Notes on dispute handling and settlement guarantees
