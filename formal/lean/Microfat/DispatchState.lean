/-
  Formal specification and kernel-checked state machine proofs for Microfat
  launcher dispatch (`auto` / `memfd` / `cache`), 4-seal monotonic lockouts,
  unsafe/corrupt/noexec cache handling, and `MICROFAT_EXEC_MODE` vs
  `MICROFAT_DISPATCH_MODE` separation. Matches `SPEC-0003` and
  `cmd/microfat-stub/exec_linux.go` + `experiments/v3lab/rust-stub/src/main.rs`.
-/

namespace Microfat.DispatchState

inductive ExecMode where
  | auto
  | memfd
  | cache
  deriving DecidableEq, Repr

inductive DispatchMech where
  | memfd
  | cache
  deriving DecidableEq, Repr

inductive CacheLookup where
  | hitValid
  | hitExecFailed
  | missAbsent
  | missCorruptSafe
  | missNoExec
  | unsafeEntry
  deriving DecidableEq, Repr

inductive MemfdOutcome where
  | memfdExecOk
  | memfdBlocked
  | memfdSealFailed
  | memfdExecFailed
  | memfdCorruptOrBudget
  deriving DecidableEq, Repr

inductive CacheMatOutcome where
  | cacheMatExecOk
  | cacheMatNoExec
  | cacheMatUnsafe
  | cacheMatFailed
  deriving DecidableEq, Repr

structure SealSet where
  write    : Bool
  shrink   : Bool
  grow     : Bool
  sealLock : Bool
  deriving DecidableEq, Repr

def SealSet.empty : SealSet :=
  { write := false, shrink := false, grow := false, sealLock := false }

def SealSet.target : SealSet :=
  { write := true, shrink := true, grow := true, sealLock := true }

def SealSet.subset (a b : SealSet) : Prop :=
  (a.write = true → b.write = true) ∧
  (a.shrink = true → b.shrink = true) ∧
  (a.grow = true → b.grow = true) ∧
  (a.sealLock = true → b.sealLock = true)

def addWriteSeal (s : SealSet) : SealSet := { s with write := true }
def addShrinkSeal (s : SealSet) : SealSet := { s with shrink := true }
def addGrowSeal (s : SealSet) : SealSet := { s with grow := true }
def addLockSeal (s : SealSet) : SealSet := { s with sealLock := true }

def applyAllTargetSeals (s : SealSet) : SealSet :=
  addLockSeal (addGrowSeal (addShrinkSeal (addWriteSeal s)))

theorem seals_step_monotone (s : SealSet) :
    SealSet.subset s (addWriteSeal s) ∧
    SealSet.subset s (addShrinkSeal s) ∧
    SealSet.subset s (addGrowSeal s) ∧
    SealSet.subset s (addLockSeal s) := by
  refine ⟨?_, ?_, ?_, ?_⟩ <;> simp [SealSet.subset, addWriteSeal, addShrinkSeal, addGrowSeal, addLockSeal]

theorem applyAllTargetSeals_complete_and_monotone (s : SealSet) :
    applyAllTargetSeals s = SealSet.target ∧ SealSet.subset s (applyAllTargetSeals s) := by
  constructor
  · rfl
  · simp [SealSet.subset, applyAllTargetSeals, addWriteSeal, addShrinkSeal, addGrowSeal, addLockSeal]

structure DispatchOutcome where
  ok               : Bool
  dispatchMode     : Option DispatchMech
  preservedReqMode : ExecMode
  cacheMutated     : Bool
  usedMemfd        : Bool
  attempts         : Nat
  deriving DecidableEq, Repr

def dispatch (req : ExecMode) (lookup : CacheLookup) (m : MemfdOutcome)
    (cm : CacheMatOutcome) : DispatchOutcome :=
  match req with
  | .memfd =>
      { ok := m == .memfdExecOk
      , dispatchMode := if m == .memfdExecOk then some .memfd else none
      , preservedReqMode := .memfd
      , cacheMutated := false
      , usedMemfd := true
      , attempts := 1 }
  | .cache =>
      match lookup with
      | .hitValid =>
          { ok := true
          , dispatchMode := some .cache
          , preservedReqMode := .cache
          , cacheMutated := false
          , usedMemfd := false
          , attempts := 1 }
      | .unsafeEntry | .missNoExec | .hitExecFailed =>
          { ok := false
          , dispatchMode := none
          , preservedReqMode := .cache
          , cacheMutated := false
          , usedMemfd := false
          , attempts := 1 }
      | .missAbsent | .missCorruptSafe =>
          let ok := cm == .cacheMatExecOk
          { ok := ok
          , dispatchMode := if ok then some .cache else none
          , preservedReqMode := .cache
          , cacheMutated := ok
          , usedMemfd := false
          , attempts := 1 }
  | .auto =>
      match lookup with
      | .unsafeEntry =>
          { ok := false
          , dispatchMode := none
          , preservedReqMode := .auto
          , cacheMutated := false
          , usedMemfd := false
          , attempts := 1 }
      | .hitValid =>
          { ok := true
          , dispatchMode := some .cache
          , preservedReqMode := .auto
          , cacheMutated := false
          , usedMemfd := false
          , attempts := 1 }
      | .hitExecFailed =>
          let ok := m == .memfdExecOk
          { ok := ok
          , dispatchMode := if ok then some .memfd else none
          , preservedReqMode := .auto
          , cacheMutated := false
          , usedMemfd := true
          , attempts := 2 }
      | .missAbsent | .missCorruptSafe | .missNoExec =>
          match m with
          | .memfdExecOk =>
              { ok := true
              , dispatchMode := some .memfd
              , preservedReqMode := .auto
              , cacheMutated := false
              , usedMemfd := true
              , attempts := 1 }
          | .memfdCorruptOrBudget =>
              { ok := false
              , dispatchMode := none
              , preservedReqMode := .auto
              , cacheMutated := false
              , usedMemfd := true
              , attempts := 1 }
          | .memfdBlocked | .memfdSealFailed | .memfdExecFailed =>
              if lookup == .missNoExec then
                { ok := false
                , dispatchMode := none
                , preservedReqMode := .auto
                , cacheMutated := false
                , usedMemfd := true
                , attempts := 2 }
              else
                let ok := cm == .cacheMatExecOk
                { ok := ok
                , dispatchMode := if ok then some .cache else none
                , preservedReqMode := .auto
                , cacheMutated := ok
                , usedMemfd := true
                , attempts := 2 }

theorem auto_unsafe_entry_fails_closed_no_mutation (m : MemfdOutcome) (cm : CacheMatOutcome) :
    let out := dispatch .auto .unsafeEntry m cm
    out.ok = false ∧ out.cacheMutated = false ∧ out.usedMemfd = false := by
  simp [dispatch]

theorem auto_warm_hit_read_only_no_mutation (m : MemfdOutcome) (cm : CacheMatOutcome) :
    let out := dispatch .auto .hitValid m cm
    out.ok = true ∧ out.dispatchMode = some .cache ∧ out.cacheMutated = false ∧ out.usedMemfd = false := by
  simp [dispatch]

theorem auto_safe_miss_memfd_ok_leaves_cache_untouched
    (lookup : CacheLookup)
    (hmiss : lookup = .missAbsent ∨ lookup = .missCorruptSafe ∨ lookup = .missNoExec)
    (cm : CacheMatOutcome) :
    let out := dispatch .auto lookup .memfdExecOk cm
    out.ok = true ∧ out.dispatchMode = some .memfd ∧ out.cacheMutated = false := by
  rcases hmiss with rfl | rfl | rfl <;> simp [dispatch]

theorem auto_corrupt_or_budget_never_materializes_cache
    (lookup : CacheLookup) (cm : CacheMatOutcome) :
    let out := dispatch .auto lookup .memfdCorruptOrBudget cm
    out.cacheMutated = false ∧ (lookup ≠ .hitValid → out.ok = false) := by
  cases lookup <;> simp [dispatch]

theorem explicit_memfd_never_touches_cache (lookup : CacheLookup) (m : MemfdOutcome) (cm : CacheMatOutcome) :
    let out := dispatch .memfd lookup m cm
    out.cacheMutated = false ∧ (out.ok = true → out.dispatchMode = some .memfd) := by
  cases m <;> simp [dispatch]

theorem explicit_cache_never_uses_memfd (lookup : CacheLookup) (m : MemfdOutcome) (cm : CacheMatOutcome) :
    let out := dispatch .cache lookup m cm
    out.usedMemfd = false ∧ (out.ok = true → out.dispatchMode = some .cache) := by
  cases lookup <;> cases cm <;> simp [dispatch]

theorem exec_mode_policy_preserved_and_dispatch_bounded
    (req : ExecMode) (lookup : CacheLookup) (m : MemfdOutcome) (cm : CacheMatOutcome) :
    let out := dispatch req lookup m cm
    out.preservedReqMode = req ∧ out.attempts ≥ 1 ∧ out.attempts ≤ 2 ∧
    (out.ok = true ↔ out.dispatchMode.isSome = true) := by
  cases req <;> cases lookup <;> cases m <;> cases cm <;> simp [dispatch]

end Microfat.DispatchState
