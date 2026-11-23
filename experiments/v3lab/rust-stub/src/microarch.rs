//! AMD64 and ARM64 microarchitecture feature models matching `internal/microarch/microarch.go`.

pub const BIT_LAHF_SAHF: u32 = 1 << 0;
pub const BIT_CX16: u32 = 1 << 1;
pub const BIT_POPCNT: u32 = 1 << 2;
pub const BIT_SSE3: u32 = 1 << 3;
pub const BIT_SSSE3: u32 = 1 << 4;
pub const BIT_SSE41: u32 = 1 << 5;
pub const BIT_SSE42: u32 = 1 << 6;
pub const BIT_AVX: u32 = 1 << 7;
pub const BIT_AVX2: u32 = 1 << 8;
pub const BIT_BMI1: u32 = 1 << 9;
pub const BIT_BMI2: u32 = 1 << 10;
pub const BIT_FMA: u32 = 1 << 11;
pub const BIT_OSXSAVE: u32 = 1 << 12;
pub const BIT_F16C: u32 = 1 << 13;
pub const BIT_LZCNT: u32 = 1 << 14;
pub const BIT_MOVBE: u32 = 1 << 15;
pub const BIT_AVX512F: u32 = 1 << 16;
pub const BIT_AVX512BW: u32 = 1 << 17;
pub const BIT_AVX512CD: u32 = 1 << 18;
pub const BIT_AVX512DQ: u32 = 1 << 19;
pub const BIT_AVX512VL: u32 = 1 << 20;

pub const V2_MASK: u32 =
    BIT_LAHF_SAHF | BIT_CX16 | BIT_POPCNT | BIT_SSE3 | BIT_SSSE3 | BIT_SSE41 | BIT_SSE42;

pub const V3_ONLY_MASK: u32 =
    BIT_AVX | BIT_AVX2 | BIT_BMI1 | BIT_BMI2 | BIT_FMA | BIT_OSXSAVE | BIT_F16C | BIT_LZCNT | BIT_MOVBE;

pub const V4_ONLY_MASK: u32 =
    BIT_AVX512F | BIT_AVX512BW | BIT_AVX512CD | BIT_AVX512DQ | BIT_AVX512VL;

pub const ALL_FEATURE_MASK: u32 = V2_MASK | V3_ONLY_MASK | V4_ONLY_MASK;

// ARM64 feature bits matching `internal/microarch.ARM64Features` requirements across v8.0..v9.5.
pub const ARM_BIT_FP: u32 = 1 << 0;
pub const ARM_BIT_ASIMD: u32 = 1 << 1;
pub const ARM_BIT_ATOMICS: u32 = 1 << 2;
pub const ARM_BIT_CRC32: u32 = 1 << 3;
pub const ARM_BIT_FPHP: u32 = 1 << 4;
pub const ARM_BIT_ASIMDHP: u32 = 1 << 5;
pub const ARM_BIT_JSCVT: u32 = 1 << 6;
pub const ARM_BIT_FCMA: u32 = 1 << 7;
pub const ARM_BIT_LRCPC: u32 = 1 << 8;
pub const ARM_BIT_DCPOP: u32 = 1 << 9;
pub const ARM_BIT_ASIMDDP: u32 = 1 << 10;
pub const ARM_BIT_DIT: u32 = 1 << 11;
pub const ARM_BIT_I8MM: u32 = 1 << 12;
pub const ARM_BIT_BF16: u32 = 1 << 13;
pub const ARM_BIT_WFXT: u32 = 1 << 14;
pub const ARM_BIT_MOPS: u32 = 1 << 15;
pub const ARM_BIT_NMI: u32 = 1 << 16;
pub const ARM_BIT_HBC: u32 = 1 << 17;
pub const ARM_BIT_GCS: u32 = 1 << 18;
pub const ARM_BIT_THE: u32 = 1 << 19;
pub const ARM_BIT_SVE: u32 = 1 << 20;
pub const ARM_BIT_SVE2: u32 = 1 << 21;
pub const ARM_BIT_SME: u32 = 1 << 22;
pub const ARM_BIT_SME2: u32 = 1 << 23;

pub const ALL_ARM64_FEATURE_MASK: u32 = (1 << 24) - 1;

// Bit positions in the 16-bit ARM64 supported-level mask (ranks 80..=95).
pub const ARM_LVL_V8_0: u16 = 1 << 0;
pub const ARM_LVL_V8_1: u16 = 1 << 1;
pub const ARM_LVL_V8_2: u16 = 1 << 2;
pub const ARM_LVL_V8_3: u16 = 1 << 3;
pub const ARM_LVL_V8_4: u16 = 1 << 4;
pub const ARM_LVL_V8_5: u16 = 1 << 5;
pub const ARM_LVL_V8_6: u16 = 1 << 6;
pub const ARM_LVL_V8_7: u16 = 1 << 7;
pub const ARM_LVL_V8_8: u16 = 1 << 8;
pub const ARM_LVL_V8_9: u16 = 1 << 9;
pub const ARM_LVL_V9_0: u16 = 1 << 10;
pub const ARM_LVL_V9_1: u16 = 1 << 11;
pub const ARM_LVL_V9_2: u16 = 1 << 12;
pub const ARM_LVL_V9_3: u16 = 1 << 13;
pub const ARM_LVL_V9_4: u16 = 1 << 14;
pub const ARM_LVL_V9_5: u16 = 1 << 15;

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub enum TargetArch {
    Amd64,
    Arm64,
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub enum HostCaps {
    Amd64(u8),
    Arm64(u16),
}

impl HostCaps {
    pub const fn target_arch(self) -> TargetArch {
        match self {
            HostCaps::Amd64(_) => TargetArch::Amd64,
            HostCaps::Arm64(_) => TargetArch::Arm64,
        }
    }

    pub const fn expected_arch_bytes(self) -> &'static [u8] {
        match self {
            HostCaps::Amd64(_) => b"amd64",
            HostCaps::Arm64(_) => b"arm64",
        }
    }
}

/// 21-bit feature mask identical in semantics to `internal/microarch.X86Features`.
#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct X86Features(pub u32);

impl X86Features {
    pub const fn new(bits: u32) -> Self {
        Self(bits & ALL_FEATURE_MASK)
    }

    pub const fn has_all(self, required: u32) -> bool {
        (self.0 & required) == required
    }
}

/// 24-bit feature mask matching ARM64 userspace capability requirements.
#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct Arm64Features(pub u32);

impl Arm64Features {
    pub const fn new(bits: u32) -> Self {
        Self(bits & ALL_ARM64_FEATURE_MASK)
    }

    pub const fn has_all(self, required: u32) -> bool {
        (self.0 & required) == required
    }
}

/// Returns host AMD64 rank (1..=4) matching `internal/microarch.EvaluateAMD64`.
pub const fn evaluate_amd64_rank(f: X86Features) -> u8 {
    if !f.has_all(V2_MASK) {
        return 1;
    }
    if !f.has_all(V3_ONLY_MASK) {
        return 2;
    }
    if !f.has_all(V4_ONLY_MASK) {
        return 3;
    }
    4
}

/// Evaluates the non-linear ARM64 v8.x/v9.x prerequisite DAG into a 16-bit supported-level bitmask.
pub const fn evaluate_arm64_mask(f: Arm64Features) -> u16 {
    let mut m = 0u16;
    let v8_0 = f.has_all(ARM_BIT_FP | ARM_BIT_ASIMD);
    if v8_0 {
        m |= ARM_LVL_V8_0;
    }
    let v8_1 = v8_0 && f.has_all(ARM_BIT_ATOMICS | ARM_BIT_CRC32);
    if v8_1 {
        m |= ARM_LVL_V8_1;
    }
    let v8_2 = v8_1 && f.has_all(ARM_BIT_FPHP | ARM_BIT_ASIMDHP);
    if v8_2 {
        m |= ARM_LVL_V8_2;
    }
    let v8_3 = v8_2 && f.has_all(ARM_BIT_JSCVT | ARM_BIT_FCMA | ARM_BIT_LRCPC);
    if v8_3 {
        m |= ARM_LVL_V8_3;
    }
    let v8_4 = v8_3 && f.has_all(ARM_BIT_DCPOP | ARM_BIT_ASIMDDP);
    if v8_4 {
        m |= ARM_LVL_V8_4;
    }
    let v8_5 = v8_4 && f.has_all(ARM_BIT_DIT);
    if v8_5 {
        m |= ARM_LVL_V8_5;
    }
    let v8_6 = v8_5 && f.has_all(ARM_BIT_I8MM | ARM_BIT_BF16);
    if v8_6 {
        m |= ARM_LVL_V8_6;
    }
    let v8_7 = v8_6 && f.has_all(ARM_BIT_WFXT);
    if v8_7 {
        m |= ARM_LVL_V8_7;
    }
    let v8_8 = v8_7 && f.has_all(ARM_BIT_MOPS | ARM_BIT_NMI | ARM_BIT_HBC);
    if v8_8 {
        m |= ARM_LVL_V8_8;
    }
    let v8_9 = v8_8 && f.has_all(ARM_BIT_GCS | ARM_BIT_THE);
    if v8_9 {
        m |= ARM_LVL_V8_9;
    }
    let v9_0 = v8_5 && f.has_all(ARM_BIT_SVE);
    if v9_0 {
        m |= ARM_LVL_V9_0;
    }
    let v9_1 = v9_0 && v8_6;
    if v9_1 {
        m |= ARM_LVL_V9_1;
    }
    let v9_2 = v9_1 && f.has_all(ARM_BIT_SVE2);
    if v9_2 {
        m |= ARM_LVL_V9_2;
    }
    let v9_3 = v9_2 && f.has_all(ARM_BIT_SME);
    if v9_3 {
        m |= ARM_LVL_V9_3;
    }
    let v9_4 = v9_3;
    if v9_4 {
        m |= ARM_LVL_V9_4;
    }
    let v9_5 = v9_4 && f.has_all(ARM_BIT_SME2);
    if v9_5 {
        m |= ARM_LVL_V9_5;
    }
    m
}

/// Converts numeric rank to canonical Go variant label.
pub const fn amd64_level_str(rank: u8) -> Option<&'static str> {
    match rank {
        1 => Some("v1"),
        2 => Some("v2"),
        3 => Some("v3"),
        4 => Some("v4"),
        _ => None,
    }
}

/// Parses an AMD64 level string (`"v1"`..`"v4"`) into rank `1..=4`.
pub const fn parse_amd64_rank(level: &[u8]) -> u8 {
    match level {
        b"v1" => 1,
        b"v2" => 2,
        b"v3" => 3,
        b"v4" => 4,
        _ => 0,
    }
}

/// Parses an ARM64 level string (`"v8.0"`..`"v9.5"`) into rank `80..=95`.
pub const fn parse_arm64_rank(level: &[u8]) -> u8 {
    match level {
        b"v8.0" => 80,
        b"v8.1" => 81,
        b"v8.2" => 82,
        b"v8.3" => 83,
        b"v8.4" => 84,
        b"v8.5" => 85,
        b"v8.6" => 86,
        b"v8.7" => 87,
        b"v8.8" => 88,
        b"v8.9" => 89,
        b"v9.0" => 90,
        b"v9.1" => 91,
        b"v9.2" => 92,
        b"v9.3" => 93,
        b"v9.4" => 94,
        b"v9.5" => 95,
        _ => 0,
    }
}

/// Parses a level string according to target architecture.
pub const fn parse_level_rank(arch: TargetArch, level: &[u8]) -> u8 {
    match arch {
        TargetArch::Amd64 => parse_amd64_rank(level),
        TargetArch::Arm64 => parse_arm64_rank(level),
    }
}

/// Returns true iff `candidate_rank` can safely execute on a host with `host_rank` (AMD64).
pub const fn is_compatible_rank(host_rank: u8, candidate_rank: u8) -> bool {
    candidate_rank >= 1 && candidate_rank <= 4 && candidate_rank <= host_rank
}

/// Returns true iff `candidate_rank` is supported by the host capabilities (AMD64 or ARM64 DAG).
pub const fn is_compatible_level(host: HostCaps, candidate_rank: u8) -> bool {
    match host {
        HostCaps::Amd64(r) => is_compatible_rank(r, candidate_rank),
        HostCaps::Arm64(mask) => {
            candidate_rank >= 80
                && candidate_rank <= 95
                && ((mask >> (candidate_rank - 80)) & 1) != 0
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn evaluates_all_tiers_and_boundaries() {
        assert_eq!(evaluate_amd64_rank(X86Features::new(0)), 1);
        assert_eq!(evaluate_amd64_rank(X86Features::new(V2_MASK)), 2);
        assert_eq!(
            evaluate_amd64_rank(X86Features::new(V2_MASK & !BIT_CX16)),
            1
        );
        assert_eq!(
            evaluate_amd64_rank(X86Features::new(V2_MASK | V3_ONLY_MASK)),
            3
        );
        assert_eq!(
            evaluate_amd64_rank(X86Features::new((V2_MASK | V3_ONLY_MASK) & !BIT_OSXSAVE)),
            2
        );
        assert_eq!(
            evaluate_amd64_rank(X86Features::new(V2_MASK | V4_ONLY_MASK)),
            2
        );
        assert_eq!(evaluate_amd64_rank(X86Features::new(ALL_FEATURE_MASK)), 4);
    }

    #[test]
    fn evaluates_arm64_non_linear_dag() {
        let base85 = ARM_BIT_FP
            | ARM_BIT_ASIMD
            | ARM_BIT_ATOMICS
            | ARM_BIT_CRC32
            | ARM_BIT_FPHP
            | ARM_BIT_ASIMDHP
            | ARM_BIT_JSCVT
            | ARM_BIT_FCMA
            | ARM_BIT_LRCPC
            | ARM_BIT_DCPOP
            | ARM_BIT_ASIMDDP
            | ARM_BIT_DIT;

        // v9.0 (v8.5 + SVE) without v8.6 must select v9.0 and v8.5, NOT v8.6 or v9.1.
        let m90 = evaluate_arm64_mask(Arm64Features::new(base85 | ARM_BIT_SVE));
        assert_ne!(m90 & ARM_LVL_V8_5, 0);
        assert_ne!(m90 & ARM_LVL_V9_0, 0);
        assert_eq!(m90 & ARM_LVL_V8_6, 0);
        assert_eq!(m90 & ARM_LVL_V9_1, 0);

        // Linear comparison mistake check: v9.0 host supports v8.5..v8.0, not v8.6.
        let caps = HostCaps::Arm64(m90);
        assert!(is_compatible_level(caps, parse_arm64_rank(b"v9.0")));
        assert!(is_compatible_level(caps, parse_arm64_rank(b"v8.5")));
        assert!(!is_compatible_level(caps, parse_arm64_rank(b"v8.6")));

        // Without NMI/THE, v8.8/v8.9 remain unreachable while v9.5 succeeds when v9 branch holds.
        let userspace_all = ALL_ARM64_FEATURE_MASK & !(ARM_BIT_NMI | ARM_BIT_THE);
        let mall = evaluate_arm64_mask(Arm64Features::new(userspace_all));
        assert_ne!(mall & ARM_LVL_V8_7, 0);
        assert_eq!(mall & ARM_LVL_V8_8, 0);
        assert_eq!(mall & ARM_LVL_V8_9, 0);
        assert_ne!(mall & ARM_LVL_V9_5, 0);
    }
}

#[cfg(kani)]
mod kani_proofs {
    use super::*;

    #[kani::proof]
    fn verify_evaluate_amd64_exhaustive() {
        let bits1: u32 = kani::any();
        let bits2: u32 = kani::any();
        kani::assume(bits1 <= ALL_FEATURE_MASK);
        kani::assume(bits2 <= ALL_FEATURE_MASK);

        let f1 = X86Features::new(bits1);
        let r1 = evaluate_amd64_rank(f1);

        // Bounds & SIGILL freedom: rank never claims capabilities absent in f1.
        assert!((1..=4).contains(&r1));
        if r1 >= 2 {
            assert!(f1.has_all(V2_MASK));
        }
        if r1 >= 3 {
            assert!(f1.has_all(V2_MASK | V3_ONLY_MASK));
        }
        if r1 == 4 {
            assert!(f1.has_all(ALL_FEATURE_MASK));
        }

        // Optimality: if prerequisite mask holds, evaluate returns at least that tier.
        if f1.has_all(ALL_FEATURE_MASK) {
            assert_eq!(r1, 4);
        } else if f1.has_all(V2_MASK | V3_ONLY_MASK) {
            assert_eq!(r1, 3);
        } else if f1.has_all(V2_MASK) {
            assert_eq!(r1, 2);
        } else {
            assert_eq!(r1, 1);
        }

        // Monotonicity across any extension of feature bits.
        if (f1.0 & !bits2) == 0 {
            let r2 = evaluate_amd64_rank(X86Features::new(bits2));
            assert!(r1 <= r2);
        }
    }

    #[kani::proof]
    fn verify_evaluate_arm64_dag_exhaustive() {
        let b1: u32 = kani::any();
        let b2: u32 = kani::any();
        kani::assume(b1 <= ALL_ARM64_FEATURE_MASK);
        kani::assume(b2 <= ALL_ARM64_FEATURE_MASK);

        let f1 = Arm64Features::new(b1);
        let m1 = evaluate_arm64_mask(f1);

        // SIGILL freedom & DAG prerequisites across every tier v8.0..v9.5.
        if (m1 & ARM_LVL_V8_0) != 0 {
            assert!(f1.has_all(ARM_BIT_FP | ARM_BIT_ASIMD));
        }
        if (m1 & ARM_LVL_V8_1) != 0 {
            assert!((m1 & ARM_LVL_V8_0) != 0 && f1.has_all(ARM_BIT_ATOMICS | ARM_BIT_CRC32));
        }
        if (m1 & ARM_LVL_V8_2) != 0 {
            assert!((m1 & ARM_LVL_V8_1) != 0 && f1.has_all(ARM_BIT_FPHP | ARM_BIT_ASIMDHP));
        }
        if (m1 & ARM_LVL_V8_3) != 0 {
            assert!(
                (m1 & ARM_LVL_V8_2) != 0
                    && f1.has_all(ARM_BIT_JSCVT | ARM_BIT_FCMA | ARM_BIT_LRCPC)
            );
        }
        if (m1 & ARM_LVL_V8_4) != 0 {
            assert!((m1 & ARM_LVL_V8_3) != 0 && f1.has_all(ARM_BIT_DCPOP | ARM_BIT_ASIMDDP));
        }
        if (m1 & ARM_LVL_V8_5) != 0 {
            assert!((m1 & ARM_LVL_V8_4) != 0 && f1.has_all(ARM_BIT_DIT));
        }
        if (m1 & ARM_LVL_V8_6) != 0 {
            assert!((m1 & ARM_LVL_V8_5) != 0 && f1.has_all(ARM_BIT_I8MM | ARM_BIT_BF16));
        }
        if (m1 & ARM_LVL_V8_7) != 0 {
            assert!((m1 & ARM_LVL_V8_6) != 0 && f1.has_all(ARM_BIT_WFXT));
        }
        if (m1 & ARM_LVL_V8_8) != 0 {
            assert!(
                (m1 & ARM_LVL_V8_7) != 0 && f1.has_all(ARM_BIT_MOPS | ARM_BIT_NMI | ARM_BIT_HBC)
            );
        }
        if (m1 & ARM_LVL_V8_9) != 0 {
            assert!((m1 & ARM_LVL_V8_8) != 0 && f1.has_all(ARM_BIT_GCS | ARM_BIT_THE));
        }
        if (m1 & ARM_LVL_V9_0) != 0 {
            assert!((m1 & ARM_LVL_V8_5) != 0 && f1.has_all(ARM_BIT_SVE));
        }
        if (m1 & ARM_LVL_V9_1) != 0 {
            assert!((m1 & ARM_LVL_V9_0) != 0 && (m1 & ARM_LVL_V8_6) != 0);
        }
        if (m1 & ARM_LVL_V9_2) != 0 {
            assert!((m1 & ARM_LVL_V9_1) != 0 && f1.has_all(ARM_BIT_SVE2));
        }
        if (m1 & ARM_LVL_V9_3) != 0 {
            assert!((m1 & ARM_LVL_V9_2) != 0 && f1.has_all(ARM_BIT_SME));
        }
        if (m1 & ARM_LVL_V9_4) != 0 {
            assert!((m1 & ARM_LVL_V9_3) != 0);
        }
        if (m1 & ARM_LVL_V9_5) != 0 {
            assert!((m1 & ARM_LVL_V9_4) != 0 && f1.has_all(ARM_BIT_SME2));
        }

        // Userspace NMI/THE safety invariant.
        if !f1.has_all(ARM_BIT_NMI) {
            assert_eq!(m1 & (ARM_LVL_V8_8 | ARM_LVL_V8_9), 0);
        }
        if !f1.has_all(ARM_BIT_THE) {
            assert_eq!(m1 & ARM_LVL_V8_9, 0);
        }

        // Monotonicity across any feature bit superset.
        if (f1.0 & !b2) == 0 {
            let m2 = evaluate_arm64_mask(Arm64Features::new(b2));
            assert_eq!(m1 & !m2, 0);
        }
    }
}
