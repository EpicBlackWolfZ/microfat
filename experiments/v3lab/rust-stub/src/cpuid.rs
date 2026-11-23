//! Native x86-64 CPUID + XGETBV probe matching `internal/microarch/microarch.go`.

use microfat_stub_core::microarch::{
    evaluate_amd64_rank, X86Features, BIT_AVX, BIT_AVX2, BIT_AVX512BW, BIT_AVX512CD, BIT_AVX512DQ,
    BIT_AVX512F, BIT_AVX512VL, BIT_BMI1, BIT_BMI2, BIT_CX16, BIT_F16C, BIT_FMA, BIT_LAHF_SAHF,
    BIT_LZCNT, BIT_MOVBE, BIT_OSXSAVE, BIT_POPCNT, BIT_SSE3, BIT_SSE41, BIT_SSE42, BIT_SSSE3,
};

#[cfg(target_arch = "x86_64")]
#[inline]
fn cpuid_count(leaf: u32, subleaf: u32) -> (u32, u32, u32, u32) {
    let res = core::arch::x86_64::__cpuid_count(leaf, subleaf);
    (res.eax, res.ebx, res.ecx, res.edx)
}

#[cfg(target_arch = "x86_64")]
#[inline]
unsafe fn read_xcr0() -> u64 {
    let eax: u32;
    let edx: u32;
    // SAFETY: Caller verifies CPUID leaf 1 ECX bit 27 (OSXSAVE) before invoking XGETBV on ECX=0.
    unsafe {
        core::arch::asm!(
            "xgetbv",
            in("ecx") 0u32,
            out("eax") eax,
            out("edx") edx,
            options(nomem, nostack, preserves_flags)
        );
    }
    ((edx as u64) << 32) | (eax as u64)
}

/// Probes host x86-64 CPUID leaves and OS XSAVE state and returns the 21-bit feature set.
#[cfg(target_arch = "x86_64")]
pub fn probe_x86_features() -> X86Features {
    let mut bits = 0u32;
    let (max_basic, _, _, _) = cpuid_count(0, 0);

    let mut os_ymm = false;
    let mut os_zmm = false;

    if max_basic >= 1 {
        let (_, _, ecx, _) = cpuid_count(1, 0);
        if (ecx & (1 << 0)) != 0 {
            bits |= BIT_SSE3;
        }
        if (ecx & (1 << 9)) != 0 {
            bits |= BIT_SSSE3;
        }
        if (ecx & (1 << 13)) != 0 {
            bits |= BIT_CX16;
        }
        if (ecx & (1 << 19)) != 0 {
            bits |= BIT_SSE41;
        }
        if (ecx & (1 << 20)) != 0 {
            bits |= BIT_SSE42;
        }
        if (ecx & (1 << 22)) != 0 {
            bits |= BIT_MOVBE;
        }
        if (ecx & (1 << 23)) != 0 {
            bits |= BIT_POPCNT;
        }
        if (ecx & (1 << 29)) != 0 {
            bits |= BIT_F16C;
        }

        let osxsave = (ecx & (1 << 27)) != 0;
        if osxsave {
            bits |= BIT_OSXSAVE;
            // SAFETY: OSXSAVE bit in CPUID leaf 1 ECX guarantees OS-enabled XGETBV for ECX=0.
            let xcr0 = unsafe { read_xcr0() };
            os_ymm = (xcr0 & 0x6) == 0x6;
            os_zmm = (xcr0 & 0xE6) == 0xE6;
        }
        if os_ymm && (ecx & (1 << 28)) != 0 {
            bits |= BIT_AVX;
        }
        if os_ymm && (ecx & (1 << 12)) != 0 {
            bits |= BIT_FMA;
        }
    }

    if max_basic >= 7 {
        let (_, ebx, _, _) = cpuid_count(7, 0);
        if (ebx & (1 << 3)) != 0 {
            bits |= BIT_BMI1;
        }
        if os_ymm && (ebx & (1 << 5)) != 0 {
            bits |= BIT_AVX2;
        }
        if (ebx & (1 << 8)) != 0 {
            bits |= BIT_BMI2;
        }
        if os_zmm {
            if (ebx & (1 << 16)) != 0 {
                bits |= BIT_AVX512F;
            }
            if (ebx & (1 << 17)) != 0 {
                bits |= BIT_AVX512DQ;
            }
            if (ebx & (1 << 28)) != 0 {
                bits |= BIT_AVX512CD;
            }
            if (ebx & (1 << 30)) != 0 {
                bits |= BIT_AVX512BW;
            }
            if (ebx & (1 << 31)) != 0 {
                bits |= BIT_AVX512VL;
            }
        }
    }

    let (max_ext, _, _, _) = cpuid_count(0x8000_0000, 0);
    if max_ext >= 0x8000_0001 {
        let (_, _, ecx, _) = cpuid_count(0x8000_0001, 0);
        if (ecx & (1 << 0)) != 0 {
            bits |= BIT_LAHF_SAHF;
        }
        if (ecx & (1 << 5)) != 0 {
            bits |= BIT_LZCNT;
        }
    }

    X86Features::new(bits)
}

#[cfg(not(target_arch = "x86_64"))]
pub fn probe_x86_features() -> X86Features {
    X86Features::new(0)
}

pub fn detect_host_amd64_rank() -> u8 {
    evaluate_amd64_rank(probe_x86_features())
}
