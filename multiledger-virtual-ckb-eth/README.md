# Multiledger Virtual Channel Demo (POC)

This example folder is for a Perun multi-ledger virtual channel proof-of-concept. The virtual channel span two chain Nervos CKB and Ethereum.

## Intended scenario

- Two endpoint participants open a virtual channel through an intermediary.
- The virtual channel is backed by channels that may span multiple ledgers.
- Endpoints exchange off-chain updates and settle safely through the underlying channel topology.

## Deliverables

- Minimal runnable virtual channel demo app
- Scripted end-to-end flow (funding, virtual updates, settlement)

## Setup 
Install dependencies:
```sh
cd chaineth
npm install
```
Install offckb using:
```sh
npm install -g @offckb/cli
```
and make all scripts executable:\
```sh
cd chainckb
chmod +x ./setup-devnet.sh
chmod +x ./print_accounts.sh
chmod +x ./fund_omni_accounts.sh
chmod +x ./deploy_contracts.sh
chmod +x ./sudt_helper.sh
cd ..
```

Initialize the submodule.
```sh
git submodule update --init --recursive
```

## Run the Demo
Start the local CKB devnet:
```sh
cd chainckb
make dev
```

Start the Ethereum local node:
```sh
cd chaineth
npx hardhat node --port 8545
```

Then run
```sh
go run .
```