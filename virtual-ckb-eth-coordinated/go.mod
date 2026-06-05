module perun-multiledger-poc/multiledger-virtual-ckb-eth-coordinated

go 1.25.7

toolchain go1.25.10

require (
	cross-chain-coordinator v0.0.0-00010101000000-000000000000
	github.com/ethereum/go-ethereum v1.17.2
	github.com/nervosnetwork/ckb-sdk-go/v2 v2.2.0
	github.com/perun-network/perun-eth-backend v0.6.0
	github.com/pkg/errors v0.9.1
	perun.network/go-perun v0.15.1-0.20260408121133-2daea3fa699a
	perun.network/perun-ckb-backend v0.0.0-20260531113006-5aa8111e501a
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Pilatuz/bigz v1.2.1 // indirect
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/bits-and-blooms/bitset v1.20.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/consensys/gnark-crypto v0.18.1 // indirect
	github.com/crate-crypto/go-eth-kzg v1.5.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/deckarep/golang-set/v2 v2.6.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/ethereum/c-kzg-4844/v2 v2.1.6 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/shirou/gopsutil v3.21.11+incompatible // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/supranational/blst v0.3.16 // indirect
	github.com/tklauser/go-sysconf v0.3.13 // indirect
	github.com/tklauser/numcpus v0.7.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.3 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.40.0 // indirect
	go.opentelemetry.io/otel/metric v1.40.0 // indirect
	go.opentelemetry.io/otel/trace v1.40.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	polycry.pt/poly-go v0.0.0-20220301085937-fb9d71b45a37 // indirect
)

replace perun.network/go-perun => github.com/NhoxxKienn/go-perun v0.0.0-20260604144715-46d216960570

replace github.com/perun-network/perun-eth-backend => github.com/Perun-Cross-chain-Virtual-Channel/perun-eth-backend v0.6.1-0.20260603060223-82e88db70699

replace perun.network/perun-ckb-backend => github.com/Perun-Cross-chain-Virtual-Channel/perun-ckb-backend v1.0.1-0.20260603103853-8759dbc4d423

replace github.com/nervosnetwork/ckb-sdk-go/v2 => github.com/perun-network/ckb-sdk-go/v2 v2.2.1-0.20260530044933-548463b5d86f

replace cross-chain-coordinator => github.com/NhoxxKienn/cross-chain-coordinator v0.0.0-20260603100756-a409e48c369a
