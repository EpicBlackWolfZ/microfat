/-
  Formal specification and kernel-checked state machine proofs for Microfat
  versioned generation installation, idempotent reuse, atomic symlink switch,
  and post-activation rollback. Matches `SPEC-0007` and `internal/install/transaction.go`.
-/

namespace Microfat.InstallTx

inductive TxStage where
  | ok
  | preActivationFail
  | activationRenameFail
  | postActivationFail
  deriving DecidableEq, Repr

structure StoreState where
  ownerInitialized : Bool
  activeGen        : Option Nat
  installedGens    : List Nat
  stagedTemp       : Option Nat
  deriving DecidableEq, Repr

structure ApplyOutcome where
  state      : StoreState
  ok         : Bool
  activated  : Bool
  reused     : Bool
  prevActive : Option Nat
  deriving DecidableEq, Repr

def isInstalled (s : StoreState) (gen : Nat) : Bool :=
  s.installedGens.contains gen

def applyGeneration (s : StoreState) (targetGen : Nat) (stage : TxStage) : ApplyOutcome :=
  if s.activeGen == some targetGen && isInstalled s targetGen then
    { state := { s with ownerInitialized := true, stagedTemp := none }
    , ok := true
    , activated := false
    , reused := true
    , prevActive := s.activeGen }
  else if isInstalled s targetGen then
    -- Existing generation directories are immutable and never overwritten.
    { state := { s with stagedTemp := none }
    , ok := false
    , activated := false
    , reused := false
    , prevActive := s.activeGen }
  else
    let ownerState : StoreState := { s with ownerInitialized := true }
    match stage with
    | .preActivationFail =>
        { state := { ownerState with stagedTemp := none }
        , ok := false
        , activated := false
        , reused := false
        , prevActive := s.activeGen }
    | .activationRenameFail =>
        { state := { ownerState with installedGens := targetGen :: ownerState.installedGens, stagedTemp := none }
        , ok := false
        , activated := false
        , reused := false
        , prevActive := s.activeGen }
    | .postActivationFail =>
        let committed : StoreState :=
          { ownerInitialized := true
          , activeGen := some targetGen
          , installedGens := targetGen :: ownerState.installedGens
          , stagedTemp := none }
        { state := committed
        , ok := false
        , activated := true
        , reused := false
        , prevActive := s.activeGen }
    | .ok =>
        let committed : StoreState :=
          { ownerInitialized := true
          , activeGen := some targetGen
          , installedGens := targetGen :: ownerState.installedGens
          , stagedTemp := none }
        { state := committed
        , ok := true
        , activated := true
        , reused := false
        , prevActive := s.activeGen }

def rollbackGeneration (outcome : ApplyOutcome) : StoreState :=
  { outcome.state with activeGen := outcome.prevActive, stagedTemp := none }

theorem pre_activation_failure_preserves_active_and_cleans_staging
    (s : StoreState) (g : Nat) (hnew : isInstalled s g = false)
    (hfail : stage = TxStage.preActivationFail ∨ stage = TxStage.activationRenameFail) :
    let out := applyGeneration s g stage
    out.ok = false ∧ out.activated = false ∧
    out.state.activeGen = s.activeGen ∧ out.state.stagedTemp = none := by
  rcases hfail with rfl | rfl <;>
    simp [applyGeneration, hnew]

theorem reuse_identical_generation_preserves_active
    (s : StoreState) (g : Nat)
    (hcur : s.activeGen = some g) (hinst : isInstalled s g = true) :
    let out := applyGeneration s g .ok
    out.ok = true ∧ out.reused = true ∧ out.activated = false ∧
    out.state.activeGen = some g ∧ out.state.installedGens = s.installedGens := by
  simp [applyGeneration, hcur, hinst]

theorem existing_generation_never_overwritten
    (s : StoreState) (g : Nat) (stage : TxStage)
    (hother : s.activeGen ≠ some g) (hinst : isInstalled s g = true) :
    let out := applyGeneration s g stage
    out.ok = false ∧ out.activated = false ∧
    out.state.activeGen = s.activeGen ∧ out.state.installedGens = s.installedGens := by
  simp [applyGeneration, hother, hinst]

theorem successful_activation_switches_atomically
    (s : StoreState) (g : Nat) (hnew : isInstalled s g = false) :
    let out := applyGeneration s g .ok
    out.ok = true ∧ out.activated = true ∧
    out.state.activeGen = some g ∧ out.state.stagedTemp = none := by
  simp [applyGeneration, hnew]

theorem rollback_restores_previous_active_generation
    (s : StoreState) (g : Nat) (hnew : isInstalled s g = false) :
    let out := applyGeneration s g .postActivationFail
    let rolled := rollbackGeneration out
    out.activated = true ∧ out.ok = false ∧
    rolled.activeGen = s.activeGen ∧ rolled.stagedTemp = none := by
  simp [applyGeneration, rollbackGeneration, hnew]

end Microfat.InstallTx
