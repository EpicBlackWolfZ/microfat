/-
  Formal specification and kernel-checked proofs for Microfat cgroup memory limits,
  overflow-safe extraction arithmetic, and parallel Pack admission.
  Matches `internal/cgroup/cgroup.go`, `internal/cgroup/memory.go`, and
  `internal/pack/parallel.go`.
-/

namespace Microfat.CgroupMath

abbrev maxInt64Nat : Nat := 2 ^ 63 - 1
abbrev defaultMinHeadroomBytes : Nat := 64 * 1024 * 1024
abbrev packUsableMemoryNumerator : Nat := 17
abbrev packUsableMemoryDenominator : Nat := 20
abbrev packBaseMemoryReserveBytes : Nat := 64 * 1024 * 1024

/-! ### Retained Memory & Overflow-Checked Extraction Budget -/

def retainedMemoryLimit (limit retained : Nat) : Nat :=
  if retained < limit then limit - retained else 0

theorem retainedMemoryLimit_le (limit retained : Nat) :
    retainedMemoryLimit limit retained ≤ limit := by
  simp only [retainedMemoryLimit]
  split <;> omega

def addChecked (acc val : Nat) : Option Nat :=
  if val > maxInt64Nat - acc then none else some (acc + val)

def extractionMemory (payload dict decoder reserve : Nat) : Option Nat :=
  if payload > maxInt64Nat then none
  else match addChecked payload dict with
  | none => none
  | some s1 =>
      match addChecked s1 decoder with
      | none => none
      | some s2 => addChecked s2 reserve

theorem extractionMemory_no_overflow (payload dict decoder reserve total : Nat) :
    extractionMemory payload dict decoder reserve = some total ↔
    total = payload + dict + decoder + reserve ∧ total ≤ maxInt64Nat := by
  simp only [extractionMemory, addChecked]
  constructor
  · intro h
    by_cases hp : payload > maxInt64Nat
    · simp [hp] at h
    · simp only [hp, ↓reduceIte] at h
      by_cases hd : dict > maxInt64Nat - payload
      · simp [hd] at h
      · simp only [hd, ↓reduceIte] at h
        by_cases hdec : decoder > maxInt64Nat - (payload + dict)
        · simp [hdec] at h
        · simp only [hdec, ↓reduceIte] at h
          by_cases hres : reserve > maxInt64Nat - (payload + dict + decoder)
          · simp [hres] at h
          · simp only [hres, ↓reduceIte, Option.some.injEq] at h
            exact ⟨by omega, by omega⟩
  · rintro ⟨rfl, hle⟩
    have hp : ¬(payload > maxInt64Nat) := by omega
    have hd : ¬(dict > maxInt64Nat - payload) := by omega
    have hdec : ¬(decoder > maxInt64Nat - (payload + dict)) := by omega
    have hres : ¬(reserve > maxInt64Nat - (payload + dict + decoder)) := by omega
    simp [hp, hd, hdec, hres]

/-! ### Effective Memory Limit & Capped-Headroom GOMEMLIMIT -/

def calculateEffectiveMemoryLimit (maxOpt highOpt : Option Nat) : Option Nat :=
  match maxOpt, highOpt with
  | some m, some h => some (min m h)
  | none, some h => some h
  | some m, none => some m
  | none, none => none

theorem effectiveMemoryLimit_le_both {maxOpt highOpt : Option Nat} {eff : Nat}
    (h : calculateEffectiveMemoryLimit maxOpt highOpt = some eff) :
    (∀ m, maxOpt = some m → eff ≤ m) ∧ (∀ hi, highOpt = some hi → eff ≤ hi) := by
  cases maxOpt <;> cases highOpt <;> simp only [calculateEffectiveMemoryLimit, Option.some.injEq] at h
  · contradiction
  · subst h; exact ⟨fun _ hm => by contradiction, fun _ hh => by cases hh; exact Nat.le_refl _⟩
  · subst h; exact ⟨fun _ hm => by cases hm; exact Nat.le_refl _, fun _ hh => by contradiction⟩
  · subst h
    refine ⟨fun _ hm => ?_, fun _ hh => ?_⟩
    · injection hm with h'; cases h'; exact Nat.min_le_left _ _
    · injection hh with h'; cases h'; exact Nat.min_le_right _ _

def calculateGOMEMLIMIT (limit ratioNum ratioDen minHeadroom : Nat) : Nat :=
  let ratioBudget := (limit * ratioNum) / ratioDen
  let half := limit / 2
  let reserved := min minHeadroom (limit - half)
  let headroomBudget := limit - reserved
  min ratioBudget headroomBudget

theorem gomemlimit_le_limit (limit ratioNum ratioDen minHeadroom : Nat) :
    calculateGOMEMLIMIT limit ratioNum ratioDen minHeadroom ≤ limit := by
  simp only [calculateGOMEMLIMIT]
  exact Nat.le_trans (Nat.min_le_right _ _) (Nat.sub_le _ _)

theorem gomemlimit_headroom_le_half (limit minHeadroom : Nat) :
    let half := limit / 2
    let reserved := min minHeadroom (limit - half)
    reserved ≤ (limit + 1) / 2 := by
  dsimp only
  have h1 : min minHeadroom (limit - limit / 2) ≤ limit - limit / 2 := Nat.min_le_right _ _
  omega

theorem gomemlimit_monotone (l1 l2 ratioNum ratioDen minHeadroom : Nat)
    (hle : l1 ≤ l2) (_hden : ratioDen > 0) :
    calculateGOMEMLIMIT l1 ratioNum ratioDen minHeadroom ≤
    calculateGOMEMLIMIT l2 ratioNum ratioDen minHeadroom := by
  simp only [calculateGOMEMLIMIT]
  have hrat : (l1 * ratioNum) / ratioDen ≤ (l2 * ratioNum) / ratioDen :=
    Nat.div_le_div_right (Nat.mul_le_mul_right ratioNum hle)
  have hhead : l1 - min minHeadroom (l1 - l1 / 2) ≤ l2 - min minHeadroom (l2 - l2 / 2) := by
    omega
  omega

/-! ### Parallel Pack Worker Memory Admission (`admitWorkersByMemory`) -/

/--
Exact loop invariant model of `admitWorkersByMemory` in `internal/pack/parallel.go`:
starts with `admitted = 1`, iterating `k = 1..maxWorkers` over descending worker
estimates `costs`, updating `admitted = k` (for `k >= 2`) whenever `wCost <= workerBudget - cumulative`.

Boundary note (#423): This proves that the sum of the `k` admitted *logical worker
estimates* stays within `workerBudget` (`85% of ceiling - 64 MiB`). Whole-process RSS,
input snapshot buffers, and OS page-cache accounting observed in `#423` under tight
finite cgroups lie outside this logical admission sum.
-/
def admitLoop : List Nat → Nat → Nat → Nat → Nat → Nat → Nat
  | [], _, _, _, _, admitted => admitted
  | wCost :: rest, k, maxWorkers, workerBudget, cumulative, admitted =>
      if k > maxWorkers then admitted
      else if wCost > workerBudget - cumulative then admitted
      else
        let nextCum := cumulative + wCost
        let nextAdm := if k ≥ 2 then k else admitted
        admitLoop rest (k + 1) maxWorkers workerBudget nextCum nextAdm

def admitWorkersByMemory (sortedDescCosts : List Nat) (maxWorkers ceilingBytes : Nat) : Nat :=
  if ceilingBytes == 0 || maxWorkers < 2 then 1
  else
    let usableBytes := (ceilingBytes / packUsableMemoryDenominator) * packUsableMemoryNumerator
    if usableBytes ≤ packBaseMemoryReserveBytes then 1
    else
      let workerBudget := usableBytes - packBaseMemoryReserveBytes
      admitLoop sortedDescCosts 1 maxWorkers workerBudget 0 1

/--
Indexed helper formulation for `admitLoop`:
Tracking `takenCount = acc.length` and `k = takenCount + 1` makes the loop invariant exact.
-/
def admitLoopIndexed : List Nat → Nat → Nat → Nat → Nat → Nat → Nat
  | [], _, _, _, _, admitted => admitted
  | wCost :: rest, taken, maxWorkers, workerBudget, cumulative, admitted =>
      let k := taken + 1
      if k > maxWorkers then admitted
      else if wCost > workerBudget - cumulative then admitted
      else
        let nextCum := cumulative + wCost
        let nextAdm := if k ≥ 2 then k else admitted
        admitLoopIndexed rest k maxWorkers workerBudget nextCum nextAdm

theorem admitLoopIndexed_eq_admitLoop (costs : List Nat) (taken maxWorkers budget cum adm : Nat) :
    admitLoopIndexed costs taken maxWorkers budget cum adm =
    admitLoop costs (taken + 1) maxWorkers budget cum adm := by
  induction costs generalizing taken cum adm with
  | nil => rfl
  | cons w rest ih =>
      simp only [admitLoopIndexed, admitLoop]
      split
      · rfl
      · split
        · rfl
        · exact ih (taken + 1) (cum + w) _

private theorem admitLoopIndexed_sum_le :
    ∀ (costs : List Nat) (prefixList : List Nat) (maxWorkers budget adm : Nat),
      prefixList.sum ≤ budget →
      (adm ≥ 2 → adm ≤ prefixList.length ∧ (prefixList.take adm).sum ≤ budget) →
      admitLoopIndexed costs prefixList.length maxWorkers budget prefixList.sum adm ≥ 2 →
      ((prefixList ++ costs).take
        (admitLoopIndexed costs prefixList.length maxWorkers budget prefixList.sum adm)).sum ≤ budget := by
  intro costs
  induction costs with
  | nil =>
      intro prefixList maxWorkers budget adm _ hadm hres
      simp only [admitLoopIndexed, List.append_nil] at hres ⊢
      exact (hadm hres).2
  | cons w rest ih =>
      intro prefixList maxWorkers budget adm hpre hadm hres
      simp only [admitLoopIndexed] at hres ⊢
      by_cases hk : prefixList.length + 1 > maxWorkers
      · simp only [hk, ↓reduceIte] at hres ⊢
        rw [List.take_append_of_le_length (hadm hres).1]
        exact (hadm hres).2
      · simp only [hk, ↓reduceIte] at hres ⊢
        by_cases hov : w > budget - prefixList.sum
        · simp only [hov, ↓reduceIte] at hres ⊢
          rw [List.take_append_of_le_length (hadm hres).1]
          exact (hadm hres).2
        · simp only [hov, ↓reduceIte] at hres ⊢
          have hsumNext : (prefixList ++ [w]).sum ≤ budget := by
            simp only [List.sum_append, List.sum_cons, List.sum_nil, Nat.add_zero]
            omega
          have hadmNext : (if prefixList.length + 1 ≥ 2 then prefixList.length + 1 else adm) ≥ 2 →
              (if prefixList.length + 1 ≥ 2 then prefixList.length + 1 else adm) ≤ (prefixList ++ [w]).length ∧
              ((prefixList ++ [w]).take (if prefixList.length + 1 ≥ 2 then prefixList.length + 1 else adm)).sum ≤ budget := by
            intro h2
            by_cases hge2 : prefixList.length + 1 ≥ 2
            · simp only [hge2, ↓reduceIte] at h2 ⊢
              refine ⟨by simp, ?_⟩
              have htake : (prefixList ++ [w]).take (prefixList.length + 1) = prefixList ++ [w] := by
                have hlen : (prefixList ++ [w]).length = prefixList.length + 1 := by simp
                rw [← hlen, List.take_length]
              rw [htake]
              exact hsumNext
            · simp only [hge2, ↓reduceIte] at h2 ⊢
              rcases hadm h2 with ⟨hle, hsum⟩
              refine ⟨by simp; omega, ?_⟩
              rw [List.take_append_of_le_length hle]
              exact hsum
          have hrec := ih (prefixList ++ [w]) maxWorkers budget
            (if prefixList.length + 1 ≥ 2 then prefixList.length + 1 else adm) hsumNext hadmNext
          have hlenEq : (prefixList ++ [w]).length = prefixList.length + 1 := by simp
          have hsumEq : (prefixList ++ [w]).sum = prefixList.sum + w := by simp
          rw [hlenEq, hsumEq] at hrec
          have hgoal := hrec hres
          simpa [List.append_assoc] using hgoal

theorem admitWorkers_sum_le_budget (sortedDescCosts : List Nat) (maxWorkers ceilingBytes : Nat) :
    let usable := (ceilingBytes / packUsableMemoryDenominator) * packUsableMemoryNumerator
    let workerBudget := usable - packBaseMemoryReserveBytes
    let admitted := admitWorkersByMemory sortedDescCosts maxWorkers ceilingBytes
    admitted ≥ 2 → (sortedDescCosts.take admitted).sum ≤ workerBudget := by
  dsimp only
  intro hadm
  simp only [admitWorkersByMemory] at hadm ⊢
  by_cases h1 : (ceilingBytes == 0 || maxWorkers < 2) = true
  · simp only [eq_true h1, ↓reduceIte] at hadm
    omega
  · simp only [eq_false h1, ↓reduceIte] at hadm ⊢
    by_cases h2 : (ceilingBytes / packUsableMemoryDenominator) * packUsableMemoryNumerator ≤ packBaseMemoryReserveBytes
    · simp only [eq_true h2, ↓reduceIte] at hadm
      omega
    · simp only [eq_false h2, ↓reduceIte] at hadm ⊢
      rw [← admitLoopIndexed_eq_admitLoop] at hadm ⊢
      have h0 := admitLoopIndexed_sum_le sortedDescCosts [] maxWorkers
        ((ceilingBytes / packUsableMemoryDenominator) * packUsableMemoryNumerator - packBaseMemoryReserveBytes) 1
      simpa using h0 (by simp) (fun h => by omega) hadm

end Microfat.CgroupMath
