//! Reversible x86-64 (BCJ+RIP) and ARM64 (BL/B/ADRP) filters matching `Microfat.ElfFilter` in Lean 4.

fn shift_imm32_le(dst: &mut [u8], pos: u32, encode: bool) {
    let v = u32::from_le_bytes([dst[0], dst[1], dst[2], dst[3]]);
    let next = if encode {
        v.wrapping_add(pos)
    } else {
        v.wrapping_sub(pos)
    };
    let bytes = next.to_le_bytes();
    dst[0] = bytes[0];
    dst[1] = bytes[1];
    dst[2] = bytes[2];
    dst[3] = bytes[3];
}

const fn is_x86_rel8(b: u8) -> bool {
    b == 0xE8 || b == 0xE9
}

const fn is_x86_rex(b: u8) -> bool {
    b == 0x48 || b == 0x4C
}

const fn is_x86_rip_op(b: u8) -> bool {
    b == 0x8D || b == 0x8B || b == 0x89
}

/// Applies one reversible x86-64 BCJ+RIP step at `buf[i..]` and returns the byte advance.
pub fn x86_step_at(buf: &mut [u8], i: usize, base_pos: u32, encode: bool) -> usize {
    let n = buf.len();
    if i >= n {
        return 0;
    }
    let rem = n - i;
    let b0 = buf[i];
    let pos = base_pos.wrapping_add(i as u32);
    if is_x86_rel8(b0) && rem >= 5 {
        shift_imm32_le(&mut buf[i + 1..i + 5], pos.wrapping_add(5), encode);
        return 5;
    }
    if b0 == 0x0F && rem >= 6 && (buf[i + 1] & 0xF0) == 0x80 {
        shift_imm32_le(&mut buf[i + 2..i + 6], pos.wrapping_add(6), encode);
        return 6;
    }
    if is_x86_rex(b0) && rem >= 7 && is_x86_rip_op(buf[i + 1]) && (buf[i + 2] & 0xC7) == 0x05 {
        shift_imm32_le(&mut buf[i + 3..i + 7], pos.wrapping_add(7), encode);
        return 7;
    }
    1
}

/// Applies the reversible x86-64 BCJ+RIP transform in place.
pub fn transform_x86_64(buf: &mut [u8], base_pos: u32, encode: bool) {
    let n = buf.len();
    let mut i = 0usize;
    while i < n {
        let step = x86_step_at(buf, i, base_pos, encode);
        if step == 0 {
            break;
        }
        i += step;
    }
}

/// Pure single-word ARM64 BL/B/ADRP step matching Lean `arm64StepWord`.
pub const fn arm64_step_word(encode: bool, pos: u32, insn: u32) -> u32 {
    let op26 = insn & 0xFC00_0000;
    if op26 == 0x9400_0000 || op26 == 0x1400_0000 {
        let imm26 = insn & 0x03FF_FFFF;
        let pc_words = pos >> 2;
        let next_imm = if encode {
            imm26.wrapping_add(pc_words) & 0x03FF_FFFF
        } else {
            imm26.wrapping_sub(pc_words) & 0x03FF_FFFF
        };
        return op26 | next_imm;
    }
    if (insn & 0x9F00_0000) == 0x9000_0000 {
        let immlo = (insn >> 29) & 0x3;
        let immhi = (insn >> 5) & 0x7_FFFF;
        let imm21 = (immhi << 2) | immlo;
        let pc_pages = pos >> 12;
        let next_imm21 = if encode {
            imm21.wrapping_add(pc_pages) & 0x001F_FFFF
        } else {
            imm21.wrapping_sub(pc_pages) & 0x001F_FFFF
        };
        let next_lo = next_imm21 & 0x3;
        let next_hi = (next_imm21 >> 2) & 0x7_FFFF;
        return (insn & 0x9F00_001F) | (next_lo << 29) | (next_hi << 5);
    }
    insn
}

/// Applies the reversible ARM64 BL/B/ADRP transform in place on 4-byte aligned instruction stream.
pub fn transform_arm64(buf: &mut [u8], base_pos: u32, encode: bool) {
    let limit = buf.len() - (buf.len() % 4);
    let mut i = 0usize;
    while i < limit {
        let pos = base_pos.wrapping_add(i as u32);
        let insn = u32::from_le_bytes([buf[i], buf[i + 1], buf[i + 2], buf[i + 3]]);
        let next = arm64_step_word(encode, pos, insn);
        buf[i..i + 4].copy_from_slice(&next.to_le_bytes());
        i += 4;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lean_conformance_x86() {
        let input = [
            0x55, 0xE8, 0x10, 0x00, 0x00, 0x00, 0x48, 0x8D, 0x05, 0x20, 0x01, 0x00, 0x00, 0x0F,
            0x84, 0x08, 0x00, 0x00, 0x00, 0x48, 0x8D, 0x00, 0xE8,
        ];
        let want_encoded = [
            85, 232, 22, 0, 0, 0, 72, 141, 5, 45, 1, 0, 0, 15, 132, 27, 0, 0, 0, 72, 141, 0, 232,
        ];
        let mut work = input;
        transform_x86_64(&mut work, 0, true);
        assert_eq!(work, want_encoded);
        transform_x86_64(&mut work, 0, false);
        assert_eq!(work, input);
    }

    #[test]
    fn lean_conformance_arm64() {
        let input = [
            0x01, 0x00, 0x00, 0x94, 0xFE, 0xFF, 0xFF, 0x17, 0x00, 0x00, 0x00, 0x90, 0x1F, 0x20,
            0x03, 0xD5, 0xAA, 0xBB,
        ];
        let want_encoded = [
            1, 4, 0, 148, 255, 3, 0, 20, 0, 0, 0, 176, 31, 32, 3, 213, 170, 187,
        ];
        let mut work = input;
        transform_arm64(&mut work, 0x1000, true);
        assert_eq!(work, want_encoded);
        transform_arm64(&mut work, 0x1000, false);
        assert_eq!(work, input);

        let mut tiny = [0xE8u8, 1, 2, 3, 4];
        assert_eq!(x86_step_at(&mut tiny, 99, 0, true), 0);
    }
}

#[cfg(kani)]
mod kani_proofs {
    use super::*;

    #[kani::proof]
    fn verify_arm64_step_word_bijection() {
        let pos: u32 = kani::any();
        let insn: u32 = kani::any();
        assert_eq!(
            arm64_step_word(false, pos, arm64_step_word(true, pos, insn)),
            insn
        );
        assert_eq!(
            arm64_step_word(true, pos, arm64_step_word(false, pos, insn)),
            insn
        );
    }

    #[kani::proof]
    #[kani::unwind(9)]
    fn verify_x86_step_bijection() {
        let orig: [u8; 7] = kani::any();
        let len: usize = kani::any();
        let pos: u32 = kani::any();
        kani::assume(len <= 7);

        let mut work = orig;
        let step_enc = x86_step_at(&mut work[..len], 0, pos, true);
        let step_dec = x86_step_at(&mut work[..len], 0, pos, false);
        assert_eq!(step_enc, step_dec);
        assert_eq!(work, orig);
    }

    #[kani::proof]
    #[kani::unwind(10)]
    fn verify_x86_transform_roundtrip_8() {
        let orig: [u8; 8] = kani::any();
        let len: usize = kani::any();
        let pos: u32 = kani::any();
        kani::assume(len <= 8);

        let mut work = orig;
        transform_x86_64(&mut work[..len], pos, true);
        transform_x86_64(&mut work[..len], pos, false);
        assert_eq!(work, orig);
    }

    #[kani::proof]
    #[kani::unwind(10)]
    fn verify_x86_chunk_local_split_roundtrip() {
        let orig: [u8; 8] = kani::any();
        let len: usize = kani::any();
        let split: usize = kani::any();
        let base_pos: u32 = kani::any();
        kani::assume(len <= 8);
        kani::assume(split <= len);

        let mut work = orig;
        transform_x86_64(&mut work[..split], base_pos, true);
        transform_x86_64(
            &mut work[split..len],
            base_pos.wrapping_add(split as u32),
            true,
        );
        transform_x86_64(&mut work[..split], base_pos, false);
        transform_x86_64(
            &mut work[split..len],
            base_pos.wrapping_add(split as u32),
            false,
        );
        assert_eq!(work, orig);
    }

    #[kani::proof]
    #[kani::unwind(10)]
    fn verify_arm64_chunk_local_split_roundtrip() {
        let orig: [u8; 8] = kani::any();
        let base_pos: u32 = kani::any();

        let mut mono = orig;
        transform_arm64(&mut mono, base_pos, true);

        let mut split_buf = orig;
        transform_arm64(&mut split_buf[..4], base_pos, true);
        transform_arm64(&mut split_buf[4..8], base_pos.wrapping_add(4), true);
        assert_eq!(split_buf, mono);

        transform_arm64(&mut split_buf[..4], base_pos, false);
        transform_arm64(&mut split_buf[4..8], base_pos.wrapping_add(4), false);
        assert_eq!(split_buf, orig);
    }
}
