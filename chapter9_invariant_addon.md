# Invariant Reference — Add-on for PoC Developer

**Attached to:** Chapter 9 Data Handover (`chapter9_data_handover.md`)
**Purpose:** Explains the four invariants you are checking in Group C so you know
exactly what on-chain state to inspect and what constitutes pass vs. fail.

---

## What an Invariant Is (in This Context)

An invariant is a property that must hold over the on-chain state at the end of
a scenario run. You do not prove it — you **observe** whether it holds or is
violated by reading the final on-chain state after all transactions complete.

The thesis reports the observation in Table 9.6 (§9.3). Your job is to
record the binary result (consistent / violated) for each invariant in each scenario.

---

## The Four Invariants

### I1 — Canonical State Uniqueness

**Plain English:** Both chains release funds under the *same* channel state version.

**Passes when:**
The version number embedded in the `conclude()` call on ℓ₁ equals the version
number embedded in the `conclude()` call on ℓ₂.

**Violated when:**
ℓ₁ settles at version `v₁` and ℓ₂ settles at version `v₂` where `v₁ ≠ v₂`.
This is exactly what the divergent-settlement attack produces in Scenario 1.

**What to read from the chain:**
- Ethereum: the `register()` and `conclude()` transaction call data — extract the
  `channelState.Version` field from the ABI-decoded input.
- CKB: the witness data of the `conclude` transaction — extract the version field
  from the Molecule-decoded channel state.

---

### I2 — Withdrawal Gate Integrity

**Plain English:** A chain only releases funds after it has passed through the
`cross-chain-committed` phase with a locked-in canonical state.

**Passes when:**
The `conclude()` transaction on each chain is preceded by a successful
`coordinate()` transaction that set the contract's phase to `cross-chain-committed`.
No `conclude()` call succeeds without that prior `coordinate()`.

**Violated when:**
A chain releases funds (i.e., `conclude()` succeeds) while its phase is still
`locally-finalised` — meaning the coordinator gate was bypassed.
This happens in Scenario 1 because there is no coordinator, so the contract
never enforces the gate.

**What to read from the chain:**
- Check the sequence of transactions on each chain: `register()` → (expected:
  `coordinate()`) → `conclude()`.
- In Scenario 1 (attack without coordinator) the sequence is `register()` →
  `conclude()` with no `coordinate()` in between. That is the violation.
- In Scenarios 2 and 4, the `coordinate()` transaction must appear and succeed
  before `conclude()` is accepted. Verify the contract emitted a
  `CrossChainCommitted` (or equivalent) event before `conclude()`.

---

### I3 — Finality Before Coordination

**Plain English:** The coordinator only issues its certificate after *both* chains
have confirmed their dispute registrations (i.e., both have reached
`locally-finalised`).

**Passes when:**
The block number of the `coordinate()` transaction on ℓ₁ is strictly greater
than the block number at which ℓ₁'s challenge window expired *and* the block
number of the `coordinate()` transaction on ℓ₂ is strictly greater than the
block number at which ℓ₂'s challenge window expired.
In other words: the coordinator did not act prematurely on either chain.

**Violated when:**
The coordinator issues its certificate while at least one chain's challenge window
is still open — meaning a later `register()` call could still override the state.
In the attack scenarios (Sc 1, Sc 3) there is no coordinator at all, so I3 is
marked `n/a` (not applicable), not violated.

**What to read from the chain:**
- Record the block number at which the challenge window closes on each chain
  (= block of `register()` + `T^ℓ_dispute` / block-time).
- Record the block number of the `coordinate()` transaction on each chain.
- Pass condition: `block(coordinate_ℓ) > challenge_close_block_ℓ` for both ℓ.

---

### I1* — Global Canonical State Uniqueness (Virtual Layer)

**Plain English:** When a virtual channel is open, *both* chains release funds
under the same virtual channel state version *and* the same parent channel state
version. The pair (σ*, σ^vc_max) must be identical on both chains.

**Extends I1** by adding the requirement that the virtual sub-state is also
consistent across chains — not just the parent.

**Passes when:**
- The parent version under which ℓ₁ enters withdrawal equals the parent version
  under which ℓ₂ enters withdrawal (same as I1), **and**
- The virtual channel version embedded in the `coordinateVC()` call on ℓ₁
  equals the virtual channel version embedded in the `coordinateVC()` call on ℓ₂.

**Violated when:**
The virtual adjudicator V̂_ℓ₁ seals at a different virtual version than V̂_ℓ₂,
and those divergent versions propagate to the parent withdrawal.
This is exactly what Scenario 3 produces: vc version 0 on CKB, vc version 1 on ETH.

**What to read from the chain:**
- ETH virtual adjudicator: ABI-decode the `register()` call to V̂_ℓ₁ —
  extract `vcState.Version`.
- CKB virtual adjudicator: Molecule-decode the witness of the register transaction
  to V̂_ℓ₂ — extract the virtual state version field.
- Then read the `coordinateVC()` call data on both chains and extract the
  virtual sub-state version it carries.
- Pass condition: all four version numbers are equal.

---

## Invariant × Scenario Matrix

Use this to fill Table 9.6 in the thesis.

| Scenario | I1 | I2 | I3 | I1* | Expected result |
|---|---|---|---|---|---|
| Sc 1 — Attack, no coordinator (parent) | violated | violated | n/a | n/a | v₁ ≠ v₂ on the two chains |
| Sc 2 — Coordinator active (parent only) | consistent | consistent | consistent | n/a | v₁ = v₂ = σ* on both chains |
| Sc 3 — VC attack, no coordinator | violated | violated | n/a | violated | parent and vc versions diverge |
| Sc 4 — Coordinator active (with VC) | consistent | consistent | consistent | consistent | (σ*, σ^vc_max) identical on both chains |

If any scenario produces an unexpected result (e.g., Sc 2 shows I1 violated),
**do not adjust the table** — report the observed result and flag it in your
delivery note so the thesis author can investigate before writing Chapter 9.

---

## Terminology Glossary

| Term used in thesis | Meaning in the codebase |
|---|---|
| ℓ₁ | Ethereum chain (`chaineth/`, port 8545) |
| ℓ₂ | Nervos CKB chain (`chainckb/`, port 8114) |
| Ĉ_ℓ | The adjudicator contract instance on ledger ℓ |
| V̂_ℓ | The virtual adjudicator contract instance on ledger ℓ |
| σ* | The canonical parent state selected by the coordinator (`coordinate()` call) |
| σ^vc_max | The canonical virtual state selected by the coordinator (`coordinateVC()` call) |
| locally-finalised | Contract phase after challenge window closes, before coordinator acts |
| cross-chain-committed | Contract phase after `coordinate()` succeeds — withdrawal gate opens |
| T^ℓ_dispute | Challenge window duration configured per chain in the adjudicator contract |
| T_coord | Coordinator response timeout — configured in coordinator service |
| T_vc | Virtual channel challenge window — configured in virtual adjudicator |
| ReadyForCoordination(cid) | Internal coordinator predicate: true when both chains are locally-finalised for channel `cid` |

---

*This add-on is a companion to `chapter9_data_handover.md`. Read that document first
for run instructions and the full list of measurements.*
