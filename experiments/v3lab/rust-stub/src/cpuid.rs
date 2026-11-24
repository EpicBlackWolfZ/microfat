//! Native host capability probing for Linux x86-64 (CPUID + XGETBV) and ARM64 (/proc/self/auxv).

#[cfg(target_arch = "x86_64")]
use microfat_stub_core::microarch::{
    evaluate_amd64_rank, X86Features, BIT_AVX, BIT_AVX2, BIT_AVX512BW, BIT_AVX512CD, BIT_AVX512DQ,
    BIT_AVX512F, BIT_AVX512VL, BIT_BMI1, BIT_BMI2, BIT_CX16, BIT_F16C, BIT_FMA, BIT_LAHF_SAHF,
    BIT_LZCNT, BIT_MOVBE, BIT_OSXSAVE, BIT_POPCNT, BIT_SSE3, BIT_SSE41, BIT_SSE42, BIT_SSSE3,
};
#[cfg(any(target_arch = "aarch64", test))]
use microfat_stub_core::microarch::{
    evaluate_arm64_mask, Arm64Features, ARM_BIT_ASIMD, ARM_BIT_ASIMDDP, ARM_BIT_ASIMDHP,
    ARM_BIT_ATOMICS, ARM_BIT_BF16, ARM_BIT_CRC32, ARM_BIT_DCPOP, ARM_BIT_DIT, ARM_BIT_FCMA,
    ARM_BIT_FP, ARM_BIT_FPHP, ARM_BIT_GCS, ARM_BIT_HBC, ARM_BIT_I8MM, ARM_BIT_JSCVT,
    ARM_BIT_LRCPC, ARM_BIT_MOPS, ARM_BIT_SME, ARM_BIT_SME2, ARM_BIT_SVE, ARM_BIT_SVE2,
    ARM_BIT_WFXT,
};
#[cfg(target_arch = "aarch64")]
use microfat_stub_core::microarch::ARM_LVL_V8_0;
use microfat_stub_core::microarch::HostCaps;

#[cfg(target_arch = "x86_64")]
#[cfg_attr(test, allow(dead_code))]
#[inline]
fn cpuid_count(leaf: u32, subleaf: u32) -> (u32, u32, u32, u32) {
    let res = core::arch::x86_64::__cpuid_count(leaf, subleaf);
    (res.eax, res.ebx, res.ecx, res.edx)
}

#[cfg(target_arch = "x86_64")]
#[cfg_attr(test, allow(dead_code))]
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
#[cfg_attr(test, allow(dead_code))]
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

/// Reports whether the host x86-64 processor is an Intel Skylake-X / Cascade Lake / Cooper Lake Xeon
/// (Family 6, Model 85) subject to AVX-512 frequency downclocking.
#[cfg(target_arch = "x86_64")]
#[cfg_attr(test, allow(dead_code))]
pub fn is_skylake_x_or_cascade_lake() -> bool {
    let (max_basic, ebx, ecx, edx) = cpuid_count(0, 0);
    if max_basic < 1 {
        return false;
    }
    // "GenuineIntel": EBX="Genu", EDX="ineI", ECX="ntel"
    if ebx != 0x756e_6547 || edx != 0x4965_6e69 || ecx != 0x6c65_746e {
        return false;
    }
    let (eax, _, _, _) = cpuid_count(1, 0);
    let base_family = (eax >> 8) & 0x0f;
    let ext_family = (eax >> 20) & 0xff;
    let family = if base_family == 0x0f {
        base_family + ext_family
    } else {
        base_family
    };
    let base_model = (eax >> 4) & 0x0f;
    let ext_model = (eax >> 16) & 0x0f;
    let model = if base_family == 6 || base_family == 0x0f {
        base_model | (ext_model << 4)
    } else {
        base_model
    };
    family == 6 && model == 85
}

#[cfg(not(target_arch = "x86_64"))]
#[cfg_attr(test, allow(dead_code))]
pub fn is_skylake_x_or_cascade_lake() -> bool {
    false
}

#[cfg(any(target_arch = "aarch64", test))]
pub fn arm64_features_from_hwcap(hwcap: u64, hwcap2: u64) -> Arm64Features {
    let mut bits = 0u32;
    if (hwcap & (1 << 0)) != 0 {
        bits |= ARM_BIT_FP;
    }
    if (hwcap & (1 << 1)) != 0 {
        bits |= ARM_BIT_ASIMD;
    }
    if (hwcap & (1 << 7)) != 0 {
        bits |= ARM_BIT_CRC32;
    }
    if (hwcap & (1 << 8)) != 0 {
        bits |= ARM_BIT_ATOMICS;
    }
    if (hwcap & (1 << 9)) != 0 {
        bits |= ARM_BIT_FPHP;
    }
    if (hwcap & (1 << 10)) != 0 {
        bits |= ARM_BIT_ASIMDHP;
    }
    if (hwcap & (1 << 13)) != 0 {
        bits |= ARM_BIT_JSCVT;
    }
    if (hwcap & (1 << 14)) != 0 {
        bits |= ARM_BIT_FCMA;
    }
    if (hwcap & (1 << 15)) != 0 {
        bits |= ARM_BIT_LRCPC;
    }
    if (hwcap & (1 << 16)) != 0 {
        bits |= ARM_BIT_DCPOP;
    }
    if (hwcap & (1 << 20)) != 0 {
        bits |= ARM_BIT_ASIMDDP;
    }
    if (hwcap & (1 << 22)) != 0 {
        bits |= ARM_BIT_SVE;
    }
    if (hwcap & (1 << 24)) != 0 {
        bits |= ARM_BIT_DIT;
    }
    if (hwcap & (1 << 32)) != 0 {
        bits |= ARM_BIT_GCS;
    }

    if (hwcap2 & (1 << 1)) != 0 {
        bits |= ARM_BIT_SVE2;
    }
    if (hwcap2 & (1 << 13)) != 0 {
        bits |= ARM_BIT_I8MM;
    }
    if (hwcap2 & (1 << 14)) != 0 {
        bits |= ARM_BIT_BF16;
    }
    if (hwcap2 & (1 << 23)) != 0 {
        bits |= ARM_BIT_SME;
    }
    if (hwcap2 & (1 << 31)) != 0 {
        bits |= ARM_BIT_WFXT;
    }
    if (hwcap2 & (1 << 37)) != 0 {
        bits |= ARM_BIT_SME2;
    }
    if (hwcap2 & (1 << 43)) != 0 {
        bits |= ARM_BIT_MOPS;
    }
    if (hwcap2 & (1 << 44)) != 0 {
        bits |= ARM_BIT_HBC;
    }
    // Deliberately leave ARM_BIT_NMI and ARM_BIT_THE unset (no safe Linux userspace ABI probe).
    Arm64Features::new(bits)
}

#[cfg(any(target_arch = "aarch64", test))]
pub fn parse_arm64_auxv_features(auxv: &[u8]) -> Arm64Features {
    const AT_HWCAP: u64 = 16;
    const AT_HWCAP2: u64 = 26;

    let mut hwcap = 0u64;
    let mut hwcap2 = 0u64;
    let mut off = 0usize;
    while off + 16 <= auxv.len() {
        let key = u64::from_le_bytes(auxv[off..off + 8].try_into().unwrap());
        let val = u64::from_le_bytes(auxv[off + 8..off + 16].try_into().unwrap());
        if key == 0 && val == 0 {
            break;
        }
        match key {
            AT_HWCAP => hwcap = val,
            AT_HWCAP2 => hwcap2 = val,
            _ => {}
        }
        off += 16;
    }
    arm64_features_from_hwcap(hwcap, hwcap2)
}

#[cfg(target_arch = "aarch64")]
fn read_proc_auxv() -> alloc::vec::Vec<u8> {
    let mut out = alloc::vec::Vec::new();
    // SAFETY: static NUL-terminated path passed to read-only open.
    let fd = unsafe {
        libc::open(
            c"/proc/self/auxv".as_ptr(),
            libc::O_RDONLY | libc::O_CLOEXEC,
        )
    };
    if fd < 0 {
        return out;
    }
    let mut buf = [0u8; 512];
    while out.len() < 16 * 1024 {
        // SAFETY: `fd` is valid and `buf` is stack-allocated writable memory.
        let n = unsafe { libc::read(fd, buf.as_mut_ptr().cast(), buf.len()) };
        if n < 0 {
            // SAFETY: thread-local errno pointer on Linux.
            let err = unsafe { *libc::__errno_location() };
            if err == libc::EINTR {
                continue;
            }
            break;
        }
        if n == 0 {
            break;
        }
        out.extend_from_slice(&buf[..n as usize]);
    }
    // SAFETY: `fd` was returned by `libc::open`.
    unsafe {
        let _ = libc::close(fd);
    }
    out
}

#[cfg_attr(test, allow(dead_code))]
pub fn detect_host_caps() -> HostCaps {
    #[cfg(target_arch = "x86_64")]
    {
        HostCaps::Amd64(evaluate_amd64_rank(probe_x86_features()))
    }
    #[cfg(target_arch = "aarch64")]
    {
        // SAFETY: `libc::getauxval` takes integer constants and has no pointer preconditions.
        let hwcap = unsafe { libc::getauxval(libc::AT_HWCAP) } as u64;
        // SAFETY: `libc::getauxval` takes integer constants and has no pointer preconditions.
        let hwcap2 = unsafe { libc::getauxval(libc::AT_HWCAP2) } as u64;
        let feat = if hwcap != 0 || hwcap2 != 0 {
            arm64_features_from_hwcap(hwcap, hwcap2)
        } else {
            let auxv = read_proc_auxv();
            parse_arm64_auxv_features(&auxv)
        };
        HostCaps::Arm64(evaluate_arm64_mask(feat) | ARM_LVL_V8_0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use microfat_stub_core::microarch::{
        ARM_LVL_V8_0, ARM_LVL_V8_2, ARM_LVL_V8_5, ARM_LVL_V8_8, ARM_LVL_V9_0, ARM_LVL_V9_5,
    };

    #[test]
    fn parses_synthetic_arm64_auxv() {
        let hwcap: u64 = (1 << 0)
            | (1 << 1)
            | (1 << 7)
            | (1 << 8)
            | (1 << 9)
            | (1 << 10)
            | (1 << 13)
            | (1 << 14)
            | (1 << 15)
            | (1 << 16)
            | (1 << 20)
            | (1 << 22)
            | (1 << 24)
            | (1 << 32);
        let hwcap2: u64 =
            (1 << 1) | (1 << 13) | (1 << 14) | (1 << 23) | (1 << 31) | (1 << 37) | (1 << 43) | (1 << 44);

        let mut buf = Vec::new();
        buf.extend_from_slice(&16u64.to_le_bytes());
        buf.extend_from_slice(&hwcap.to_le_bytes());
        buf.extend_from_slice(&26u64.to_le_bytes());
        buf.extend_from_slice(&hwcap2.to_le_bytes());
        buf.extend_from_slice(&0u64.to_le_bytes());
        buf.extend_from_slice(&0u64.to_le_bytes());

        let mask = evaluate_arm64_mask(parse_arm64_auxv_features(&buf));
        assert_ne!(mask & ARM_LVL_V8_0, 0);
        assert_ne!(mask & ARM_LVL_V8_2, 0);
        assert_ne!(mask & ARM_LVL_V8_5, 0);
        assert_ne!(mask & ARM_LVL_V9_0, 0);
        assert_ne!(mask & ARM_LVL_V9_5, 0);
        assert_eq!(mask & ARM_LVL_V8_8, 0);
    }
}
