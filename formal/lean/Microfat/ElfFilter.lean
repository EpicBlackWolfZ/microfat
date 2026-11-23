/-
Reversible ELF machine-code filters for Format v3 compression research (`experiments/v3lab/compress/`):
1. ARM64 32-bit instruction filter (`BL`, `B`, `ADRP`) over 4-byte little-endian streams.
2. x86-64 variable-stride `BCJ+RIP` byte-stream filter (`E8 CALL`, `E9 JMP`, `0F 8x Jcc`,
   `48/4C {8D,8B,89} ModRM(rip)`).

All proofs are pure Lean kernel proofs (zero `sorry`, zero `bv_decide` / `Lean.ofReduceBool`,
zero custom axioms; only standard axioms `propext`, `Classical.choice`, `Quot.sound`).
-/

namespace Microfat.ElfFilter

def decodeU32LE (b0 b1 b2 b3 : UInt8) : UInt32 :=
  b0.toUInt32 ||| (b1.toUInt32 <<< 8) ||| (b2.toUInt32 <<< 16) ||| (b3.toUInt32 <<< 24)

def encodeU32LEBytes (x : UInt32) : UInt8 × UInt8 × UInt8 × UInt8 :=
  (x.toUInt8, (x >>> 8).toUInt8, (x >>> 16).toUInt8, (x >>> 24).toUInt8)

private theorem shift8_eq : ((8 : UInt32).toBitVec % 32).toNat = 8 := by decide
private theorem shift16_eq : ((16 : UInt32).toBitVec % 32).toNat = 16 := by decide
private theorem shift24_eq : ((24 : UInt32).toBitVec % 32).toNat = 24 := by decide

private theorem u8_getLsbD_ge (b : UInt8) {i : Nat} (hi : 8 ≤ i) :
    b.toBitVec.getLsbD i = false :=
  Nat.testBit_lt_two_pow
    (Nat.lt_of_lt_of_le b.toBitVec.isLt (Nat.pow_le_pow_right (by omega) hi))

private theorem decodeU32LE_getLsbD (b0 b1 b2 b3 : UInt8) {i : Nat} (hi : i < 32) :
    (decodeU32LE b0 b1 b2 b3).toBitVec.getLsbD i =
      if i < 8 then b0.toBitVec.getLsbD i
      else if i < 16 then b1.toBitVec.getLsbD (i - 8)
      else if i < 24 then b2.toBitVec.getLsbD (i - 16)
      else b3.toBitVec.getLsbD (i - 24) := by
  dsimp [decodeU32LE]
  simp only [UInt32.toBitVec_or, UInt32.toBitVec_shiftLeft, UInt8.toBitVec_toUInt32,
    BitVec.getLsbD_or, BitVec.getLsbD_shiftLeft', BitVec.getLsbD_setWidth,
    shift8_eq, shift16_eq, shift24_eq]
  by_cases h8 : i < 8
  · simp [h8, hi, show i < 16 by omega, show i < 24 by omega]
  · rw [u8_getLsbD_ge b0 (by omega)]
    by_cases h16 : i < 16
    · simp [h8, h16, hi, show i - 8 < 8 by omega, show i < 24 by omega, show i - 8 < 32 by omega]
    · rw [u8_getLsbD_ge b1 (by omega)]
      by_cases h24 : i < 24
      · simp [h8, h16, h24, hi, show i - 16 < 8 by omega, show i - 16 < 32 by omega,
          show i - 8 < 32 by omega]
      · rw [u8_getLsbD_ge b2 (by omega)]
        simp [h8, h16, h24, hi, show i - 24 < 32 by omega,
          show i - 16 < 32 by omega, show i - 8 < 32 by omega]

theorem decode_encodeU32LEBytes (x : UInt32) :
    let (b0, b1, b2, b3) := encodeU32LEBytes x
    decodeU32LE b0 b1 b2 b3 = x := by
  dsimp [encodeU32LEBytes]
  apply UInt32.toBitVec_inj.mp
  apply BitVec.eq_of_getLsbD_eq
  intro i hi
  rw [decodeU32LE_getLsbD _ _ _ _ hi]
  simp only [UInt32.toBitVec_toUInt8, UInt32.toBitVec_shiftRight,
    BitVec.getLsbD_setWidth, BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight,
    shift8_eq, shift16_eq, shift24_eq]
  by_cases h8 : i < 8
  · simp [h8]
  · by_cases h16 : i < 16
    · simp [h8, h16, show i - 8 < 8 by omega, show 8 + (i - 8) = i by omega]
    · by_cases h24 : i < 24
      · simp [h8, h16, h24, show i - 16 < 8 by omega, show 16 + (i - 16) = i by omega]
      · simp [h8, h16, h24, show i - 24 < 8 by omega, show 24 + (i - 24) = i by omega]

theorem encode_decodeU32LEBytes (b0 b1 b2 b3 : UInt8) :
    encodeU32LEBytes (decodeU32LE b0 b1 b2 b3) = (b0, b1, b2, b3) := by
  dsimp [encodeU32LEBytes]
  have hb0 : (decodeU32LE b0 b1 b2 b3).toUInt8 = b0 := by
    apply UInt8.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    simp only [UInt32.toBitVec_toUInt8, BitVec.getLsbD_setWidth, decide_eq_true hi, Bool.true_and]
    rw [decodeU32LE_getLsbD _ _ _ _ (by omega)]
    simp [hi]
  have hb1 : ((decodeU32LE b0 b1 b2 b3) >>> 8).toUInt8 = b1 := by
    apply UInt8.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    simp only [UInt32.toBitVec_toUInt8, UInt32.toBitVec_shiftRight, BitVec.getLsbD_setWidth,
      BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight, shift8_eq, decide_eq_true hi, Bool.true_and]
    rw [decodeU32LE_getLsbD _ _ _ _ (by omega)]
    simp [show ¬(8 + i < 8) by omega, show 8 + i < 16 by omega]
  have hb2 : ((decodeU32LE b0 b1 b2 b3) >>> 16).toUInt8 = b2 := by
    apply UInt8.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    simp only [UInt32.toBitVec_toUInt8, UInt32.toBitVec_shiftRight, BitVec.getLsbD_setWidth,
      BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight, shift16_eq, decide_eq_true hi, Bool.true_and]
    rw [decodeU32LE_getLsbD _ _ _ _ (by omega)]
    simp [show ¬(16 + i < 8) by omega, show ¬(16 + i < 16) by omega, show 16 + i < 24 by omega]
  have hb3 : ((decodeU32LE b0 b1 b2 b3) >>> 24).toUInt8 = b3 := by
    apply UInt8.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    simp only [UInt32.toBitVec_toUInt8, UInt32.toBitVec_shiftRight, BitVec.getLsbD_setWidth,
      BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight, shift24_eq, decide_eq_true hi, Bool.true_and]
    rw [decodeU32LE_getLsbD _ _ _ _ (by omega)]
    simp [show ¬(24 + i < 8) by omega, show ¬(24 + i < 16) by omega, show ¬(24 + i < 24) by omega]
  simp [hb0, hb1, hb2, hb3]

/-! ### Part 1: ARM64 (`BL`, `B`, `ADRP`) Filter -/

private theorem mod_pow2_add_sub (k : Nat) (hk : k ≤ 32) (enc : Bool) (a d : UInt32) :
    let m : UInt32 := UInt32.ofNat (2 ^ k - 1)
    let x := a &&& m
    let z := if enc then (x + d) &&& m else (x - d) &&& m
    (if !enc then (z + d) &&& m else (z - d) &&& m) = x := by
  intro m x z
  apply UInt32.toNat_inj.mp
  have hmVal : m.toNat = 2 ^ k - 1 := by
    change (UInt32.ofNat (2 ^ k - 1)).toNat = 2 ^ k - 1
    rw [UInt32.toNat_ofNat']
    apply Nat.mod_eq_of_lt
    have hpow : 2 ^ k ≤ 2 ^ 32 := Nat.pow_le_pow_right (by omega) hk
    omega
  rcases Nat.pow_dvd_pow 2 hk with ⟨c, hc⟩
  have hdvd : 2 ^ k ∣ 2 ^ 32 := ⟨c, hc⟩
  have hd_lt : d.toNat < 2 ^ 32 := UInt32.toNat_lt d
  have hx : x.toNat = a.toNat % 2 ^ k := by
    change (a &&& m).toNat = _
    rw [UInt32.toNat_and, hmVal, Nat.and_two_pow_sub_one_eq_mod]
  cases enc
  · simp only [Bool.not_false, ↓reduceIte] at z ⊢
    have hz : z.toNat = (2 ^ 32 - d.toNat + a.toNat % 2 ^ k) % 2 ^ k := by
      change ((x - d) &&& m).toNat = _
      rw [UInt32.toNat_and, hmVal, Nat.and_two_pow_sub_one_eq_mod, UInt32.toNat_sub, hx,
        Nat.mod_mod_of_dvd _ hdvd]
    rw [UInt32.toNat_and, hmVal, Nat.and_two_pow_sub_one_eq_mod, UInt32.toNat_add, hz,
      Nat.mod_mod_of_dvd _ hdvd, Nat.mod_add_mod]
    have heq : 2 ^ 32 - d.toNat + a.toNat % 2 ^ k + d.toNat = a.toNat % 2 ^ k + 2 ^ k * c := by omega
    rw [heq, Nat.add_mul_mod_self_left, Nat.mod_mod, ← hx]
  · simp only [Bool.not_true, Bool.false_eq_true, ↓reduceIte] at z ⊢
    have hz : z.toNat = (a.toNat % 2 ^ k + d.toNat) % 2 ^ k := by
      change ((x + d) &&& m).toNat = _
      rw [UInt32.toNat_and, hmVal, Nat.and_two_pow_sub_one_eq_mod, UInt32.toNat_add, hx,
        Nat.mod_mod_of_dvd _ hdvd]
    rw [UInt32.toNat_and, hmVal, Nat.and_two_pow_sub_one_eq_mod, UInt32.toNat_sub, hz,
      Nat.mod_mod_of_dvd _ hdvd, Nat.add_mod_mod]
    have heq : 2 ^ 32 - d.toNat + (a.toNat % 2 ^ k + d.toNat) = a.toNat % 2 ^ k + 2 ^ k * c := by omega
    rw [heq, Nat.add_mul_mod_self_left, Nat.mod_mod, ← hx]

def arm64StepWord (encode : Bool) (pos insn : UInt32) : UInt32 :=
  if insn &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
     insn &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32) then
    let imm26 : UInt32 := insn &&& (0x03FFFFFF : UInt32)
    let pcWords : UInt32 := pos >>> (2 : UInt32)
    let nextImm : UInt32 :=
      if encode then (imm26 + pcWords) &&& (0x03FFFFFF : UInt32)
      else (imm26 - pcWords) &&& (0x03FFFFFF : UInt32)
    (insn &&& (0xFC000000 : UInt32)) ||| nextImm
  else if insn &&& (0x9F000000 : UInt32) = (0x90000000 : UInt32) then
    let immlo : UInt32 := (insn >>> (29 : UInt32)) &&& (0x3 : UInt32)
    let immhi : UInt32 := (insn >>> (5 : UInt32)) &&& (0x7FFFF : UInt32)
    let imm21 : UInt32 := (immhi <<< (2 : UInt32)) ||| immlo
    let pcPages : UInt32 := pos >>> (12 : UInt32)
    let nextImm21 : UInt32 :=
      if encode then (imm21 + pcPages) &&& (0x1FFFFF : UInt32)
      else (imm21 - pcPages) &&& (0x1FFFFF : UInt32)
    let nextLo : UInt32 := nextImm21 &&& (0x3 : UInt32)
    let nextHi : UInt32 := (nextImm21 >>> (2 : UInt32)) &&& (0x7FFFF : UInt32)
    let baseBits : UInt32 := insn &&& (0x9F00001F : UInt32)
    baseBits ||| (nextLo <<< (29 : UInt32)) ||| (nextHi <<< (5 : UInt32))
  else
    insn

private theorem mask_FC000000 :
    ∀ i : Fin 32, (0xFC000000 : UInt32).toBitVec.getLsbD i.val = decide (26 ≤ i.val) := by
  decide

private theorem mask_03FFFFFF :
    ∀ i : Fin 32, (0x03FFFFFF : UInt32).toBitVec.getLsbD i.val = decide (i.val < 26) := by
  decide

private theorem mask_9F000000 :
    ∀ i : Fin 32,
      (0x9F000000 : UInt32).toBitVec.getLsbD i.val = decide (i.val = 31 ∨ (24 ≤ i.val ∧ i.val ≤ 28)) := by
  decide

private theorem mask_9F00001F :
    ∀ i : Fin 32,
      (0x9F00001F : UInt32).toBitVec.getLsbD i.val =
        decide (i.val < 5 ∨ (24 ≤ i.val ∧ i.val ≤ 28) ∨ i.val = 31) := by
  decide

private theorem mask_00000003 :
    ∀ i : Fin 32, (0x3 : UInt32).toBitVec.getLsbD i.val = decide (i.val < 2) := by
  decide

private theorem mask_0007FFFF :
    ∀ i : Fin 32, (0x7FFFF : UInt32).toBitVec.getLsbD i.val = decide (i.val < 19) := by
  decide

private theorem mask_001FFFFF :
    ∀ i : Fin 32, (0x1FFFFF : UInt32).toBitVec.getLsbD i.val = decide (i.val < 21) := by
  decide

private theorem shift2_eq : ((2 : UInt32).toBitVec % 32).toNat = 2 := by decide
private theorem shift5_eq : ((5 : UInt32).toBitVec % 32).toNat = 5 := by decide
private theorem shift29_eq : ((29 : UInt32).toBitVec % 32).toNat = 29 := by decide

private theorem blMask_split_or (a b : UInt32) :
    ((a &&& (0xFC000000 : UInt32)) ||| (b &&& (0x03FFFFFF : UInt32))) &&& (0xFC000000 : UInt32) =
      a &&& (0xFC000000 : UInt32) ∧
    ((a &&& (0xFC000000 : UInt32)) ||| (b &&& (0x03FFFFFF : UInt32))) &&& (0x03FFFFFF : UInt32) =
      b &&& (0x03FFFFFF : UInt32) := by
  constructor <;>
  · apply UInt32.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    simp only [UInt32.toBitVec_and, UInt32.toBitVec_or, BitVec.getLsbD_and, BitVec.getLsbD_or,
      mask_FC000000 ⟨i, hi⟩, mask_03FFFFFF ⟨i, hi⟩]
    by_cases h26 : i < 26
    · simp [h26, decide_eq_false (by omega : ¬(26 ≤ i))]
    · simp [h26, decide_eq_true (by omega : 26 ≤ i)]

private theorem blMask_recombine (a : UInt32) :
    (a &&& (0xFC000000 : UInt32)) ||| (a &&& (0x03FFFFFF : UInt32)) = a := by
  apply UInt32.toBitVec_inj.mp
  apply BitVec.eq_of_getLsbD_eq
  intro i hi
  simp only [UInt32.toBitVec_or, UInt32.toBitVec_and, BitVec.getLsbD_or, BitVec.getLsbD_and,
    mask_FC000000 ⟨i, hi⟩, mask_03FFFFFF ⟨i, hi⟩]
  by_cases h26 : i < 26
  · simp [h26, decide_eq_false (by omega : ¬(26 ≤ i))]
  · simp [h26, decide_eq_true (by omega : 26 ≤ i)]

private theorem adrp_not_bl (x : UInt32) (h : x &&& (0x9F000000 : UInt32) = (0x90000000 : UInt32)) :
    x &&& (0xFC000000 : UInt32) ≠ (0x94000000 : UInt32) ∧
    x &&& (0xFC000000 : UInt32) ≠ (0x14000000 : UInt32) := by
  have hbit26 :
      (x &&& (0x9F000000 : UInt32)).toBitVec.getLsbD 26 =
      (0x90000000 : UInt32).toBitVec.getLsbD 26 := by
    rw [h]
  have hx26 : x.toBitVec.getLsbD 26 = false := by
    have hr : (0x90000000 : UInt32).toBitVec.getLsbD 26 = false := by decide
    have hm : (0x9F000000 : UInt32).toBitVec.getLsbD 26 = true := by decide
    rw [UInt32.toBitVec_and, BitVec.getLsbD_and, hm, hr] at hbit26
    simpa using hbit26
  have hfc26 : (x &&& (0xFC000000 : UInt32)).toBitVec.getLsbD 26 = false := by
    rw [UInt32.toBitVec_and, BitVec.getLsbD_and, hx26]
    simp
  have h94 : (0x94000000 : UInt32).toBitVec.getLsbD 26 = true := by decide
  have h14 : (0x14000000 : UInt32).toBitVec.getLsbD 26 = true := by decide
  constructor
  · intro heq; rw [heq, h94] at hfc26; cases hfc26
  · intro heq; rw [heq, h14] at hfc26; cases hfc26

private theorem adrp_pack_eq_masked (insn x21 : UInt32)
    (hx : x21 = x21 &&& (0x1FFFFF : UInt32)) :
    let loPart : UInt32 := (x21 &&& (0x3 : UInt32)) <<< (29 : UInt32)
    let hiPart : UInt32 := ((x21 >>> (2 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (5 : UInt32)
    let w : UInt32 := (insn &&& (0x9F00001F : UInt32)) ||| loPart ||| hiPart
    w &&& (0x9F000000 : UInt32) = insn &&& (0x9F000000 : UInt32) ∧
    ((((w >>> (5 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (2 : UInt32)) |||
      ((w >>> (29 : UInt32)) &&& (0x3 : UInt32))) = x21 := by
  intro loPart hiPart w
  have hxbit : ∀ j, j < 32 → x21.toBitVec.getLsbD j = (decide (j < 21) && x21.toBitVec.getLsbD j) := by
    intro j hj
    have hcon := congrArg (fun u : UInt32 => u.toBitVec.getLsbD j) hx
    rw [UInt32.toBitVec_and, BitVec.getLsbD_and, mask_001FFFFF ⟨j, hj⟩, Bool.and_comm] at hcon
    exact hcon
  constructor
  · apply UInt32.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    dsimp [w, loPart, hiPart]
    simp only [UInt32.toBitVec_and, UInt32.toBitVec_or, UInt32.toBitVec_shiftLeft,
      BitVec.getLsbD_and, BitVec.getLsbD_or, BitVec.getLsbD_shiftLeft',
      mask_9F000000 ⟨i, hi⟩, mask_9F00001F ⟨i, hi⟩, shift29_eq, shift5_eq]
    by_cases h31 : i = 31
    · subst h31
      simp only [decide_eq_true (by omega : 31 < 32),
        decide_eq_false (by omega : ¬(31 < 29)), decide_eq_false (by omega : ¬(31 < 5)),
        Bool.not_false, Bool.true_and, Bool.and_true]
      rw [mask_00000003 ⟨2, by omega⟩, mask_0007FFFF ⟨26, by omega⟩]
      simp
    · by_cases hmid : 24 ≤ i ∧ i ≤ 28
      · simp only [decide_eq_true hi, decide_eq_true (Or.inr hmid),
          decide_eq_true (Or.inr (Or.inl hmid)),
          decide_eq_true (by omega : i < 29), decide_eq_false (by omega : ¬(i < 5)),
          Bool.not_true, Bool.not_false, Bool.true_and, Bool.false_and, Bool.and_true, Bool.or_false]
        rw [mask_0007FFFF ⟨i - 5, by omega⟩, decide_eq_false (by omega : ¬(i - 5 < 19))]
        simp
      · simp [decide_eq_false (show ¬(i = 31 ∨ (24 ≤ i ∧ i ≤ 28)) by omega)]
  · apply UInt32.toBitVec_inj.mp
    apply BitVec.eq_of_getLsbD_eq
    intro i hi
    dsimp [w, loPart, hiPart]
    simp only [UInt32.toBitVec_or, UInt32.toBitVec_shiftLeft, UInt32.toBitVec_and,
      UInt32.toBitVec_shiftRight, BitVec.getLsbD_or, BitVec.getLsbD_shiftLeft',
      BitVec.getLsbD_and, BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight,
      shift2_eq, shift5_eq, shift29_eq, mask_00000003 ⟨i, hi⟩]
    by_cases h2 : i < 2
    · have h29 : 29 + i < 32 := by omega
      simp only [decide_eq_true hi,
        Bool.and_false, Bool.false_or, Bool.true_and, Bool.not_false,
        mask_9F00001F ⟨29 + i, h29⟩,
        decide_eq_false (by omega : ¬(29 + i < 5 ∨ (24 ≤ 29 + i ∧ 29 + i ≤ 28) ∨ 29 + i = 31)),
        decide_eq_true h29,
        decide_eq_false (by omega : ¬(29 + i < 29)), decide_eq_false (by omega : ¬(29 + i < 5)),
        show 29 + i - 29 = i by omega,
        mask_00000003 ⟨i, by omega⟩, decide_eq_true h2,
        mask_0007FFFF ⟨29 + i - 5, by omega⟩, decide_eq_false (by omega : ¬(29 + i - 5 < 19)),
        Bool.and_true, Bool.or_false, Bool.false_or]
      rw [hxbit i hi]
      simp [show i < 21 by omega]
    · by_cases h21 : i < 21
      · have h5 : 5 + (i - 2) < 32 := by omega
        have hi2 : i - 2 < 19 := by omega
        simp only [decide_eq_true hi,
          decide_eq_false h2, decide_eq_true h5,
          Bool.false_and, Bool.or_false, Bool.true_and, Bool.and_true, Bool.not_false, Bool.not_true,
          mask_9F00001F ⟨5 + (i - 2), h5⟩,
          decide_eq_false (by omega : ¬(5 + (i - 2) < 5 ∨ (24 ≤ 5 + (i - 2) ∧ 5 + (i - 2) ≤ 28) ∨ 5 + (i - 2) = 31)),
          decide_eq_true (by omega : 5 + (i - 2) < 29), decide_eq_false (by omega : ¬(5 + (i - 2) < 5)),
          show 5 + (i - 2) - 5 = i - 2 by omega,
          mask_0007FFFF ⟨i - 2, by omega⟩, decide_eq_true hi2,
          show 2 + (i - 2) = i by omega]
        rw [hxbit i hi]
        simp [h21]
      · simp only [decide_eq_true hi, decide_eq_false h2,
          mask_0007FFFF ⟨i - 2, by omega⟩, decide_eq_false (by omega : ¬(i - 2 < 19)),
          Bool.and_false]
        have hfalse := hxbit i hi
        simp only [decide_eq_false h21, Bool.false_and] at hfalse
        exact hfalse.symm

private theorem adrp_recombine (insn : UInt32) :
    let imm21 : UInt32 :=
      (((insn >>> (5 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (2 : UInt32)) |||
      ((insn >>> (29 : UInt32)) &&& (0x3 : UInt32))
    let loPart : UInt32 := (imm21 &&& (0x3 : UInt32)) <<< (29 : UInt32)
    let hiPart : UInt32 := ((imm21 >>> (2 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (5 : UInt32)
    (insn &&& (0x9F00001F : UInt32)) ||| loPart ||| hiPart = insn := by
  intro imm21 loPart hiPart
  apply UInt32.toBitVec_inj.mp
  apply BitVec.eq_of_getLsbD_eq
  intro i hi
  have himm : ∀ j, j < 21 → imm21.toBitVec.getLsbD j =
      if j < 2 then insn.toBitVec.getLsbD (29 + j) else insn.toBitVec.getLsbD (5 + (j - 2)) := by
    intro j hj
    dsimp [imm21]
    simp only [UInt32.toBitVec_or, UInt32.toBitVec_shiftLeft, UInt32.toBitVec_and,
      UInt32.toBitVec_shiftRight, BitVec.getLsbD_or, BitVec.getLsbD_shiftLeft',
      BitVec.getLsbD_and, BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight,
      shift2_eq, shift5_eq, shift29_eq,
      mask_00000003 ⟨j, by omega⟩]
    by_cases h2 : j < 2
    · simp [h2]
    · rw [mask_0007FFFF ⟨j - 2, by omega⟩]
      simp [h2, decide_eq_true (by omega : j < 32), decide_eq_true (by omega : j - 2 < 19)]
  dsimp [loPart, hiPart]
  simp only [UInt32.toBitVec_or, UInt32.toBitVec_and, UInt32.toBitVec_shiftLeft,
    UInt32.toBitVec_shiftRight, BitVec.getLsbD_or, BitVec.getLsbD_and,
    BitVec.getLsbD_shiftLeft', BitVec.ushiftRight_eq', BitVec.getLsbD_ushiftRight,
    shift2_eq, shift5_eq, shift29_eq, mask_9F00001F ⟨i, hi⟩]
  by_cases h5 : i < 5
  · simp [h5, decide_eq_true (by omega : i < 29)]
  · by_cases h24 : i < 24
    · simp only [decide_eq_true hi, decide_eq_true (by omega : i < 29),
        decide_eq_false (by omega : ¬(i < 5)),
        decide_eq_false (by omega : ¬(i < 5 ∨ (24 ≤ i ∧ i ≤ 28) ∨ i = 31)),
        Bool.not_true, Bool.not_false, Bool.false_and, Bool.or_false, Bool.true_and]
      rw [mask_0007FFFF ⟨i - 5, by omega⟩, decide_eq_true (by omega : i - 5 < 19)]
      simp only [Bool.and_true]
      rw [himm (2 + (i - 5)) (by omega)]
      simp [show ¬(2 + (i - 5) < 2) by omega, show 5 + (i - 5) = i by omega]
    · by_cases h28 : i ≤ 28
      · simp only [decide_eq_true hi,
          decide_eq_true (Or.inr (Or.inl ⟨by omega, h28⟩) : i < 5 ∨ (24 ≤ i ∧ i ≤ 28) ∨ i = 31),
          decide_eq_true (by omega : i < 29), decide_eq_false (by omega : ¬(i < 5)),
          Bool.not_true, Bool.not_false, Bool.true_and, Bool.false_and, Bool.and_true, Bool.or_false]
        rw [mask_0007FFFF ⟨i - 5, by omega⟩, decide_eq_false (by omega : ¬(i - 5 < 19))]
        simp
      · by_cases h30 : i ≤ 30
        · simp only [decide_eq_true hi, decide_eq_false (by omega : ¬(i < 29)),
            decide_eq_false (by omega : ¬(i < 5)),
            decide_eq_false (by omega : ¬(i < 5 ∨ (24 ≤ i ∧ i ≤ 28) ∨ i = 31)),
            Bool.not_false, Bool.true_and]
          rw [mask_0007FFFF ⟨i - 5, by omega⟩, decide_eq_false (by omega : ¬(i - 5 < 19)),
            mask_00000003 ⟨i - 29, by omega⟩, decide_eq_true (by omega : i - 29 < 2)]
          simp only [Bool.and_false, Bool.or_false, Bool.and_true]
          rw [himm (i - 29) (by omega)]
          simp [show i - 29 < 2 by omega, show 29 + (i - 29) = i by omega]
        · have hi31 : i = 31 := by omega
          subst hi31
          simp only [decide_eq_true (by omega : 31 < 32),
            decide_eq_false (by omega : ¬(31 < 29)), decide_eq_false (by omega : ¬(31 < 5)),
            Bool.not_false, Bool.true_and, Bool.and_true]
          rw [mask_00000003 ⟨2, by omega⟩, mask_0007FFFF ⟨26, by omega⟩]
          simp

private theorem arm64StepWord_bl (encode : Bool) (pos insn : UInt32)
    (h : (insn &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
          insn &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32)) = true) :
    arm64StepWord encode pos insn =
      (insn &&& (0xFC000000 : UInt32)) |||
        (if encode then ((insn &&& (0x03FFFFFF : UInt32)) + (pos >>> (2 : UInt32))) &&& (0x03FFFFFF : UInt32)
         else ((insn &&& (0x03FFFFFF : UInt32)) - (pos >>> (2 : UInt32))) &&& (0x03FFFFFF : UInt32)) := by
  rw [arm64StepWord]
  simp only [eq_true h, ↓reduceIte]

private theorem arm64StepWord_adrp (encode : Bool) (pos insn : UInt32)
    (hnotbl : ¬(insn &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
                insn &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32)) = true)
    (hadrp : insn &&& (0x9F000000 : UInt32) = (0x90000000 : UInt32)) :
    arm64StepWord encode pos insn =
      let immlo : UInt32 := (insn >>> (29 : UInt32)) &&& (0x3 : UInt32)
      let immhi : UInt32 := (insn >>> (5 : UInt32)) &&& (0x7FFFF : UInt32)
      let imm21 : UInt32 := (immhi <<< (2 : UInt32)) ||| immlo
      let pcPages : UInt32 := pos >>> (12 : UInt32)
      let nextImm21 : UInt32 :=
        if encode then (imm21 + pcPages) &&& (0x1FFFFF : UInt32)
        else (imm21 - pcPages) &&& (0x1FFFFF : UInt32)
      (insn &&& (0x9F00001F : UInt32)) |||
        ((nextImm21 &&& (0x3 : UInt32)) <<< (29 : UInt32)) |||
        (((nextImm21 >>> (2 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (5 : UInt32)) := by
  rw [arm64StepWord]
  simp only [eq_false hnotbl, ↓reduceIte, eq_true hadrp]

private theorem arm64StepWord_else (encode : Bool) (pos insn : UInt32)
    (hnotbl : ¬(insn &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
                insn &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32)) = true)
    (hnadrp : insn &&& (0x9F000000 : UInt32) ≠ (0x90000000 : UInt32)) :
    arm64StepWord encode pos insn = insn := by
  rw [arm64StepWord]
  simp only [eq_false hnotbl, ↓reduceIte, eq_false hnadrp]

theorem arm64_word_roundtrip_gen (enc : Bool) (pos insn : UInt32) :
    arm64StepWord (!enc) pos (arm64StepWord enc pos insn) = insn := by
  by_cases hbl : (insn &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
                  insn &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32)) = true
  · rw [arm64StepWord_bl enc pos insn hbl]
    let imm26 : UInt32 := insn &&& (0x03FFFFFF : UInt32)
    let pcWords : UInt32 := pos >>> (2 : UInt32)
    let nextImm : UInt32 :=
      if enc then (imm26 + pcWords) &&& (0x03FFFFFF : UInt32)
      else (imm26 - pcWords) &&& (0x03FFFFFF : UInt32)
    let w : UInt32 := (insn &&& (0xFC000000 : UInt32)) ||| nextImm
    have hnextMasked : nextImm = nextImm &&& (0x03FFFFFF : UInt32) := by
      cases enc <;>
      · dsimp [nextImm]
        apply UInt32.toBitVec_inj.mp; apply BitVec.eq_of_getLsbD_eq; intro i hi
        simp only [UInt32.toBitVec_and, BitVec.getLsbD_and]
        rw [Bool.and_assoc, Bool.and_self]
    have hwEq : w = (insn &&& (0xFC000000 : UInt32)) ||| (nextImm &&& (0x03FFFFFF : UInt32)) := by
      dsimp [w]; rw [← hnextMasked]
    have hsplit0 := blMask_split_or insn nextImm
    rw [← hwEq] at hsplit0
    have hsplit : w &&& (0xFC000000 : UInt32) = insn &&& (0xFC000000 : UInt32) ∧
        w &&& (0x03FFFFFF : UInt32) = nextImm :=
      ⟨hsplit0.1, by rw [hsplit0.2, ← hnextMasked]⟩
    have hbl2 : (w &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
                 w &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32)) = true := by
      simpa [hsplit.1] using hbl
    change arm64StepWord (!enc) pos w = insn
    rw [arm64StepWord_bl (!enc) pos w hbl2, hsplit.1, hsplit.2]
    have hround := mod_pow2_add_sub 26 (by omega) enc insn pcWords
    dsimp only at hround
    have hmEq : UInt32.ofNat (2 ^ 26 - 1) = (0x03FFFFFF : UInt32) := by decide
    rw [hmEq] at hround
    change (insn &&& (0xFC000000 : UInt32)) |||
      (if !enc then (nextImm + pcWords) &&& (0x03FFFFFF : UInt32)
       else (nextImm - pcWords) &&& (0x03FFFFFF : UInt32)) = insn
    rw [hround]
    exact blMask_recombine insn
  · by_cases hadrp : insn &&& (0x9F000000 : UInt32) = (0x90000000 : UInt32)
    · rw [arm64StepWord_adrp enc pos insn hbl hadrp]
      dsimp only
      let immlo : UInt32 := (insn >>> (29 : UInt32)) &&& (0x3 : UInt32)
      let immhi : UInt32 := (insn >>> (5 : UInt32)) &&& (0x7FFFF : UInt32)
      let imm21 : UInt32 := (immhi <<< (2 : UInt32)) ||| immlo
      let pcPages : UInt32 := pos >>> (12 : UInt32)
      let nextImm21 : UInt32 :=
        if enc then (imm21 + pcPages) &&& (0x1FFFFF : UInt32)
        else (imm21 - pcPages) &&& (0x1FFFFF : UInt32)
      let loPart : UInt32 := (nextImm21 &&& (0x3 : UInt32)) <<< (29 : UInt32)
      let hiPart : UInt32 := ((nextImm21 >>> (2 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (5 : UInt32)
      let w : UInt32 := (insn &&& (0x9F00001F : UInt32)) ||| loPart ||| hiPart
      have hnext21 : nextImm21 = nextImm21 &&& (0x1FFFFF : UInt32) := by
        cases enc <;>
        · dsimp [nextImm21]
          apply UInt32.toBitVec_inj.mp; apply BitVec.eq_of_getLsbD_eq; intro i hi
          simp only [UInt32.toBitVec_and, BitVec.getLsbD_and]
          rw [Bool.and_assoc, Bool.and_self]
      have hpack := adrp_pack_eq_masked insn nextImm21 hnext21
      have hadrp2 : w &&& (0x9F000000 : UInt32) = (0x90000000 : UInt32) := Eq.trans hpack.1 hadrp
      have hnotbl2 : ¬(w &&& (0xFC000000 : UInt32) = (0x94000000 : UInt32) ||
                       w &&& (0xFC000000 : UInt32) = (0x14000000 : UInt32)) = true := by
        have hdis := adrp_not_bl w hadrp2
        simp [hdis.1, hdis.2]
      change arm64StepWord (!enc) pos w = insn
      rw [arm64StepWord_adrp (!enc) pos w hnotbl2 hadrp2]
      dsimp only
      have hwMask : w &&& (0x9F00001F : UInt32) = insn &&& (0x9F00001F : UInt32) := by
        apply UInt32.toBitVec_inj.mp; apply BitVec.eq_of_getLsbD_eq; intro i hi
        dsimp [w, loPart, hiPart]
        simp only [UInt32.toBitVec_and, UInt32.toBitVec_or, UInt32.toBitVec_shiftLeft,
          BitVec.getLsbD_and, BitVec.getLsbD_or, BitVec.getLsbD_shiftLeft',
          mask_9F00001F ⟨i, hi⟩, shift29_eq, shift5_eq]
        by_cases h5 : i < 5
        · simp [h5, decide_eq_true (by omega : i < 29)]
        · by_cases hmid : 24 ≤ i ∧ i ≤ 28
          · simp only [decide_eq_true hi,
              decide_eq_true (Or.inr (Or.inl hmid) : i < 5 ∨ (24 ≤ i ∧ i ≤ 28) ∨ i = 31),
              decide_eq_true (by omega : i < 29), decide_eq_false (by omega : ¬(i < 5)),
              Bool.not_true, Bool.not_false, Bool.true_and, Bool.false_and, Bool.and_true, Bool.or_false]
            rw [mask_0007FFFF ⟨i - 5, by omega⟩, decide_eq_false (by omega : ¬(i - 5 < 19))]
            simp
          · by_cases h31 : i = 31
            · subst h31
              simp only [decide_eq_true (by omega : 31 < 32),
                decide_eq_false (by omega : ¬(31 < 29)), decide_eq_false (by omega : ¬(31 < 5)),
                Bool.not_false, Bool.true_and, Bool.and_true]
              rw [mask_00000003 ⟨2, by omega⟩, mask_0007FFFF ⟨26, by omega⟩]
              simp
            · simp [decide_eq_false (show ¬(i < 5 ∨ (24 ≤ i ∧ i ≤ 28) ∨ i = 31) by omega)]
      rw [hwMask]
      have hpack2 : (((w >>> (5 : UInt32)) &&& (0x7FFFF : UInt32)) <<< (2 : UInt32)) |||
          ((w >>> (29 : UInt32)) &&& (0x3 : UInt32)) = nextImm21 := hpack.2
      rw [hpack2]
      have himm21Mask : imm21 = imm21 &&& (0x1FFFFF : UInt32) := by
        apply UInt32.toBitVec_inj.mp; apply BitVec.eq_of_getLsbD_eq; intro i hi
        dsimp [imm21, immhi, immlo]
        simp only [UInt32.toBitVec_and, UInt32.toBitVec_or, UInt32.toBitVec_shiftLeft,
          BitVec.getLsbD_and, BitVec.getLsbD_or, BitVec.getLsbD_shiftLeft',
          shift2_eq, mask_001FFFFF ⟨i, hi⟩, mask_00000003 ⟨i, hi⟩]
        by_cases h2 : i < 2
        · simp [h2, decide_eq_true (by omega : i < 21)]
        · by_cases h21 : i < 21
          · rw [mask_0007FFFF ⟨i - 2, by omega⟩]
            simp [hi, h2, h21, decide_eq_true (by omega : i - 2 < 19)]
          · rw [mask_0007FFFF ⟨i - 2, by omega⟩]
            simp [hi, h2, h21, decide_eq_false (by omega : ¬(i - 2 < 19))]
      have hround := mod_pow2_add_sub 21 (by omega) enc imm21 pcPages
      dsimp only at hround
      have hm21Eq : UInt32.ofNat (2 ^ 21 - 1) = (0x1FFFFF : UInt32) := by decide
      rw [hm21Eq, ← himm21Mask] at hround
      rw [hround]
      exact adrp_recombine insn
    · rw [arm64StepWord_else enc pos insn hbl hadrp, arm64StepWord_else (!enc) pos insn hbl hadrp]

theorem arm64_word_roundtrip (pos insn : UInt32) :
    arm64StepWord false pos (arm64StepWord true pos insn) = insn :=
  arm64_word_roundtrip_gen true pos insn

theorem arm64_word_inverse_roundtrip (pos insn : UInt32) :
    arm64StepWord true pos (arm64StepWord false pos insn) = insn :=
  arm64_word_roundtrip_gen false pos insn

def arm64Transform (encode : Bool) : List UInt8 → UInt32 → List UInt8
  | b0 :: b1 :: b2 :: b3 :: rest, pos =>
      let w := decodeU32LE b0 b1 b2 b3
      let w' := arm64StepWord encode pos w
      let (c0, c1, c2, c3) := encodeU32LEBytes w'
      c0 :: c1 :: c2 :: c3 :: arm64Transform encode rest (pos + 4)
  | tail, _ => tail

theorem arm64_bytes_length (encode : Bool) :
    ∀ (bs : List UInt8) (pos : UInt32), (arm64Transform encode bs pos).length = bs.length
  | _ :: _ :: _ :: _ :: rest, pos => by
      simp [arm64Transform, arm64_bytes_length encode rest (pos + 4)]
  | [], _ => rfl
  | [_], _ => rfl
  | [_, _], _ => rfl
  | [_, _, _], _ => rfl

theorem arm64_bytes_roundtrip :
    ∀ (bs : List UInt8) (pos : UInt32),
      arm64Transform false (arm64Transform true bs pos) pos = bs
  | b0 :: b1 :: b2 :: b3 :: rest, pos => by
      dsimp only [arm64Transform]
      let w' := arm64StepWord true pos (decodeU32LE b0 b1 b2 b3)
      have hdc : decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
          (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2 = w' :=
        decode_encodeU32LEBytes w'
      change (encodeU32LEBytes (arm64StepWord false pos
        (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
          (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).1 ::
        (encodeU32LEBytes (arm64StepWord false pos
          (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
            (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).2.1 ::
        (encodeU32LEBytes (arm64StepWord false pos
          (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
            (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).2.2.1 ::
        (encodeU32LEBytes (arm64StepWord false pos
          (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
            (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).2.2.2 ::
        arm64Transform false (arm64Transform true rest (pos + 4)) (pos + 4) =
        b0 :: b1 :: b2 :: b3 :: rest
      rw [hdc, arm64_word_roundtrip, encode_decodeU32LEBytes]
      simp [arm64_bytes_roundtrip rest (pos + 4)]
  | [], _ => rfl
  | [_], _ => rfl
  | [_, _], _ => rfl
  | [_, _, _], _ => rfl

theorem arm64_bytes_inverse_roundtrip :
    ∀ (bs : List UInt8) (pos : UInt32),
      arm64Transform true (arm64Transform false bs pos) pos = bs
  | b0 :: b1 :: b2 :: b3 :: rest, pos => by
      dsimp only [arm64Transform]
      let w' := arm64StepWord false pos (decodeU32LE b0 b1 b2 b3)
      have hdc : decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
          (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2 = w' :=
        decode_encodeU32LEBytes w'
      change (encodeU32LEBytes (arm64StepWord true pos
        (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
          (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).1 ::
        (encodeU32LEBytes (arm64StepWord true pos
          (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
            (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).2.1 ::
        (encodeU32LEBytes (arm64StepWord true pos
          (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
            (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).2.2.1 ::
        (encodeU32LEBytes (arm64StepWord true pos
          (decodeU32LE (encodeU32LEBytes w').1 (encodeU32LEBytes w').2.1
            (encodeU32LEBytes w').2.2.1 (encodeU32LEBytes w').2.2.2))).2.2.2 ::
        arm64Transform true (arm64Transform false rest (pos + 4)) (pos + 4) =
        b0 :: b1 :: b2 :: b3 :: rest
      rw [hdc, arm64_word_inverse_roundtrip, encode_decodeU32LEBytes]
      simp [arm64_bytes_inverse_roundtrip rest (pos + 4)]
  | [], _ => rfl
  | [_], _ => rfl
  | [_, _], _ => rfl
  | [_, _, _], _ => rfl

/-! ### Part 2: x86-64 Variable-Stride `BCJ+RIP` Filter -/

def shiftImm32Bytes (encode : Bool) (delta b0 b1 b2 b3 : UInt32) : UInt8 × UInt8 × UInt8 × UInt8 :=
  let v := b0 ||| (b1 <<< 8) ||| (b2 <<< 16) ||| (b3 <<< 24)
  let v' := if encode then v + delta else v - delta
  encodeU32LEBytes v'

theorem shiftImm32Bytes_roundtrip (enc : Bool) (delta : UInt32) (b0 b1 b2 b3 : UInt8) :
    let (c0, c1, c2, c3) := shiftImm32Bytes enc delta b0.toUInt32 b1.toUInt32 b2.toUInt32 b3.toUInt32
    shiftImm32Bytes (!enc) delta c0.toUInt32 c1.toUInt32 c2.toUInt32 c3.toUInt32 = (b0, b1, b2, b3) := by
  dsimp only [shiftImm32Bytes]
  let v : UInt32 := b0.toUInt32 ||| (b1.toUInt32 <<< 8) ||| (b2.toUInt32 <<< 16) ||| (b3.toUInt32 <<< 24)
  let v' : UInt32 := if enc then v + delta else v - delta
  have hdc :
      ((encodeU32LEBytes v').1.toUInt32 ||| ((encodeU32LEBytes v').2.1.toUInt32 <<< 8) |||
        ((encodeU32LEBytes v').2.2.1.toUInt32 <<< 16) ||| ((encodeU32LEBytes v').2.2.2.toUInt32 <<< 24)) = v' :=
    decode_encodeU32LEBytes v'
  change encodeU32LEBytes
    (if !enc then
      ((encodeU32LEBytes v').1.toUInt32 ||| ((encodeU32LEBytes v').2.1.toUInt32 <<< 8) |||
        ((encodeU32LEBytes v').2.2.1.toUInt32 <<< 16) ||| ((encodeU32LEBytes v').2.2.2.toUInt32 <<< 24)) + delta
     else
      ((encodeU32LEBytes v').1.toUInt32 ||| ((encodeU32LEBytes v').2.1.toUInt32 <<< 8) |||
        ((encodeU32LEBytes v').2.2.1.toUInt32 <<< 16) ||| ((encodeU32LEBytes v').2.2.2.toUInt32 <<< 24)) - delta) =
    (b0, b1, b2, b3)
  rw [hdc]
  have hcancel : (if !enc then v' + delta else v' - delta) = v := by
    cases enc
    · simp [v', UInt32.sub_add_cancel]
    · simp [v', UInt32.add_sub_cancel]
  rw [hcancel]
  exact encode_decodeU32LEBytes b0 b1 b2 b3

@[inline] def isX86Rel8 (b : UInt8) : Bool :=
  b == 0xE8 || b == 0xE9

@[inline] def isX86Jcc (b : UInt8) : Bool :=
  (b &&& 0xF0) == 0x80

@[inline] def isX86Rex (b : UInt8) : Bool :=
  b == 0x48 || b == 0x4C

@[inline] def isX86RipOp (b : UInt8) : Bool :=
  b == 0x8D || b == 0x8B || b == 0x89

@[inline] def isX86RipModrm (b : UInt8) : Bool :=
  (b &&& 0xC7) == 0x05

def x86Step (encode : Bool) (pos : UInt32) (b0 : UInt8) (rest : List UInt8) :
    List UInt8 × List UInt8 × UInt32 :=
  if isX86Rel8 b0 then
    match rest with
    | d0 :: d1 :: d2 :: d3 :: tail =>
        let (c0, c1, c2, c3) :=
          shiftImm32Bytes encode (pos + 5) d0.toUInt32 d1.toUInt32 d2.toUInt32 d3.toUInt32
        ([b0, c0, c1, c2, c3], tail, pos + 5)
    | _ => ([b0], rest, pos + 1)
  else if b0 == 0x0F then
    match rest with
    | jcc :: d0 :: d1 :: d2 :: d3 :: tail =>
        if isX86Jcc jcc then
          let (c0, c1, c2, c3) :=
            shiftImm32Bytes encode (pos + 6) d0.toUInt32 d1.toUInt32 d2.toUInt32 d3.toUInt32
          ([b0, jcc, c0, c1, c2, c3], tail, pos + 6)
        else
          ([b0], rest, pos + 1)
    | _ => ([b0], rest, pos + 1)
  else if isX86Rex b0 then
    match rest with
    | op :: modrm :: d0 :: d1 :: d2 :: d3 :: tail =>
        if isX86RipOp op && isX86RipModrm modrm then
          let (c0, c1, c2, c3) :=
            shiftImm32Bytes encode (pos + 7) d0.toUInt32 d1.toUInt32 d2.toUInt32 d3.toUInt32
          ([b0, op, modrm, c0, c1, c2, c3], tail, pos + 7)
        else
          ([b0], rest, pos + 1)
    | _ => ([b0], rest, pos + 1)
  else
    ([b0], rest, pos + 1)

private theorem x86Step_invariants (encode : Bool) (pos : UInt32) (b0 : UInt8) (rest : List UInt8) :
    let step := x86Step encode pos b0 rest
    (∃ t, step.1 = b0 :: t) ∧ step.1.length + step.2.1.length = 1 + rest.length := by
  dsimp [x86Step]
  by_cases hrel : isX86Rel8 b0 = true
  · simp only [eq_true hrel, ↓reduceIte]
    rcases rest with _ | ⟨d0, _ | ⟨d1, _ | ⟨d2, _ | ⟨d3, tail⟩⟩⟩⟩ <;>
      exact ⟨⟨_, rfl⟩, by simp only [List.length_cons, List.length_nil]; try omega⟩
  · simp only [eq_false hrel, ↓reduceIte]
    by_cases hj : (b0 == 0x0F) = true
    · simp only [eq_true hj, ↓reduceIte]
      rcases rest with _ | ⟨jcc, _ | ⟨d0, _ | ⟨d1, _ | ⟨d2, _ | ⟨d3, tail⟩⟩⟩⟩⟩
      · exact ⟨⟨_, rfl⟩, by simp⟩
      · exact ⟨⟨_, rfl⟩, by simp⟩
      · exact ⟨⟨_, rfl⟩, by simp⟩
      · exact ⟨⟨_, rfl⟩, by simp⟩
      · exact ⟨⟨_, rfl⟩, by simp⟩
      · dsimp only
        by_cases hjcc : isX86Jcc jcc = true
        · simp only [eq_true hjcc, ↓reduceIte]
          exact ⟨⟨_, rfl⟩, by simp only [List.length_cons, List.length_nil]; omega⟩
        · simp only [eq_false hjcc, ↓reduceIte]
          exact ⟨⟨_, rfl⟩, by simp⟩
    · simp only [eq_false hj, ↓reduceIte]
      by_cases hrex : isX86Rex b0 = true
      · simp only [eq_true hrex, ↓reduceIte]
        rcases rest with _ | ⟨op, _ | ⟨modrm, _ | ⟨d0, _ | ⟨d1, _ | ⟨d2, _ | ⟨d3, tail⟩⟩⟩⟩⟩⟩
        · exact ⟨⟨_, rfl⟩, by simp⟩
        · exact ⟨⟨_, rfl⟩, by simp⟩
        · exact ⟨⟨_, rfl⟩, by simp⟩
        · exact ⟨⟨_, rfl⟩, by simp⟩
        · exact ⟨⟨_, rfl⟩, by simp⟩
        · exact ⟨⟨_, rfl⟩, by simp⟩
        · dsimp only
          by_cases hrip : (isX86RipOp op && isX86RipModrm modrm) = true
          · simp only [eq_true hrip, ↓reduceIte]
            exact ⟨⟨_, rfl⟩, by simp only [List.length_cons, List.length_nil]; omega⟩
          · simp only [eq_false hrip, ↓reduceIte]
            exact ⟨⟨_, rfl⟩, by simp⟩
      · simp only [eq_false hrex, ↓reduceIte]
        exact ⟨⟨_, rfl⟩, by simp⟩

private theorem x86Step_tail_lt (encode : Bool) (pos : UInt32) (b0 : UInt8) (rest : List UInt8) :
    (x86Step encode pos b0 rest).2.1.length < (b0 :: rest).length := by
  rcases x86Step_invariants encode pos b0 rest with ⟨⟨t, ht⟩, hbal⟩
  rw [ht, List.length_cons] at hbal
  simp only [List.length_cons]
  omega

def x86Transform (encode : Bool) (bs : List UInt8) (pos : UInt32) : List UInt8 :=
  match bs with
  | [] => []
  | b0 :: rest =>
      let step := x86Step encode pos b0 rest
      step.1 ++ x86Transform encode step.2.1 step.2.2
termination_by bs.length
decreasing_by exact x86Step_tail_lt encode pos b0 rest

@[simp] theorem x86Transform_nil (encode : Bool) (pos : UInt32) :
    x86Transform encode [] pos = [] := by
  rw [x86Transform]

theorem x86Transform_cons (encode : Bool) (b0 : UInt8) (rest : List UInt8) (pos : UInt32) :
    x86Transform encode (b0 :: rest) pos =
      (x86Step encode pos b0 rest).1 ++
        x86Transform encode (x86Step encode pos b0 rest).2.1 (x86Step encode pos b0 rest).2.2 := by
  conv => lhs; rw [x86Transform]

private theorem x86Transform_length_aux :
    ∀ n encode (bs : List UInt8) pos, bs.length ≤ n → (x86Transform encode bs pos).length = bs.length
  | 0, encode, bs, pos, h => by
      cases bs with
      | nil => simp
      | cons _ _ => simp at h
  | n + 1, encode, bs, pos, h => by
      cases bs with
      | nil => simp
      | cons b0 rest =>
          rw [x86Transform_cons, List.length_append]
          have hbal := (x86Step_invariants encode pos b0 rest).2
          have hlt := x86Step_tail_lt encode pos b0 rest
          simp only [List.length_cons] at h hlt
          have hih := x86Transform_length_aux n encode (x86Step encode pos b0 rest).2.1
            (x86Step encode pos b0 rest).2.2 (by omega)
          rw [hih, hbal, List.length_cons]
          omega

theorem x86_length_preserved (encode : Bool) (bs : List UInt8) (pos : UInt32) :
    (x86Transform encode bs pos).length = bs.length :=
  x86Transform_length_aux bs.length encode bs pos (Nat.le_refl _)

theorem x86Transform_head_cons (encode : Bool) (b0 : UInt8) (rest : List UInt8) (pos : UInt32) :
    ∃ tail', x86Transform encode (b0 :: rest) pos = b0 :: tail' := by
  rw [x86Transform_cons]
  rcases (x86Step_invariants encode pos b0 rest).1 with ⟨t, ht⟩
  rw [ht]
  exact ⟨t ++ x86Transform encode (x86Step encode pos b0 rest).2.1 (x86Step encode pos b0 rest).2.2, rfl⟩

private theorem ripOp_disjoint {op : UInt8} (hop : isX86RipOp op = true) :
    isX86Rel8 op = false ∧ (op == 0x0F) = false ∧ isX86Rex op = false := by
  simp only [isX86RipOp, Bool.or_eq_true, beq_iff_eq] at hop
  rcases hop with (rfl | rfl) | rfl <;> decide

private theorem x86Transform_ripOp_step (encode : Bool) (op modrm : UInt8)
    (tl : List UInt8) (pos : UInt32) (hop : isX86RipOp op = true) :
    ∃ tail', x86Transform encode (op :: modrm :: tl) pos = op :: modrm :: tail' := by
  have hdis := ripOp_disjoint hop
  rw [x86Transform_cons]
  have hstep : x86Step encode pos op (modrm :: tl) = ([op], modrm :: tl, pos + 1) := by
    dsimp [x86Step]
    simp only [eq_false (by simp [hdis.1] : ¬isX86Rel8 op = true),
      eq_false (by simp [hdis.2.1] : ¬(op == 0x0F) = true),
      eq_false (by simp [hdis.2.2] : ¬isX86Rex op = true), ↓reduceIte]
  rw [hstep]
  rcases x86Transform_head_cons encode modrm tl (pos + 1) with ⟨t2, ht2⟩
  rw [ht2]
  exact ⟨t2, rfl⟩

private theorem x86Step_roundtrip_on (enc : Bool) (pos : UInt32) (b0 : UInt8)
    (rest rem' : List UInt8)
    (hlen : rem'.length = (x86Step enc pos b0 rest).2.1.length)
    (hhead : ∀ hd tl, (x86Step enc pos b0 rest).2.1 = hd :: tl → ∃ tl', rem' = hd :: tl')
    (hrip2 : ∀ op modrm tl,
      (x86Step enc pos b0 rest).2.1 = op :: modrm :: tl →
      isX86RipOp op = true →
      ∃ tl', rem' = op :: modrm :: tl') :
    let step1 := x86Step enc pos b0 rest
    let decTail := step1.1.tail ++ rem'
    let step2 := x86Step (!enc) pos b0 decTail
    step2.1 ++ step1.2.1 = b0 :: rest ∧ step2.2.1 = rem' ∧ step2.2.2 = step1.2.2 := by
  dsimp [x86Step] at hlen hhead hrip2 ⊢
  by_cases hrel : isX86Rel8 b0 = true
  · simp only [eq_true hrel, ↓reduceIte] at hlen hhead hrip2 ⊢
    rcases rest with _ | ⟨d0, _ | ⟨d1, _ | ⟨d2, _ | ⟨d3, tail⟩⟩⟩⟩
    · simp only at hlen hhead hrip2 ⊢
      have hnil : rem' = [] := List.eq_nil_of_length_eq_zero hlen
      subst hnil; simp
    · simp only [List.length_cons, List.length_nil] at hlen
      rcases rem' with _ | ⟨a0, _ | ⟨_, _⟩⟩ <;> simp at hlen
      simp
    · simp only [List.length_cons, List.length_nil] at hlen
      rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨_, _⟩⟩⟩ <;> simp at hlen
      simp
    · simp only [List.length_cons, List.length_nil] at hlen
      rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨_, _⟩⟩⟩⟩ <;> simp at hlen
      simp
    · dsimp only
      simp only [List.tail_cons, List.cons_append, List.nil_append]
      have hsh := shiftImm32Bytes_roundtrip enc (pos + 5) d0 d1 d2 d3
      dsimp only at hsh
      rw [hsh]
      simp
  · simp only [eq_false hrel, ↓reduceIte] at hlen hhead hrip2 ⊢
    by_cases hj : (b0 == 0x0F) = true
    · simp only [eq_true hj, ↓reduceIte] at hlen hhead hrip2 ⊢
      rcases rest with _ | ⟨jcc, _ | ⟨d0, _ | ⟨d1, _ | ⟨d2, _ | ⟨d3, tail⟩⟩⟩⟩⟩
      · simp only at hlen
        have hnil : rem' = [] := List.eq_nil_of_length_eq_zero hlen
        subst hnil; simp
      · simp only [List.length_cons, List.length_nil] at hlen
        rcases rem' with _ | ⟨a0, _ | ⟨_, _⟩⟩ <;> simp at hlen
        simp
      · simp only [List.length_cons, List.length_nil] at hlen
        rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨_, _⟩⟩⟩ <;> simp at hlen
        simp
      · simp only [List.length_cons, List.length_nil] at hlen
        rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨_, _⟩⟩⟩⟩ <;> simp at hlen
        simp
      · simp only [List.length_cons, List.length_nil] at hlen
        rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨a3, _ | ⟨_, _⟩⟩⟩⟩⟩ <;> simp at hlen
        simp
      · by_cases hjcc : isX86Jcc jcc = true
        · dsimp only
          simp only [eq_true hjcc, ↓reduceIte, List.tail_cons, List.cons_append, List.nil_append]
          have hsh := shiftImm32Bytes_roundtrip enc (pos + 6) d0 d1 d2 d3
          dsimp only at hsh
          rw [hsh]
          simp
        · simp only [eq_false hjcc, ↓reduceIte] at hlen hhead hrip2 ⊢
          rcases hhead jcc (d0 :: d1 :: d2 :: d3 :: tail) rfl with ⟨tl', rfl⟩
          rcases tl' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨a3, tl4⟩⟩⟩⟩
          · simp at hlen
          · simp at hlen
          · simp at hlen
          · simp at hlen
          · dsimp only [List.tail_cons, List.nil_append]
            simp [hjcc]
    · simp only [eq_false hj, ↓reduceIte] at hlen hhead hrip2 ⊢
      by_cases hrex : isX86Rex b0 = true
      · simp only [eq_true hrex, ↓reduceIte] at hlen hhead hrip2 ⊢
        rcases rest with _ | ⟨op, _ | ⟨modrm, _ | ⟨d0, _ | ⟨d1, _ | ⟨d2, _ | ⟨d3, tail⟩⟩⟩⟩⟩⟩
        · simp only at hlen
          have hnil : rem' = [] := List.eq_nil_of_length_eq_zero hlen
          subst hnil; simp
        · simp only [List.length_cons, List.length_nil] at hlen
          rcases rem' with _ | ⟨a0, _ | ⟨_, _⟩⟩ <;> simp at hlen
          simp
        · simp only [List.length_cons, List.length_nil] at hlen
          rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨_, _⟩⟩⟩ <;> simp at hlen
          simp
        · simp only [List.length_cons, List.length_nil] at hlen
          rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨_, _⟩⟩⟩⟩ <;> simp at hlen
          simp
        · simp only [List.length_cons, List.length_nil] at hlen
          rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨a3, _ | ⟨_, _⟩⟩⟩⟩⟩ <;> simp at hlen
          simp
        · simp only [List.length_cons, List.length_nil] at hlen
          rcases rem' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨a3, _ | ⟨a4, _ | ⟨_, _⟩⟩⟩⟩⟩⟩ <;> simp at hlen
          simp
        · by_cases hrip : (isX86RipOp op && isX86RipModrm modrm) = true
          · dsimp only
            simp only [eq_true hrip, ↓reduceIte, List.tail_cons, List.cons_append, List.nil_append]
            have hsh := shiftImm32Bytes_roundtrip enc (pos + 7) d0 d1 d2 d3
            dsimp only at hsh
            rw [hsh]
            simp
          · simp only [eq_false hrip, ↓reduceIte] at hlen hhead hrip2 ⊢
            by_cases hop : isX86RipOp op = true
            · rcases hrip2 op modrm (d0 :: d1 :: d2 :: d3 :: tail) rfl hop with ⟨tl', rfl⟩
              rcases tl' with _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨a3, tl4⟩⟩⟩⟩
              · simp at hlen
              · simp at hlen
              · simp at hlen
              · simp at hlen
              · dsimp only [List.tail_cons, List.nil_append]
                simp [hrip]
            · rcases hhead op (modrm :: d0 :: d1 :: d2 :: d3 :: tail) rfl with ⟨tl', rfl⟩
              rcases tl' with _ | ⟨m', _ | ⟨a0, _ | ⟨a1, _ | ⟨a2, _ | ⟨a3, tl4⟩⟩⟩⟩⟩
              · simp at hlen
              · simp at hlen
              · simp at hlen
              · simp at hlen
              · simp at hlen
              · dsimp only [List.tail_cons, List.nil_append]
                have hnotrip : ¬(isX86RipOp op && isX86RipModrm m') = true := by
                  simp only [Bool.and_eq_true]
                  exact fun h => hop h.1
                simp [hnotrip]
      · simp only [eq_false hrex, ↓reduceIte] at hlen hhead hrip2 ⊢
        simp

private theorem x86_roundtrip_aux :
    ∀ n enc (bs : List UInt8) pos, bs.length ≤ n → x86Transform (!enc) (x86Transform enc bs pos) pos = bs
  | 0, enc, bs, pos, h => by
      cases bs with
      | nil => simp
      | cons _ _ => simp at h
  | n + 1, enc, bs, pos, h => by
      cases bs with
      | nil => simp
      | cons b0 rest =>
          rw [x86Transform_cons enc b0 rest pos]
          let step1 := x86Step enc pos b0 rest
          let rem' := x86Transform enc step1.2.1 step1.2.2
          have hcons : ∃ t, step1.1 = b0 :: t := (x86Step_invariants enc pos b0 rest).1
          rcases hcons with ⟨t, ht⟩
          have hstep1eq : step1.1 ++ rem' = b0 :: (step1.1.tail ++ rem') := by
            rw [ht]; rfl
          change x86Transform (!enc) (step1.1 ++ rem') pos = b0 :: rest
          rw [hstep1eq, x86Transform_cons (!enc) b0 (step1.1.tail ++ rem') pos]
          have hlen : rem'.length = step1.2.1.length := x86_length_preserved enc step1.2.1 step1.2.2
          have hhead : ∀ hd tl, step1.2.1 = hd :: tl → ∃ tl', rem' = hd :: tl' := by
            intro hd tl hrem
            dsimp [rem']
            rw [hrem]
            exact x86Transform_head_cons enc hd tl step1.2.2
          have hrip2 : ∀ op modrm tl,
              step1.2.1 = op :: modrm :: tl →
              isX86RipOp op = true →
              ∃ tl', rem' = op :: modrm :: tl' := by
            intro op modrm tl hrem hop
            dsimp [rem']
            rw [hrem]
            exact x86Transform_ripOp_step enc op modrm tl step1.2.2 hop
          have hround := x86Step_roundtrip_on enc pos b0 rest rem' hlen hhead hrip2
          dsimp only at hround
          rcases hround with ⟨hchunk, hnextTail, hnextPos⟩
          rw [hnextTail, hnextPos]
          have hlt := x86Step_tail_lt enc pos b0 rest
          change step1.2.1.length < (b0 :: rest).length at hlt
          simp only [List.length_cons] at h hlt
          have hih : x86Transform (!enc) rem' step1.2.2 = step1.2.1 :=
            x86_roundtrip_aux n enc step1.2.1 step1.2.2 (by omega)
          rw [hih, hchunk]

theorem x86_roundtrip (bs : List UInt8) (pos : UInt32) :
    x86Transform false (x86Transform true bs pos) pos = bs :=
  x86_roundtrip_aux bs.length true bs pos (Nat.le_refl _)

theorem x86_inverse_roundtrip (bs : List UInt8) (pos : UInt32) :
    x86Transform true (x86Transform false bs pos) pos = bs := by
  have h := x86_roundtrip_aux bs.length false bs pos (Nat.le_refl _)
  simpa using h

/-! ### Part 3: Chunk-Local Extent Filtering & Deduplication Soundness -/

structure FilteredExtent where
  data : List UInt8
  filterBase : Option UInt32
  deriving DecidableEq, Repr

def applyExtentX86 (encode : Bool) (e : FilteredExtent) : FilteredExtent :=
  match e.filterBase with
  | some pos => { data := x86Transform encode e.data pos, filterBase := some pos }
  | none => e

def applyExtentArm64 (encode : Bool) (e : FilteredExtent) : FilteredExtent :=
  match e.filterBase with
  | some pos => { data := arm64Transform encode e.data pos, filterBase := some pos }
  | none => e

def reassembleExtents (es : List FilteredExtent) : List UInt8 :=
  es.flatMap (·.data)

private theorem applyExtentX86_roundtrip (e : FilteredExtent) :
    applyExtentX86 false (applyExtentX86 true e) = e := by
  cases e with
  | mk d fb =>
      cases fb with
      | none => rfl
      | some pos => simp [applyExtentX86, x86_roundtrip]

private theorem applyExtentArm64_roundtrip (e : FilteredExtent) :
    applyExtentArm64 false (applyExtentArm64 true e) = e := by
  cases e with
  | mk d fb =>
      cases fb with
      | none => rfl
      | some pos => simp [applyExtentArm64, arm64_bytes_roundtrip]

theorem x86_chunk_local_reassembly (es : List FilteredExtent) :
    reassembleExtents ((es.map (applyExtentX86 true)).map (applyExtentX86 false)) =
      reassembleExtents es := by
  rw [List.map_map]
  have hcomp : applyExtentX86 false ∘ applyExtentX86 true = id := by
    funext e
    exact applyExtentX86_roundtrip e
  rw [hcomp, List.map_id]

theorem arm64_chunk_local_reassembly (es : List FilteredExtent) :
    reassembleExtents ((es.map (applyExtentArm64 true)).map (applyExtentArm64 false)) =
      reassembleExtents es := by
  rw [List.map_map]
  have hcomp : applyExtentArm64 false ∘ applyExtentArm64 true = id := by
    funext e
    exact applyExtentArm64_roundtrip e
  rw [hcomp, List.map_id]

theorem x86_dedup_multi_offset_sound (orig1 orig2 blk : List UInt8) (pos1 pos2 : UInt32)
    (h1 : x86Transform true orig1 pos1 = blk)
    (h2 : x86Transform true orig2 pos2 = blk) :
    x86Transform false blk pos1 = orig1 ∧ x86Transform false blk pos2 = orig2 := by
  constructor
  · rw [← h1, x86_roundtrip]
  · rw [← h2, x86_roundtrip]

theorem arm64_dedup_multi_offset_sound (orig1 orig2 blk : List UInt8) (pos1 pos2 : UInt32)
    (h1 : arm64Transform true orig1 pos1 = blk)
    (h2 : arm64Transform true orig2 pos2 = blk) :
    arm64Transform false blk pos1 = orig1 ∧ arm64Transform false blk pos2 = orig2 := by
  constructor
  · rw [← h1, arm64_bytes_roundtrip]
  · rw [← h2, arm64_bytes_roundtrip]

private theorem pos_add_four_length (pos : UInt32) (n : Nat) :
    (pos + (4 : UInt32)) + UInt32.ofNat n = pos + UInt32.ofNat (n + 4) := by
  apply UInt32.toNat_inj.mp
  simp only [UInt32.toNat_add, UInt32.toNat_ofNat']
  have h4 : (4 : UInt32).toNat = 4 := by decide
  rw [h4, Nat.add_mod_mod, Nat.mod_add_mod]
  rw [Nat.add_mod pos.toNat ((n + 4) % 2 ^ 32) (2 ^ 32), Nat.mod_mod, ← Nat.add_mod]
  rw [show pos.toNat + 4 + n = pos.toNat + (n + 4) by omega]

theorem arm64_aligned_split_commutes (encode : Bool) :
    ∀ (a b : List UInt8) (pos : UInt32),
      a.length % 4 = 0 →
      arm64Transform encode (a ++ b) pos =
        arm64Transform encode a pos ++ arm64Transform encode b (pos + UInt32.ofNat a.length)
  | [], b, pos, _ => by
      simp [arm64Transform]
  | [_], _, _, h => by simp at h
  | [_, _], _, _, h => by simp at h
  | [_, _, _], _, _, h => by simp at h
  | b0 :: b1 :: b2 :: b3 :: rest, b, pos, h => by
      have hrest : rest.length % 4 = 0 := by
        simp only [List.length_cons] at h
        omega
      dsimp only [List.cons_append, arm64Transform]
      have ih := arm64_aligned_split_commutes encode rest b (pos + 4) hrest
      have hpos := pos_add_four_length pos rest.length
      simp only [List.length_cons]
      rw [← hpos, ih]

end Microfat.ElfFilter
