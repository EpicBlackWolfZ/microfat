//! AMD64 microarchitecture feature model matching Go `internal/microarch.EvaluateAMD64`.

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

/// Returns true iff `candidate_rank` can safely execute on a host with `host_rank`.
pub const fn is_compatible_rank(host_rank: u8, candidate_rank: u8) -> bool {
    candidate_rank >= 1 && candidate_rank <= 4 && candidate_rank <= host_rank
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
        // AVX-512 bits without v3 bits must still fall back safely to v2 (missing v3).
        assert_eq!(
            evaluate_amd64_rank(X86Features::new(V2_MASK | V4_ONLY_MASK)),
            2
        );
        assert_eq!(evaluate_amd64_rank(X86Features::new(ALL_FEATURE_MASK)), 4);
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
}
