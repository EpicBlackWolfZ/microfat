/-
  Formal specification and kernel-checked proofs for Microfat CPU capability
  detection, non-linear ARM64 v8.x/v9.x DAG compatibility, and policy selection.
  Matches `internal/microarch/microarch.go`.
-/

namespace Microfat.Microarch

inductive Arch where
  | amd64
  | arm64
  deriving DecidableEq, Repr

inductive AMD64Level where
  | v1 | v2 | v3 | v4
  deriving DecidableEq, Repr

structure X86Features where
  hasLAHFSAHF : Bool
  hasCX16     : Bool
  hasPOPCNT   : Bool
  hasSSE3     : Bool
  hasSSSE3    : Bool
  hasSSE41    : Bool
  hasSSE42    : Bool
  hasAVX      : Bool
  hasAVX2     : Bool
  hasBMI1     : Bool
  hasBMI2     : Bool
  hasFMA      : Bool
  hasOSXSAVE  : Bool
  hasF16C     : Bool
  hasLZCNT    : Bool
  hasMOVBE    : Bool
  hasAVX512F  : Bool
  hasAVX512BW : Bool
  hasAVX512CD : Bool
  hasAVX512DQ : Bool
  hasAVX512VL : Bool
  deriving DecidableEq, Repr

def X86Features.le (a b : X86Features) : Prop :=
  (a.hasLAHFSAHF = true → b.hasLAHFSAHF = true) ∧
  (a.hasCX16 = true → b.hasCX16 = true) ∧
  (a.hasPOPCNT = true → b.hasPOPCNT = true) ∧
  (a.hasSSE3 = true → b.hasSSE3 = true) ∧
  (a.hasSSSE3 = true → b.hasSSSE3 = true) ∧
  (a.hasSSE41 = true → b.hasSSE41 = true) ∧
  (a.hasSSE42 = true → b.hasSSE42 = true) ∧
  (a.hasAVX = true → b.hasAVX = true) ∧
  (a.hasAVX2 = true → b.hasAVX2 = true) ∧
  (a.hasBMI1 = true → b.hasBMI1 = true) ∧
  (a.hasBMI2 = true → b.hasBMI2 = true) ∧
  (a.hasFMA = true → b.hasFMA = true) ∧
  (a.hasOSXSAVE = true → b.hasOSXSAVE = true) ∧
  (a.hasF16C = true → b.hasF16C = true) ∧
  (a.hasLZCNT = true → b.hasLZCNT = true) ∧
  (a.hasMOVBE = true → b.hasMOVBE = true) ∧
  (a.hasAVX512F = true → b.hasAVX512F = true) ∧
  (a.hasAVX512BW = true → b.hasAVX512BW = true) ∧
  (a.hasAVX512CD = true → b.hasAVX512CD = true) ∧
  (a.hasAVX512DQ = true → b.hasAVX512DQ = true) ∧
  (a.hasAVX512VL = true → b.hasAVX512VL = true)

def amd64Rank : AMD64Level → Nat
  | .v1 => 1
  | .v2 => 2
  | .v3 => 3
  | .v4 => 4

def amd64Satisfied (lvl : AMD64Level) (f : X86Features) : Bool :=
  let hasV2 := f.hasLAHFSAHF && f.hasCX16 && f.hasPOPCNT && f.hasSSE3 && f.hasSSSE3 && f.hasSSE41 && f.hasSSE42
  let hasV3 := hasV2 && f.hasAVX && f.hasAVX2 && f.hasBMI1 && f.hasBMI2 && f.hasFMA &&
    f.hasOSXSAVE && f.hasF16C && f.hasLZCNT && f.hasMOVBE
  let hasV4 := hasV3 && f.hasAVX512F && f.hasAVX512BW && f.hasAVX512CD && f.hasAVX512DQ && f.hasAVX512VL
  match lvl with
  | .v1 => true
  | .v2 => hasV2
  | .v3 => hasV3
  | .v4 => hasV4

def evaluateAMD64 (f : X86Features) : AMD64Level :=
  if amd64Satisfied .v4 f then .v4
  else if amd64Satisfied .v3 f then .v3
  else if amd64Satisfied .v2 f then .v2
  else .v1

theorem amd64_eval_rank_iff (lvl : AMD64Level) (f : X86Features) :
    amd64Satisfied lvl f = true ↔ amd64Rank lvl ≤ amd64Rank (evaluateAMD64 f) := by
  have h43 : amd64Satisfied .v4 f = true → amd64Satisfied .v3 f = true := by
    simp only [amd64Satisfied, Bool.and_eq_true]
    intro h; exact h.1.1.1.1.1
  have h32 : amd64Satisfied .v3 f = true → amd64Satisfied .v2 f = true := by
    simp only [amd64Satisfied, Bool.and_eq_true]
    intro h; exact h.1.1.1.1.1.1.1.1.1
  have h1 : amd64Satisfied .v1 f = true := rfl
  by_cases h4 : amd64Satisfied .v4 f = true
  · have h3 := h43 h4
    have h2 := h32 h3
    cases lvl <;> simp [evaluateAMD64, h4, h3, h2, h1, amd64Rank]
  · by_cases h3 : amd64Satisfied .v3 f = true
    · have h2 := h32 h3
      cases lvl <;> simp [evaluateAMD64, h4, h3, h2, h1, amd64Rank]
    · by_cases h2 : amd64Satisfied .v2 f = true
      · cases lvl <;> simp [evaluateAMD64, h4, h3, h2, h1, amd64Rank]
      · cases lvl <;> simp [evaluateAMD64, h4, h3, h2, h1, amd64Rank]

theorem amd64_feature_monotonicity {a b : X86Features} (hle : X86Features.le a b)
    (lvl : AMD64Level) (h : amd64Satisfied lvl a = true) :
    amd64Satisfied lvl b = true := by
  rcases hle with ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13, h14, h15, h16, h17, h18, h19, h20, h21⟩
  cases lvl <;> simp_all [amd64Satisfied]

/-! ### ARM64 Non-Linear Prerequisite DAG (`v8.0`..`v8.9`, `v9.0`..`v9.5`) -/

inductive ARM64Level where
  | v8_0 | v8_1 | v8_2 | v8_3 | v8_4 | v8_5 | v8_6 | v8_7 | v8_8 | v8_9
  | v9_0 | v9_1 | v9_2 | v9_3 | v9_4 | v9_5
  deriving DecidableEq, Repr

structure ARM64Features where
  hasFP      : Bool
  hasASIMD   : Bool
  hasATOMICS : Bool
  hasCRC32   : Bool
  hasFPHP    : Bool
  hasASIMDHP : Bool
  hasJSCVT   : Bool
  hasFCMA    : Bool
  hasLRCPC   : Bool
  hasDCPOP   : Bool
  hasASIMDDP : Bool
  hasDIT     : Bool
  hasI8MM    : Bool
  hasBF16    : Bool
  hasWFxT    : Bool
  hasMOPS    : Bool
  hasNMI     : Bool
  hasHBC     : Bool
  hasGCS     : Bool
  hasTHE     : Bool
  hasSVE     : Bool
  hasSVE2    : Bool
  hasSME     : Bool
  hasSME2    : Bool
  deriving DecidableEq, Repr

def ARM64Features.le (a b : ARM64Features) : Prop :=
  (a.hasFP = true → b.hasFP = true) ∧
  (a.hasASIMD = true → b.hasASIMD = true) ∧
  (a.hasATOMICS = true → b.hasATOMICS = true) ∧
  (a.hasCRC32 = true → b.hasCRC32 = true) ∧
  (a.hasFPHP = true → b.hasFPHP = true) ∧
  (a.hasASIMDHP = true → b.hasASIMDHP = true) ∧
  (a.hasJSCVT = true → b.hasJSCVT = true) ∧
  (a.hasFCMA = true → b.hasFCMA = true) ∧
  (a.hasLRCPC = true → b.hasLRCPC = true) ∧
  (a.hasDCPOP = true → b.hasDCPOP = true) ∧
  (a.hasASIMDDP = true → b.hasASIMDDP = true) ∧
  (a.hasDIT = true → b.hasDIT = true) ∧
  (a.hasI8MM = true → b.hasI8MM = true) ∧
  (a.hasBF16 = true → b.hasBF16 = true) ∧
  (a.hasWFxT = true → b.hasWFxT = true) ∧
  (a.hasMOPS = true → b.hasMOPS = true) ∧
  (a.hasNMI = true → b.hasNMI = true) ∧
  (a.hasHBC = true → b.hasHBC = true) ∧
  (a.hasGCS = true → b.hasGCS = true) ∧
  (a.hasTHE = true → b.hasTHE = true) ∧
  (a.hasSVE = true → b.hasSVE = true) ∧
  (a.hasSVE2 = true → b.hasSVE2 = true) ∧
  (a.hasSME = true → b.hasSME = true) ∧
  (a.hasSME2 = true → b.hasSME2 = true)

def arm64Rank : ARM64Level → Nat
  | .v8_0 => 80 | .v8_1 => 81 | .v8_2 => 82 | .v8_3 => 83 | .v8_4 => 84
  | .v8_5 => 85 | .v8_6 => 86 | .v8_7 => 87 | .v8_8 => 88 | .v8_9 => 89
  | .v9_0 => 90 | .v9_1 => 91 | .v9_2 => 92 | .v9_3 => 93 | .v9_4 => 94
  | .v9_5 => 95

def arm64Prereqs : ARM64Level → List ARM64Level
  | .v8_0 => []
  | .v8_1 => [.v8_0]
  | .v8_2 => [.v8_1]
  | .v8_3 => [.v8_2]
  | .v8_4 => [.v8_3]
  | .v8_5 => [.v8_4]
  | .v8_6 => [.v8_5]
  | .v8_7 => [.v8_6]
  | .v8_8 => [.v8_7]
  | .v8_9 => [.v8_8]
  | .v9_0 => [.v8_5]
  | .v9_1 => [.v9_0, .v8_6]
  | .v9_2 => [.v9_1]
  | .v9_3 => [.v9_2]
  | .v9_4 => [.v9_3]
  | .v9_5 => [.v9_4]

def arm64TopoMeasure : ARM64Level → Nat
  | .v8_0 => 0  | .v8_1 => 1  | .v8_2 => 2  | .v8_3 => 3  | .v8_4 => 4
  | .v8_5 => 5  | .v8_6 => 6  | .v8_7 => 7  | .v8_8 => 8  | .v8_9 => 9
  | .v9_0 => 6  | .v9_1 => 7  | .v9_2 => 8  | .v9_3 => 9  | .v9_4 => 10
  | .v9_5 => 11

theorem arm64_prereqs_acyclic :
    ∀ (l p : ARM64Level), p ∈ arm64Prereqs l → arm64TopoMeasure p < arm64TopoMeasure l := by
  intro l p hp
  cases l <;> simp [arm64Prereqs] at hp <;> rcases hp with rfl | rfl <;> decide

def arm64DirectFeatures (lvl : ARM64Level) (f : ARM64Features) : Bool :=
  match lvl with
  | .v8_0 => f.hasFP && f.hasASIMD
  | .v8_1 => f.hasATOMICS && f.hasCRC32
  | .v8_2 => f.hasFPHP && f.hasASIMDHP
  | .v8_3 => f.hasJSCVT && f.hasFCMA && f.hasLRCPC
  | .v8_4 => f.hasDCPOP && f.hasASIMDDP
  | .v8_5 => f.hasDIT
  | .v8_6 => f.hasI8MM && f.hasBF16
  | .v8_7 => f.hasWFxT
  | .v8_8 => f.hasMOPS && f.hasNMI && f.hasHBC
  | .v8_9 => f.hasGCS && f.hasTHE
  | .v9_0 => f.hasSVE
  | .v9_1 => true
  | .v9_2 => f.hasSVE2
  | .v9_3 => f.hasSME
  | .v9_4 => true
  | .v9_5 => f.hasSME2

def arm64Satisfied (lvl : ARM64Level) (f : ARM64Features) : Bool :=
  arm64DirectFeatures lvl f &&
  match lvl with
  | .v8_0 => true
  | .v8_1 => arm64Satisfied .v8_0 f
  | .v8_2 => arm64Satisfied .v8_1 f
  | .v8_3 => arm64Satisfied .v8_2 f
  | .v8_4 => arm64Satisfied .v8_3 f
  | .v8_5 => arm64Satisfied .v8_4 f
  | .v8_6 => arm64Satisfied .v8_5 f
  | .v8_7 => arm64Satisfied .v8_6 f
  | .v8_8 => arm64Satisfied .v8_7 f
  | .v8_9 => arm64Satisfied .v8_8 f
  | .v9_0 => arm64Satisfied .v8_5 f
  | .v9_1 => arm64Satisfied .v9_0 f && arm64Satisfied .v8_6 f
  | .v9_2 => arm64Satisfied .v9_1 f
  | .v9_3 => arm64Satisfied .v9_2 f
  | .v9_4 => arm64Satisfied .v9_3 f
  | .v9_5 => arm64Satisfied .v9_4 f
termination_by arm64TopoMeasure lvl
decreasing_by all_goals (simp [arm64TopoMeasure])

theorem arm64Satisfied_prereq_closure (lvl : ARM64Level) (f : ARM64Features)
    (hsat : arm64Satisfied lvl f = true) :
    arm64DirectFeatures lvl f = true ∧
    ∀ p ∈ arm64Prereqs lvl, arm64Satisfied p f = true := by
  cases lvl
  · rw [arm64Satisfied] at hsat
    exact ⟨by simpa using hsat, fun p hp => by simp [arm64Prereqs] at hp⟩
  all_goals
    try (
      rw [arm64Satisfied] at hsat
      simp only [Bool.and_eq_true] at hsat
      rcases hsat with ⟨hd, hp⟩
      exact ⟨hd, fun q hq => by simp [arm64Prereqs] at hq; subst hq; exact hp⟩
    )
  · rw [arm64Satisfied] at hsat
    simp only [Bool.and_eq_true] at hsat
    rcases hsat with ⟨hd, h90, h86⟩
    refine ⟨hd, fun q hq => ?_⟩
    simp only [arm64Prereqs, List.mem_cons, List.not_mem_nil, or_false] at hq
    rcases hq with rfl | rfl <;> assumption

theorem arm64DirectFeatures_mono {a b : ARM64Features} (hle : ARM64Features.le a b)
    (lvl : ARM64Level) (h : arm64DirectFeatures lvl a = true) :
    arm64DirectFeatures lvl b = true := by
  rcases hle with ⟨h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13, h14, h15, h16, h17, h18, h19, h20, h21, h22, h23, h24⟩
  cases lvl <;> simp_all [arm64DirectFeatures]

theorem arm64_feature_monotonicity {a b : ARM64Features} (hle : ARM64Features.le a b) :
    ∀ lvl : ARM64Level, arm64Satisfied lvl a = true → arm64Satisfied lvl b = true
  | .v8_0, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_0 a hsat
      rw [arm64Satisfied]
      simpa using arm64DirectFeatures_mono hle .v8_0 hcl.1
  | .v8_1, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_1 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_1 hcl.1,
        arm64_feature_monotonicity hle .v8_0 (hcl.2 .v8_0 (by simp [arm64Prereqs]))⟩
  | .v8_2, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_2 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_2 hcl.1,
        arm64_feature_monotonicity hle .v8_1 (hcl.2 .v8_1 (by simp [arm64Prereqs]))⟩
  | .v8_3, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_3 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_3 hcl.1,
        arm64_feature_monotonicity hle .v8_2 (hcl.2 .v8_2 (by simp [arm64Prereqs]))⟩
  | .v8_4, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_4 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_4 hcl.1,
        arm64_feature_monotonicity hle .v8_3 (hcl.2 .v8_3 (by simp [arm64Prereqs]))⟩
  | .v8_5, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_5 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_5 hcl.1,
        arm64_feature_monotonicity hle .v8_4 (hcl.2 .v8_4 (by simp [arm64Prereqs]))⟩
  | .v8_6, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_6 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_6 hcl.1,
        arm64_feature_monotonicity hle .v8_5 (hcl.2 .v8_5 (by simp [arm64Prereqs]))⟩
  | .v8_7, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_7 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_7 hcl.1,
        arm64_feature_monotonicity hle .v8_6 (hcl.2 .v8_6 (by simp [arm64Prereqs]))⟩
  | .v8_8, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_8 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_8 hcl.1,
        arm64_feature_monotonicity hle .v8_7 (hcl.2 .v8_7 (by simp [arm64Prereqs]))⟩
  | .v8_9, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v8_9 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v8_9 hcl.1,
        arm64_feature_monotonicity hle .v8_8 (hcl.2 .v8_8 (by simp [arm64Prereqs]))⟩
  | .v9_0, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v9_0 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v9_0 hcl.1,
        arm64_feature_monotonicity hle .v8_5 (hcl.2 .v8_5 (by simp [arm64Prereqs]))⟩
  | .v9_1, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v9_1 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v9_1 hcl.1,
        arm64_feature_monotonicity hle .v9_0 (hcl.2 .v9_0 (by simp [arm64Prereqs])),
        arm64_feature_monotonicity hle .v8_6 (hcl.2 .v8_6 (by simp [arm64Prereqs]))⟩
  | .v9_2, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v9_2 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v9_2 hcl.1,
        arm64_feature_monotonicity hle .v9_1 (hcl.2 .v9_1 (by simp [arm64Prereqs]))⟩
  | .v9_3, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v9_3 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v9_3 hcl.1,
        arm64_feature_monotonicity hle .v9_2 (hcl.2 .v9_2 (by simp [arm64Prereqs]))⟩
  | .v9_4, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v9_4 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v9_4 hcl.1,
        arm64_feature_monotonicity hle .v9_3 (hcl.2 .v9_3 (by simp [arm64Prereqs]))⟩
  | .v9_5, hsat => by
      have hcl := arm64Satisfied_prereq_closure .v9_5 a hsat
      rw [arm64Satisfied]; simp only [Bool.and_eq_true]
      exact ⟨arm64DirectFeatures_mono hle .v9_5 hcl.1,
        arm64_feature_monotonicity hle .v9_4 (hcl.2 .v9_4 (by simp [arm64Prereqs]))⟩
termination_by lvl _ => arm64TopoMeasure lvl
decreasing_by all_goals (simp [arm64TopoMeasure])

/-! ### Unified Variant Selection & SIGILL-Freedom -/

inductive Level where
  | amd64 : AMD64Level → Level
  | arm64 : ARM64Level → Level
  deriving DecidableEq, Repr

def Level.arch : Level → Arch
  | .amd64 _ => .amd64
  | .arm64 _ => .arm64

def Level.rank : Level → Nat
  | .amd64 l => amd64Rank l
  | .arm64 l => arm64Rank l

structure HostInfo where
  arch : Arch
  x86 : X86Features
  arm64 : ARM64Features
  isSkylakeXOrCascadeLake : Bool
  deriving DecidableEq, Repr

def hostSupports (host : HostInfo) (lvl : Level) : Bool :=
  match host.arch, lvl with
  | .amd64, .amd64 l => amd64Satisfied l host.x86
  | .arm64, .arm64 l => arm64Satisfied l host.arm64
  | _, _ => false

theorem feature_monotonicity_amd64 {h1 h2 : HostInfo} (harch : h1.arch = .amd64)
    (harch2 : h2.arch = .amd64) (hle : X86Features.le h1.x86 h2.x86)
    (lvl : Level) (h : hostSupports h1 lvl = true) :
    hostSupports h2 lvl = true := by
  cases lvl with
  | amd64 l =>
      simp only [hostSupports, harch, harch2] at h ⊢
      exact amd64_feature_monotonicity hle l h
  | arm64 l =>
      simp [hostSupports, harch] at h

theorem feature_monotonicity_arm64 {h1 h2 : HostInfo} (harch : h1.arch = .arm64)
    (harch2 : h2.arch = .arm64) (hle : ARM64Features.le h1.arm64 h2.arm64)
    (lvl : Level) (h : hostSupports h1 lvl = true) :
    hostSupports h2 lvl = true := by
  cases lvl with
  | amd64 l =>
      simp [hostSupports, harch] at h
  | arm64 l =>
      simp only [hostSupports, harch, harch2] at h ⊢
      exact arm64_feature_monotonicity hle l h

structure Policy where
  forceLevel : Option Level
  maxLevel : Option Level
  disabledVariants : List Level
  avx512DownclockProtection : Bool
  deriving DecidableEq, Repr

def computeEffectiveMaxRank (targetArch : Arch) (hostMaxRank : Nat) (host : HostInfo) (p : Policy) : Nat :=
  let afterMax :=
    match p.maxLevel with
    | some ml => if ml.arch == targetArch && ml.rank < hostMaxRank then ml.rank else hostMaxRank
    | none => hostMaxRank
  if p.avx512DownclockProtection && targetArch == .amd64 && hostMaxRank ≥ 4 && host.isSkylakeXOrCascadeLake then
    min afterMax 3
  else afterMax

def eligibleCandidates (targetArch : Arch) (host : HostInfo) (available : List Level) (p : Policy) : List Level :=
  let maxRank := computeEffectiveMaxRank targetArch 100 host p
  available.filter fun lvl =>
    (lvl.arch == targetArch) &&
    hostSupports host lvl &&
    (lvl.rank ≤ maxRank) &&
    (!p.disabledVariants.contains lvl)

def pickHighestRank : List Level → Option Level
  | [] => none
  | x :: xs =>
      match pickHighestRank xs with
      | none => some x
      | some best => if x.rank ≥ best.rank then some x else some best

def selectVariantForHost (targetArch : Arch) (host : HostInfo) (available : List Level) (p : Policy) : Option Level :=
  if host.arch != targetArch then none
  else
    match p.forceLevel with
    | some fl =>
        if fl.arch == targetArch && hostSupports host fl && available.contains fl then some fl
        else none
    | none => pickHighestRank (eligibleCandidates targetArch host available p)

theorem pickHighestRank_mem {xs : List Level} {sel : Level}
    (h : pickHighestRank xs = some sel) : sel ∈ xs := by
  induction xs generalizing sel with
  | nil => simp [pickHighestRank] at h
  | cons x xs ih =>
      simp only [pickHighestRank] at h
      cases htail : pickHighestRank xs with
      | none =>
          simp only [htail, Option.some.injEq] at h
          subst h; exact List.Mem.head _
      | some best =>
          simp only [htail] at h
          split at h
          · simp only [Option.some.injEq] at h; subst h; exact List.Mem.head _
          · simp only [Option.some.injEq] at h; subst h; exact List.Mem.tail _ (ih htail)

theorem pickHighestRank_optimal {xs : List Level} {sel : Level}
    (h : pickHighestRank xs = some sel) : ∀ c ∈ xs, c.rank ≤ sel.rank := by
  induction xs generalizing sel with
  | nil => simp [pickHighestRank] at h
  | cons x xs ih =>
      simp only [pickHighestRank] at h
      cases htail : pickHighestRank xs with
      | none =>
          have hnil : xs = [] := by
            cases xs with
            | nil => rfl
            | cons y ys =>
                simp only [pickHighestRank] at htail
                cases hys : pickHighestRank ys <;> simp only [hys] at htail
                · contradiction
                · split at htail <;> contradiction
          subst hnil
          simp only [htail, Option.some.injEq] at h
          subst h
          intro c hc
          simp at hc; subst hc; exact Nat.le_refl _
      | some best =>
          simp only [htail] at h
          have hbest := ih htail
          split at h
          · rename_i hge
            simp only [Option.some.injEq] at h; subst h
            intro c hc
            simp only [List.mem_cons] at hc
            rcases hc with rfl | hmem
            · exact Nat.le_refl _
            · exact Nat.le_trans (hbest c hmem) hge
          · rename_i hlt
            simp only [Option.some.injEq] at h; subst h
            intro c hc
            simp only [List.mem_cons] at hc
            rcases hc with rfl | hmem
            · omega
            · exact hbest c hmem

theorem select_sigill_free (targetArch : Arch) (host : HostInfo) (available : List Level)
    (p : Policy) (sel : Level)
    (h : selectVariantForHost targetArch host available p = some sel) :
    sel ∈ available ∧ hostSupports host sel = true ∧ sel.arch = targetArch := by
  simp only [selectVariantForHost] at h
  split at h
  · contradiction
  · rename_i harch
    have harchEq : host.arch = targetArch := by simpa using harch
    cases hforce : p.forceLevel with
    | some fl =>
        simp only [hforce] at h
        split at h
        · rename_i hcond
          simp only [Bool.and_eq_true, beq_iff_eq, List.contains_eq_mem, decide_eq_true_eq] at hcond
          simp only [Option.some.injEq] at h
          subst h
          exact ⟨hcond.2, hcond.1.2, hcond.1.1⟩
        · contradiction
    | none =>
        simp only [hforce] at h
        have hmem := pickHighestRank_mem h
        simp only [eligibleCandidates, List.mem_filter, Bool.and_eq_true, beq_iff_eq,
          decide_eq_true_eq] at hmem
        exact ⟨hmem.1, hmem.2.1.1.2, hmem.2.1.1.1⟩

theorem select_optimal_unforced (targetArch : Arch) (host : HostInfo) (available : List Level)
    (p : Policy) (hnone : p.forceLevel = none) (sel : Level)
    (h : selectVariantForHost targetArch host available p = some sel) :
    ∀ cand ∈ eligibleCandidates targetArch host available p, cand.rank ≤ sel.rank := by
  simp only [selectVariantForHost, hnone] at h
  split at h
  · contradiction
  · exact pickHighestRank_optimal h

end Microfat.Microarch
