#!/usr/bin/env bash
# bench/run.sh — run a scenario N times and append one machine-readable JSON line
# per run to results/<scenario>-<mode>.jsonl, for the Chapter 9 evaluation.
#
# Devnets must already be up (this script does NOT start them):
#   - offckb CKB devnet on :8114  (cd <module>/chainckb && make dev)
#   - one Hardhat node on  :8545  (cd <module>/chaineth && npx hardhat node --port 8545)
# Run ONE scenario at a time; they all contend for :8545 / :8114.
#
# Usage:
#   bench/run.sh <module-dir> <mode> <N> [timeoutSeconds]
# Examples:
#   bench/run.sh multiledger-ckb-eth        attack       30
#   bench/run.sh multiledger-ckb-eth        coordinated  30
#   bench/run.sh multiledger-virtual-ckb-eth attack      30
#   bench/run.sh virtual-ckb-eth-coordinated defended    30 300
#
# Each go run deploys fresh contracts, so runs are independent. Failures are
# logged and skipped (the run produces no JSON line); the loop continues and is
# resumable (results are appended).
set -u

MODULE="${1:?module dir, e.g. multiledger-ckb-eth}"
MODE="${2:?mode, e.g. attack|coordinated|defended|cooperative}"
N="${3:?run count}"
TIMEOUT="${4:-300}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RESULTS_DIR="$REPO_ROOT/bench/results"
mkdir -p "$RESULTS_DIR"

OUT="$RESULTS_DIR/${MODULE//\//_}-${MODE}.jsonl"
LOGDIR="$RESULTS_DIR/logs"
mkdir -p "$LOGDIR"

export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

echo "Scenario : $MODULE -mode=$MODE"
echo "Runs     : $N (timeout ${TIMEOUT}s each)"
echo "Output   : $OUT"
echo

ok=0
fail=0
for i in $(seq 1 "$N"); do
  log="$LOGDIR/${MODULE//\//_}-${MODE}-run${i}.log"
  printf "[%2d/%s] " "$i" "$N"
  if ( cd "$REPO_ROOT/$MODULE" && EVAL_OUT="$OUT" EVAL_RUN="$i" timeout "$TIMEOUT" go run . -mode="$MODE" >"$log" 2>&1 ); then
    ok=$((ok+1)); echo "OK"
  else
    fail=$((fail+1)); echo "FAIL (see $log)"
  fi
done

echo
echo "Done: $ok ok, $fail failed. Records in $OUT ($(wc -l <"$OUT" 2>/dev/null || echo 0) lines)."
