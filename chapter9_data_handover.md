# Chapter 9 — Evaluation Data Handover

**Thesis:** Cross-Chain Virtual Channel Integration in the Perun State Channel Framework
**Prepared for:** PoC Implementation Developer
**Date:** 2026-06-05
**Repository:** [perun-multiledger-poc](https://github.com/Perun-Cross-chain-Virtual-Channel/perun-multiledger-poc)

---

## Purpose

Chapter 9 of the thesis reports three categories of measured data from the PoC prototype:

1. **On-chain transaction cost** — gas (Ethereum) and cycles (CKB) per operation
2. **Settlement lifecycle latency** — per-phase timing, decomposed by phase
3. **Scenario outcomes** — which invariants hold or are violated in each of the four PoC scenarios

Every `% DATA:` comment in [`chapter9_spec_v4.tex`](chapter9_spec_v4.tex) marks a value that must come from a test run.
This document tells you exactly which scenario to run, what to measure, and how to record the result.

---

## Scenarios to Run

The repository contains eight scenario directories. The thesis uses **four** of them,
mapped below to the four proof-of-concept scenarios named in Chapter 8.

> **Substrate correction (2026-06-06):** the glossary fixes ℓ₂ = CKB, so Sc 1/Sc 2 must be
> CKB↔ETH **parent** (payment) channels — not the original ETH↔ETH `multiledger-attack`/
> `multiledger-defended` pair (which has no CKB leg and cannot fill the Group A CKB-cycle rows,
> e.g. A8 `coordinate()`). Both thesis scenarios now run on the **same CKB+ETH substrate** as
> Sc 3/Sc 4, out of the new [`multiledger-ckb-eth/`](https://github.com/Perun-Cross-chain-Virtual-Channel/perun-multiledger-poc/tree/main/multiledger-ckb-eth)
> module's `-mode=attack` / `-mode=coordinated` (a 2-party Alice↔Bob multi-ledger payment
> channel — the same parent-channel construction the virtual scenarios already build on, minus
> the intermediary/virtual layer). The original ETH↔ETH `multiledger-attack`/`multiledger-defended`
> remain in the repo as the simpler same-asset baseline demos but are **not** the thesis Sc 1/Sc 2.

| Thesis scenario | Directory | Coordinator? | What it proves |
|---|---|---|---|
| **Sc 1** — Attack without coordinator (parent) | [`multiledger-ckb-eth/`](https://github.com/Perun-Cross-chain-Virtual-Channel/perun-multiledger-poc/tree/main/multiledger-ckb-eth) `-mode=attack` | No | I1, I2 violated |
| **Sc 2** — Coordinator active (parent only) | [`multiledger-ckb-eth/`](https://github.com/Perun-Cross-chain-Virtual-Channel/perun-multiledger-poc/tree/main/multiledger-ckb-eth) `-mode=coordinated` | Yes | I1, I2, I3 consistent |
| **Sc 3** — VC attack without coordinator | [`multiledger-virtual-ckb-eth/`](https://github.com/Perun-Cross-chain-Virtual-Channel/perun-multiledger-poc/tree/main/multiledger-virtual-ckb-eth) `-mode=attack` | No | I1, I2, I1* violated |
| **Sc 4** — Coordinator active (with virtual sub-channel) | [`virtual-ckb-eth-coordinated/`](https://github.com/Perun-Cross-chain-Virtual-Channel/perun-multiledger-poc/tree/main/virtual-ckb-eth-coordinated) `-mode=defended` | Yes | I1, I2, I3, I1* consistent |

> **Run count:** Execute each scenario **at least 30 times** (`N ≥ 30`) for credible medians.
> Record all raw values so maximum and standard deviation can also be derived.
> All four scenarios are CKB↔ETH and require `offckb` running on port 8114 alongside Hardhat on 8545
> (one devnet pair serves all of them — run one scenario at a time).
> Use `bench/run.sh <module-dir> <mode> <N>` to drive the runs and emit one JSONL record per run
> (set `EVAL_OUT`/`EVAL_RUN`, see `eval/` and `bench/`), then `bench/aggregate` to produce the
> Group A–D tables below automatically.

---

## Data Points Required

### Group A — On-Chain Transaction Cost (§9.1)

Run **Sc 2** (`multiledger-ckb-eth -mode=coordinated`) and **Sc 4** (`virtual-ckb-eth-coordinated`).
For each on-chain transaction, record the gas receipt (Ethereum) or the cycle report (CKB).

| ID | Measurement | Operation | Scenario | Where to find it |
|---|---|---|---|---|
| A1 | Gas cost of `fund()` on ℓ₁ | fund | Sc 2 | Hardhat tx receipt `.gasUsed` |
| A2 | Cycles cost of `fund()` on ℓ₂ | fund | Sc 4 | CKB RPC `get_transaction` — `cycles` field |
| A3 | Gas cost of `register()` (parent) on ℓ₁ | register | Sc 2 | Hardhat tx receipt |
| A4 | Cycles cost of `register()` (parent) on ℓ₂ | register | Sc 4 | CKB RPC |
| A5 | Gas cost of `register()` on V̂_ℓ₁ | register (virtual adjudicator) | Sc 4 | Hardhat tx receipt |
| A6 | Cycles cost of `register()` on V̂_ℓ₂ | register (virtual adjudicator) | Sc 4 | CKB RPC |
| A7 | Gas cost of `coordinate()` — parent only | coordinate | Sc 2 | Hardhat tx receipt — **new operation** |
| A8 | Cycles cost of `coordinate()` — parent only | coordinate | Sc 2 | CKB RPC — **new operation** |
| A9 | Gas cost of `coordinate()` — with virtual sub-channel | coordinateVC | Sc 4 | Hardhat tx receipt — **new operation** |
| A10 | Cycles cost of `coordinate()` — with virtual sub-channel | coordinateVC | Sc 4 | CKB RPC — **new operation** |
| A11 | Gas cost of `conclude()` on ℓ₁ | conclude | Sc 2 | Hardhat tx receipt |
| A12 | Cycles cost of `conclude()` on ℓ₂ | conclude | Sc 4 | CKB RPC |
| A13 | Call-data size delta between A7 and A9 | bytes | — | Derived: tx input size(A9) − tx input size(A7) |
| A14 | CKB lock-script ECDSA check cycle count (standalone) | ecrecover equivalent | Sc 4 | CKB RPC `cycles` on the coordinate tx |

> **Derived values needed for the thesis text (compute from above):**
> - `Y%` = A7 / A3 × 100 (coordinate overhead as % of baseline register)
> - `Z gas` = A9 − A7 (virtual surcharge in gas)
> - `W cycles` = A10 − A8 (virtual surcharge in cycles)
> - CKB block cycle budget `C`: read from `get_consensus` RPC field `max_block_cycles`
> - `V/C %` = A14 / C × 100

---

### Group B — Settlement Lifecycle Latency (§9.2)

Run **Sc 2** (enforced parent closure) and **Sc 4** (enforced virtual closure).
For each run, record timestamps at each phase transition using the test harness clock or log timestamps.

#### B1 — Enforced Closure, Parent Channel (Sc 2)

| ID | Measurement | Unit | Definition |
|---|---|---|---|
| B1a | `register()` confirmation latency δ_ℓ₁ | ms | Time from tx submission to tx mined on ℓ₁ |
| B1b | `register()` confirmation latency δ_ℓ₂ | ms | Time from tx submission to tx mined on ℓ₂ |
| B1c | Challenge window T^ℓ₁_dispute | s | Configured value in Hardhat/contract deployment |
| B1d | Challenge window T^ℓ₂_dispute | s | Configured value in offckb/contract deployment |
| B1e | T_coord observed — median | ms | From moment `ReadyForCoordination(cid)` holds on **both** chains to moment **last** `coordinate()` tx is confirmed |
| B1f | T_coord observed — maximum | ms | Worst-case over all N runs |
| B1g | `coordinate()` confirmation latency δ_ℓ₁ | ms | Same definition as B1a |
| B1h | `coordinate()` confirmation latency δ_ℓ₂ | ms | Same definition as B1b |
| B1i | `conclude()` confirmation latency δ_ℓ₁ | ms | Same definition as B1a |
| B1j | `conclude()` confirmation latency δ_ℓ₂ | ms | Same definition as B1b |
| B1k | Total enforced closure end-to-end | s | From first `register()` submission to last `conclude()` confirmed |

> **Configured bound B:** Read `T_coord` timeout from the coordinator service config.
> Record it alongside B1e and B1f.
> The thesis text will state: "Configured bound = B ms. Observed max = B1f ms."

#### B2 — Additional Latency, Virtual Enforced Closure (Sc 4, deltas over B1)

| ID | Measurement | Unit | Definition |
|---|---|---|---|
| B2a | `register()` on V̂_ℓ₁ confirmation δ | ms | Same definition as B1a, but for the virtual adjudicator contract |
| B2b | `register()` on V̂_ℓ₂ confirmation δ | ms | CKB virtual adjudicator equivalent |
| B2c | T_vc challenge window | s | Configured value in virtual adjudicator deployment |
| B2d | Extended cert issuance overhead | ms | Extra time for `widehat{cert}` generation relative to plain cert (B1e baseline) |
| B2e | Total virtual enforced closure | s | Full end-to-end from first `register()` to last `conclude()` in Sc 4 |
| B2f | T^I_react (intermediary reaction time) | ms | From watcher detection event to `register()` submission by I |
| B2g | Propagation timing inequality check | boolean | Does `T_vc ≥ T^I_react + 2·max(δ_ℓ)` hold for every run? |

---

### Group C — Scenario Outcomes (§9.3)

For each scenario, record the **on-chain state versions** before and after coordination,
and the **post-withdrawal balances** for all parties.

#### C1 — Canonical state version numbers (Sc 2)

| ID | Measurement | Definition |
|---|---|---|
| C1a | Version v₁ registered on Ĉ_ℓ₁ before coordination | Channel state version number from `register()` call data |
| C1b | Version v₂ registered on Ĉ_ℓ₂ before coordination | Same, on ℓ₂ |
| C1c | Version of σ* selected by coordinator | Should equal max(v₁, v₂) — verify |
| C1d | Version under which ℓ₁ enters withdrawal | From `conclude()` call data — must equal C1c |
| C1e | Version under which ℓ₂ enters withdrawal | From `conclude()` call data — must equal C1c |

#### C2 — Virtual canonical state version numbers (Sc 4)

| ID | Measurement | Definition |
|---|---|---|
| C2a | Version u₁ sealed on V̂_ℓ₁ | Virtual adjudicator sealed version on ETH |
| C2b | Version u₂ sealed on V̂_ℓ₂ | Virtual adjudicator sealed version on CKB |
| C2c | Version of σ^vc_max selected | Should equal max(u₁, u₂) — verify |
| C2d | (σ*, σ^vc_max) pair on ℓ₁ withdrawal | From conclude() call data |
| C2e | (σ*, σ^vc_max) pair on ℓ₂ withdrawal | Must match C2d exactly |

#### C3 — Financial neutrality of I (Sc 4)

| ID | Measurement | Definition |
|---|---|---|
| C3a | I's pre-opening balance on ℓ₁ (b₁⁰) | Token balance before `fund()` call |
| C3b | I's pre-opening balance on ℓ₂ (b₂⁰) | Token balance before `fund()` call on CKB |
| C3c | I's post-withdrawal balance on ℓ₁ (b₁) | Token balance after `conclude()` confirmed on ℓ₁ |
| C3d | I's post-withdrawal balance on ℓ₂ (b₂) | Token balance after `conclude()` confirmed on ℓ₂ |
| C3e | Net change: (b₁ − b₁⁰) + (b₂ − b₂⁰) | Must equal 0.  Record for all M VC lifecycle runs. |
| C3f | Number of VC lifecycle runs M | Total runs over which C3e was verified |

---

### Group D — Ledger Asymmetry Parameters (§9.4)

These are one-time configuration reads, not per-run measurements.

| ID | Measurement | Where to find it |
|---|---|---|
| D1 | T^ℓ₁_dispute configured value (s) | Hardhat adjudicator deployment params in `multiledger-ckb-eth/` (`challengeDuration` const) |
| D2 | T^ℓ₂_dispute configured value (s) | offckb adjudicator deployment params in `virtual-ckb-eth-coordinated/` |
| D3 | δ_ℓ₁ median confirmation delay (ms) | B1a median |
| D4 | δ_ℓ₂ median confirmation delay (ms) | B1b median |
| D5 | Ratio F = δ_ℓ₂ / δ_ℓ₁ | Derived from D3, D4 |
| D6 | Fraction of δ_ℓ₂ measurements that would violate replication bound under a uniform T^ℓ₁_dispute window | Count runs where δ_ℓ₂ > D1, divide by N |
| D7 | D-block confirmation threshold used by coordinator on ℓ₂ | Coordinator service config |
| D8 | CKB block time T_block (ms) | offckb config or measured median block interval |
| D9 | CKB block cycle budget C (max_block_cycles) | `ckb_get_consensus` RPC |

---

## Output Format

Deliver results as a single Markdown file with one table per group (A, B1, B2, C1, C2, C3, D).
Each row: `ID | median | min | max | std-dev | N`.
For boolean values (B2g): `true/false | fraction of runs where true`.
For configured constants (B1c, B1d, D1–D9): single value, no stats needed.

If a measurement cannot be extracted automatically from logs, note the manual extraction
method used so the result is reproducible.

---

## Scenario-to-Section Mapping (Quick Reference)

| Data group | Thesis section | Scenario(s) |
|---|---|---|
| A (cost) | §9.1 Table 9.1 | Sc 2, Sc 4 |
| B1 (enforced parent latency) | §9.2 Table 9.2 | Sc 2 |
| B2 (virtual latency deltas) | §9.2 Table 9.3 | Sc 4 |
| C1 (canonical version) | §9.3 ¶ Canonical and virtual-state selection | Sc 2 |
| C2 (virtual version) | §9.3 ¶ Canonical and virtual-state selection | Sc 4 |
| C3 (financial neutrality) | §9.3 ¶ Financial neutrality observation | Sc 4 |
| D (asymmetry params) | §9.4 | Sc 2, Sc 4 + config files |

---

*Questions about which `% DATA:` comment a measurement maps to: refer to `chapter9_spec_v4.tex` directly.
Every comment is prefixed `% DATA:` and names the variable it fills.*
