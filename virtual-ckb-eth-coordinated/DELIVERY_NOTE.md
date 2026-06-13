# DELIVERY NOTE — Sc4 evaluation harness fixes (D1 + D2)

Module: `virtual-ckb-eth-coordinated/` (thesis Scenario 4 — coordinator active,
virtual sub-channel). Branch: `claude/sc4-neutrality-conclude-metrics-9415mz`.

Two examiner defects were addressed; the third (fault-tolerance / partial
delivery) was explicitly out of scope and left untouched. **All changes are in
`defended.go`.** `main.go` and `metrics.go` were **not** changed — see "Why no
schema change" below.

---

## D1 — Neutrality is now testable: the Alice–Ingrid parent leg is closed

### Before
`runDefended` discarded the Alice–Ingrid leg (`_ = alice; _ = chAI; _ = chIA`)
and settled only the Bob–Ingrid parent + the virtual sub-channel. The
post-balance snapshot (`main.go`, `recordBalances(m, "post", …)`) therefore ran
with the Alice–Ingrid parent still **open**: the intermediary's funded
allocation there, plus the virtual share locked from Alice's side, stayed
trapped inside a live channel. I's measured wallet delta was non-zero *by
construction* (the examiner reports ≈ −5 ETH / −155 CKB), so no run could ever
falsify a neutrality violation in a leg the harness left open.

### After
A new **Phase 4** closes the Alice–Ingrid leg after the contested settlement
completes, so at the post-snapshot *every* channel I participates in (Bob–Ingrid
parent, Alice–Ingrid parent, and the virtual locked inside both) is settled.

### How it is closed, and why not the plain high-level `Settle`
I first tried the obvious "reuse the existing high-level close"
(`PaymentChannel.Settle` / `client.Channel.Settle`). It does **not** work here,
for two independent reasons that are worth recording:

1. **`PaymentChannel.Settle` finalises off-chain first.** It calls `Finalize()`
   (a cooperative `IsFinal=true` update) before settling. A parent that still
   has a **locked virtual sub-channel** cannot be made final, so that update is
   rejected. (The cooperative sibling `multiledger-virtual-ckb-eth` only gets
   away with `chAI.Settle(false)` because it cooperatively closes the *virtual*
   first, releasing both parents' locks — impossible here, since the attacker
   Bob will not co-sign a cooperative virtual close after disputing it.)

2. **The Alice–Ingrid parent carries the coordinator**, so
   `channel.IsCoordinated(params.Coordinator)` is true and go-perun's
   `Channel.Settle` routes through `ensureCoordinated`, which **blocks on a
   `CoordinatedEvent`** that nobody emits unless the coordinator actually
   coordinates that channel.

So the close reuses the same **high-level multi-backend primitive Phase 2 uses**
— `multiCoord.Coordinate` — to pin vc1 on the (still-locked) Alice–Ingrid parent
across both ledgers, then withdraws. For the withdrawal step I deliberately use
the backend adjudicator with an **explicit vc1 state-map** (`smap1`), mirroring
Phase 3, because the intermediary only ever saw the funding sub-state vc0; her
recursive `Settle` machine cannot *gather* vc1, which is the very reason Phase 3
already hand-drives the withdraw rather than calling the high-level `Settle`.
This is also exactly the shape of the proven cooperative close in the sibling
`multiledger-virtual-coordinated` module (Coordinate the still-locked parents,
then settle).

The flow is kept **serial** — `Coordinate → waitChallenge(CKB) →
waitChallenge(ETH) → withdraw` — identical to Phases 2–3, so it never races the
live Alice/Ingrid watchers for the CKB channel cell. **No new sleeps were
added:** Phase 4 needs no pre-coordinate settle-wait (unlike the 8 s before
Phase 2, which exists to let watchers finish reacting to Bob's stale *dispute*);
there is no prior dispute on the Alice–Ingrid leg, so the watchers only react
*after* Phase 4 registers it, and the existing `waitChallenge` windows absorb
that reaction.

### Expected neutrality delta (post − pre), modulo gas/fees
| account     | before fix (leg left open) | after fix (leg closed) |
|-------------|----------------------------|------------------------|
| ingrid_eth  | ≈ −5 ETH (funded share + locked virtual share trapped) | ≈ 0 (only gas) |
| ingrid_ckb  | ≈ −155 CKB (trapped capacity)                          | ≈ 0 (only fees) |

CKB is cell capacity (shannons) and nets exactly to ~0 for the intermediary;
ETH is native wei and *includes gas*, so I's ETH net is ~0 only modulo the gas
she pays as a withdrawing party (the aggregator's balance table already notes
this caveat).

---

## D2 — Sc4 now emits conclude/withdraw metrics

### Before
Phase 3's four real withdrawals (`bob/ingrid × CkbAdj/EthAdj .Withdraw`) were
called **directly**, bypassing the `m.ckbOp` / `m.ethOp` brackets used for
`register_*` and `coordinate`. Result: the JSONL had no `conclude_ckb` /
`conclude_eth` tx (no gas/cycles), no conclude latency phase, and no folded
conclude version — so the thesis's "both adjudicators seal at version N" claim
and the Table 9.1 conclude-cost row had no backing data.

### After
The four withdrawals are bracketed exactly like `register`/`coordinate`:
- CKB withdrawals (Bob primary, Ingrid secondary) → `m.ckbOp("conclude_ckb", …)`
- ETH withdrawals (Bob primary, Ingrid secondary) → `m.ethOp("conclude_eth", …)`
- the folded version is recorded under both labels via
  `m.version("conclude_ckb", …)` / `m.version("conclude_eth", …)`.

**What was *not* changed:** the *settlement semantics* — same parties, same
`parentReq*`, same `smap1` (vc1), same order. Only the *measurement* was added.
The two same-labelled calls per ledger accumulate latency and collect both txs
under one phase label (the `Timer.record` and meter `Collect` contract already
supports this), so `conclude_ckb`/`conclude_eth` capture the whole conclude path
per ledger. The aggregator's I1 conclude invariant (`conclude_ckb` vs
`conclude_eth`) now has data and should read "conclude: consistent".

Phase 4 (the Alice–Ingrid neutrality close) is **intentionally left unmetered**:
it is the neutrality cleanup, not part of the Sc4 conclude path being measured,
and folding it into `conclude_*` would double-count and conflate two different
costs. Its un-bracketed on-chain txs are buffered by the CKB poller but never
`Collect`-ed, so they do not leak into any phase label.

### Why no `RunRecord` schema change (`metrics.go` untouched)
`conclude_ckb`/`conclude_eth` are ordinary labels: versions land in the existing
`Versions map[string]uint64`, gas/cycles in the existing `ETH`/`CKB` tx slices,
latency in `Phases`. No new field is needed, so `metrics.go` and the schema were
left alone.

### EVAL_OUT-unset path stays dormant
Every metric method short-circuits on `!m.on` (`newMeters` returns `&meters{}`
when `eval.Enabled()` is false). The new `m.ckbOp/m.ethOp/m.version` calls wrap
the *same* withdrawals; when disabled, `ckbOp`/`ethOp` just call `fn()`. A plain
`go run .` behaves exactly as before (Phase 4 runs unconditionally — it is a real
settlement fix, not instrumentation).

---

## Version-mismatch finding (investigated, NOT papered over)

The code names the captured states `vc0` (`ss0`) and `vc1` (`ss1`), suggesting
versions 0 and 1. **That naming is misleading.** `ss0` is snapshotted *before*
two updates and `ss1` *after* them:

```
ss0 = signedStateOf(chBA)                 // funding sub-state, State.Version = 0
chAB.SendEthPayment(virtualEth/2)         // update  -> Version 1
chBA.SendCKBPayment(virtualCkb/2)         // update  -> Version 2
ss1 = signedStateOf(chBA)                 // State.Version = 2
```

(go-perun increments `state.Version++` once per `Update`; the cross-asset swap is
**two** updates.) So:

- `ss1.State.Version == 2`
- the folded conclude versions recorded this run are
  `conclude_ckb == conclude_eth == 2` (and `coordinate == 2`),
- the stale state Bob registers is `register_ckb == register_eth == 0`.

**This is consistent with the thesis prose's "version 2 / σ^vc_2"** — the prose
matches the *actual* sealed version. The discrepancy is purely the code's
`vc0 → vc1` *variable naming*, which undercounts (it is really `v0 → v2`). I have
**not** renamed anything (the rename touches the central narrative of the module
and is the thesis author's call); I added a log line that prints the real
`ss1.State.Version` and a code comment flagging it. Recommendation for the
author: either rename `vc0/vc1` → `vc0/vc2` (or `vcFunding/vcSwap`) or add a note
that "vc1" denotes "the post-swap virtual state, on-chain version 2".

---

## Verification status — HONEST ACCOUNT

**Static (done in this environment):**
- `go build ./...` — passes (toolchain go1.25.10 auto-fetched).
- `go vet .` — clean.
- `alice`, `chAI`, `chIA` are now consumed (the `_ =` discards are gone); the
  compiler confirms no unused params.

**Live (NOT completed in this container — and I will not claim results I did not
observe):** `bench/run.sh virtual-ckb-eth-coordinated defended 5` plus
`bench/aggregate` could not be executed here. Bringing up the CKB devnet requires
tooling that is absent from this ephemeral container (`tmuxp`, `ckb-cli`,
`expect`) **and** a RISC-V cross-build of the Perun CKB contracts (the
`chainckb/contract` submodule ships Rust sources, no prebuilt cells). I
installed `offckb` and initialised the contract submodule, but a reliable
end-to-end bring-up (deploy + fund + 600 s ready-gate + a Hardhat node) was not
achievable in the time/tooling available.

**To verify on a machine with the devnet (per `bench/run.sh` header):**
```bash
# Terminal A — CKB devnet on :8114
cd virtual-ckb-eth-coordinated/chainckb && make dev
# Terminal B — Hardhat on :8545
cd virtual-ckb-eth-coordinated/chaineth && npx hardhat node --port 8545
# Terminal C — 5 instrumented runs, then aggregate
bench/run.sh virtual-ckb-eth-coordinated defended 5 300
go run ./bench/aggregate
# Sanity: plain run with metrics dormant
cd virtual-ckb-eth-coordinated && go run . -mode=defended   # no EVAL_OUT
```

**What to confirm in each JSONL record (`bench/results/..._defended.jsonl`):**
- `eth[]` has `conclude_eth` entries with non-zero `gasUsed`; `ckb[]` has
  `conclude_ckb` entries with non-zero `cycles`.
- `phases` contains `conclude_ckb` and `conclude_eth` latencies.
- `versions` contains `conclude_eth == conclude_ckb == 2` (matching `coordinate`).
- `balances`: `ingrid_eth_post − ingrid_eth_pre ≈ 0` (modulo gas) and
  `ingrid_ckb_post − ingrid_ckb_pre ≈ 0`.

**In `bench/results/tables.md` after `bench/aggregate`:**
- Group A (on-chain cost) now has `conclude_eth` / `conclude_ckb` rows.
- Group C "State versions" lists `conclude_*` and prints
  "I1 (register): … — conclude: consistent".
- Balance net-change shows `ingrid_*` ≈ 0.

---

## Open items / things to watch during the live run
- **Phase 4 watcher coexistence.** The Alice–Ingrid leg is coordinated while
  Alice's and Ingrid's watchers are live. The structure mirrors Phase 2 (which
  already coordinates a channel Ingrid watches), and the `waitChallenge` windows
  absorb the watchers' reaction, but a real run should confirm no watcher race on
  the Alice–Ingrid CKB cell. If one appears, it should be diagnosed (ordering /
  asset-routing) rather than masked with a sleep.
- **Run timeout.** Phase 4 adds one more coordinate + two challenge waits
  (≈ 2×(15+2) s) to the run. The example invocation already uses a 300 s
  per-run timeout, which should be sufficient, but watch for `timeout` FAILs if
  the devnet is slow.
