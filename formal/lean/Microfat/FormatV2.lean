/-
  Formal specification and kernel-checked proofs for Microfat Format v2 binary
  index serialization, exact consumption, and bounds validation.
  Matches constants and invariants in `internal/format/format.go`.
-/

namespace Microfat.FormatV2

abbrev trailerSize : Nat := 56
abbrev formatVersion2 : UInt16 := 2
abbrev maxIndexSize : Nat := 1024 * 1024
abbrev maxPayloadSize : Nat := 1024 * 1024 * 1024
abbrev maxDictionarySize : Nat := 1024 * 1024
abbrev maxSHA256HexLen : Nat := 64
abbrev maxInt64Nat : Nat := 2 ^ 63 - 1

def indexMagicV2 : List UInt8 := [0x00, 0xFA, 0x4D, 0x32]
def defaultCompressionBytes : List UInt8 := [0x7a, 0x73, 0x74, 0x64] -- "zstd"
def archAMD64Bytes : List UInt8 := [0x61, 0x6d, 0x64, 0x36, 0x34] -- "amd64"
def archARM64Bytes : List UInt8 := [0x61, 0x72, 0x6d, 0x36, 0x34] -- "arm64"

/-! ### Little-Endian Integer Codecs -/

def encodeU16 (n : UInt16) : List UInt8 :=
  [UInt8.ofNat (n.toNat % 256), UInt8.ofNat ((n.toNat / 256) % 256)]

def decodeU16 : List UInt8 → Option (UInt16 × List UInt8)
  | b0 :: b1 :: rest =>
      some (UInt16.ofNat (b0.toNat + b1.toNat * 256), rest)
  | _ => none

theorem decodeU16_encodeU16 (n : UInt16) (rest : List UInt8) :
    decodeU16 (encodeU16 n ++ rest) = some (n, rest) := by
  simp only [encodeU16, List.cons_append, List.nil_append, decodeU16]
  have hn : n.toNat < 2 ^ 16 := n.toNat_lt
  congr 2
  apply UInt16.toNat_inj.mp
  simp
  omega

theorem decodeU16_len {bs rest : List UInt8} {n : UInt16} :
    decodeU16 bs = some (n, rest) → bs.length = 2 + rest.length := by
  intro h
  rcases bs with _ | ⟨b0, _ | ⟨b1, tl⟩⟩ <;> simp [decodeU16] at h
  rcases h with ⟨_, rfl⟩
  simp
  omega

def encodeU32 (n : UInt32) : List UInt8 :=
  [ UInt8.ofNat (n.toNat % 256),
    UInt8.ofNat ((n.toNat / 256) % 256),
    UInt8.ofNat ((n.toNat / 65536) % 256),
    UInt8.ofNat ((n.toNat / 16777216) % 256) ]

def decodeU32 : List UInt8 → Option (UInt32 × List UInt8)
  | b0 :: b1 :: b2 :: b3 :: rest =>
      some (UInt32.ofNat (b0.toNat + b1.toNat * 256 + b2.toNat * 65536 + b3.toNat * 16777216), rest)
  | _ => none

theorem decodeU32_encodeU32 (n : UInt32) (rest : List UInt8) :
    decodeU32 (encodeU32 n ++ rest) = some (n, rest) := by
  simp only [encodeU32, List.cons_append, List.nil_append, decodeU32]
  have hn : n.toNat < 2 ^ 32 := n.toNat_lt
  congr 2
  apply UInt32.toNat_inj.mp
  simp
  omega

theorem decodeU32_len {bs rest : List UInt8} {n : UInt32} :
    decodeU32 bs = some (n, rest) → bs.length = 4 + rest.length := by
  intro h
  rcases bs with _ | ⟨b0, _ | ⟨b1, _ | ⟨b2, _ | ⟨b3, tl⟩⟩⟩⟩ <;> simp [decodeU32] at h
  rcases h with ⟨_, rfl⟩
  simp
  omega

def encodeU64 (n : UInt64) : List UInt8 :=
  [ UInt8.ofNat (n.toNat % 256),
    UInt8.ofNat ((n.toNat / 256) % 256),
    UInt8.ofNat ((n.toNat / 65536) % 256),
    UInt8.ofNat ((n.toNat / 16777216) % 256),
    UInt8.ofNat ((n.toNat / 4294967296) % 256),
    UInt8.ofNat ((n.toNat / 1099511627776) % 256),
    UInt8.ofNat ((n.toNat / 281474976710656) % 256),
    UInt8.ofNat ((n.toNat / 72057594037927936) % 256) ]

def decodeU64 : List UInt8 → Option (UInt64 × List UInt8)
  | b0 :: b1 :: b2 :: b3 :: b4 :: b5 :: b6 :: b7 :: rest =>
      some (UInt64.ofNat (
        b0.toNat +
        b1.toNat * 256 +
        b2.toNat * 65536 +
        b3.toNat * 16777216 +
        b4.toNat * 4294967296 +
        b5.toNat * 1099511627776 +
        b6.toNat * 281474976710656 +
        b7.toNat * 72057594037927936), rest)
  | _ => none

theorem decodeU64_encodeU64 (n : UInt64) (rest : List UInt8) :
    decodeU64 (encodeU64 n ++ rest) = some (n, rest) := by
  simp only [encodeU64, List.cons_append, List.nil_append, decodeU64]
  have hn : n.toNat < 2 ^ 64 := n.toNat_lt
  congr 2
  apply UInt64.toNat_inj.mp
  simp
  omega

theorem decodeU64_len {bs rest : List UInt8} {n : UInt64} :
    decodeU64 bs = some (n, rest) → bs.length = 8 + rest.length := by
  intro h
  rcases bs with
    _ | ⟨b0, _ | ⟨b1, _ | ⟨b2, _ | ⟨b3, _ | ⟨b4, _ | ⟨b5, _ | ⟨b6, _ | ⟨b7, tl⟩⟩⟩⟩⟩⟩⟩⟩ <;>
    simp [decodeU64] at h
  rcases h with ⟨_, rfl⟩
  simp
  omega

/-! ### Length-Prefixed Byte Slice Codecs -/

def encodeBytes1 (bs : List UInt8) : List UInt8 :=
  UInt8.ofNat bs.length :: bs

def decodeBytes1 : List UInt8 → Option (List UInt8 × List UInt8)
  | lenByte :: rest =>
      let n := lenByte.toNat
      if rest.length < n then none
      else some (rest.take n, rest.drop n)
  | [] => none

theorem decodeBytes1_encodeBytes1 (bs rest : List UInt8) (hlen : bs.length < 256) :
    decodeBytes1 (encodeBytes1 bs ++ rest) = some (bs, rest) := by
  simp only [encodeBytes1, List.cons_append, decodeBytes1]
  have h1 : (UInt8.ofNat bs.length).toNat = bs.length := by
    simp
    omega
  rw [h1]
  have hnot : ¬((bs ++ rest).length < bs.length) := by simp
  simp only [hnot, ↓reduceIte, List.take_left', List.drop_left']

theorem decodeBytes1_len {input payload rest : List UInt8} :
    decodeBytes1 input = some (payload, rest) →
    input.length = 1 + payload.length + rest.length ∧ payload.length < 256 := by
  intro h
  cases input with
  | nil => simp [decodeBytes1] at h
  | cons b tl =>
      simp only [decodeBytes1] at h
      split at h
      · contradiction
      · rename_i hge
        have hlt : b.toNat < 256 := b.toNat_lt
        have hle : b.toNat ≤ tl.length := Nat.le_of_not_lt hge
        simp only [Option.some.injEq, Prod.mk.injEq] at h
        rcases h with ⟨rfl, rfl⟩
        simp [List.length_take, List.length_drop, Nat.min_eq_left hle]
        omega

def encodeBytes2 (bs : List UInt8) : List UInt8 :=
  encodeU16 (UInt16.ofNat bs.length) ++ bs

def decodeBytes2 (input : List UInt8) : Option (List UInt8 × List UInt8) :=
  match decodeU16 input with
  | none => none
  | some (lenU16, rest) =>
      let n := lenU16.toNat
      if rest.length < n then none
      else some (rest.take n, rest.drop n)

theorem decodeBytes2_encodeBytes2 (bs rest : List UInt8) (hlen : bs.length < 65536) :
    decodeBytes2 (encodeBytes2 bs ++ rest) = some (bs, rest) := by
  simp only [encodeBytes2, List.append_assoc, decodeBytes2]
  rw [decodeU16_encodeU16]
  dsimp only
  have h1 : (UInt16.ofNat bs.length).toNat = bs.length := by
    simp
    omega
  rw [h1]
  have hnot : ¬((bs ++ rest).length < bs.length) := by simp
  simp only [hnot, ↓reduceIte, List.take_left', List.drop_left']

theorem decodeBytes2_len {input payload rest : List UInt8} :
    decodeBytes2 input = some (payload, rest) →
    input.length = 2 + payload.length + rest.length ∧ payload.length < 65536 := by
  intro h
  simp only [decodeBytes2] at h
  cases h16 : decodeU16 input with
  | none => simp [h16] at h
  | some p =>
      rw [h16] at h
      rcases p with ⟨lenU16, tl⟩
      have hlen16 := decodeU16_len h16
      dsimp only at h
      split at h
      · contradiction
      · rename_i hge
        have hlt : lenU16.toNat < 65536 := lenU16.toNat_lt
        have hle : lenU16.toNat ≤ tl.length := Nat.le_of_not_lt hge
        simp only [Option.some.injEq, Prod.mk.injEq] at h
        rcases h with ⟨rfl, rfl⟩
        simp [List.length_take, List.length_drop, Nat.min_eq_left hle] at *
        omega

/-! ### SHA-256 Hex Validation & Normalization -/

def isHexLower (c : UInt8) : Bool :=
  (0x30 ≤ c && c ≤ 0x39) || (0x61 ≤ c && c ≤ 0x66)

def isHexChar (c : UInt8) : Bool :=
  isHexLower c || (0x41 ≤ c && c ≤ 0x46)

def toLowerASCII (c : UInt8) : UInt8 :=
  if 0x41 ≤ c && c ≤ 0x5A then c + 0x20 else c

theorem toLowerASCII_of_isHexLower {c : UInt8} (h : isHexLower c = true) :
    toLowerASCII c = c := by
  simp only [isHexLower, Bool.or_eq_true, Bool.and_eq_true, decide_eq_true_eq] at h
  simp only [toLowerASCII]
  have h39 : (0x39 : UInt8).toNat = 0x39 := rfl
  have h41 : (0x41 : UInt8).toNat = 0x41 := rfl
  have h5A : (0x5A : UInt8).toNat = 0x5A := rfl
  have h61 : (0x61 : UInt8).toNat = 0x61 := rfl
  have hnot : ¬(0x41 ≤ c ∧ c ≤ 0x5A) := by
    intro ⟨hlo, hhi⟩
    have hlo' : (0x41 : UInt8).toNat ≤ c.toNat := hlo
    have hhi' : c.toNat ≤ (0x5A : UInt8).toNat := hhi
    rcases h with ⟨_, h2⟩ | ⟨h1, _⟩
    · have h2' : c.toNat ≤ (0x39 : UInt8).toNat := h2
      omega
    · have h1' : (0x61 : UInt8).toNat ≤ c.toNat := h1
      omega
  simp [hnot]

def validateChecksum (bs : List UInt8) : Bool :=
  bs.isEmpty || (bs.length == maxSHA256HexLen && bs.all isHexChar)

def isCanonicalLowerChecksum (bs : List UInt8) : Prop :=
  bs.length = maxSHA256HexLen ∧ ∀ c ∈ bs, isHexLower c = true

theorem map_toLowerASCII_canonical {bs : List UInt8} (hc : ∀ c ∈ bs, isHexLower c = true) :
    bs.map toLowerASCII = bs := by
  induction bs with
  | nil => rfl
  | cons x xs ih =>
      simp only [List.map_cons]
      rw [toLowerASCII_of_isHexLower (hc x (by simp)), ih (fun c h => hc c (by simp [h]))]

theorem validateChecksum_of_canonical {bs : List UInt8} (hc : isCanonicalLowerChecksum bs) :
    validateChecksum bs = true := by
  rcases hc with ⟨hlen, hchars⟩
  simp only [validateChecksum, Bool.or_eq_true, Bool.and_eq_true, beq_iff_eq, List.all_eq_true]
  right
  refine ⟨hlen, fun c hmem => ?_⟩
  simp [isHexChar, hchars c hmem]

/-! ### Format v2 Data Model & Wire Serialization -/

structure VariantEntry where
  level : List UInt8
  offset : UInt64
  compressedSize : UInt64
  uncompressedSize : UInt64
  sha256 : List UInt8
  compression : List UInt8
  deriving DecidableEq, Repr

structure Index where
  version : UInt16
  appName : List UInt8
  targetOS : List UInt8
  targetArch : List UInt8
  createdUnix : UInt64
  dictOffset : UInt64
  dictSize : UInt64
  dictSHA256 : List UInt8
  dictID : UInt32
  variants : List VariantEntry
  deriving DecidableEq, Repr

def VariantEntry.Canonical (v : VariantEntry) : Prop :=
  v.level.length < 256 ∧
  v.offset.toNat ≤ maxInt64Nat ∧
  v.compressedSize.toNat ≤ maxInt64Nat ∧
  v.uncompressedSize.toNat ≤ maxInt64Nat ∧
  isCanonicalLowerChecksum v.sha256 ∧
  !v.compression.isEmpty ∧
  v.compression.length < 256

def Index.Canonical (idx : Index) : Prop :=
  idx.version = formatVersion2 ∧
  idx.targetOS.length < 256 ∧
  idx.targetArch.length < 256 ∧
  idx.appName.length < 65536 ∧
  idx.variants.length < 65536 ∧
  idx.dictOffset.toNat ≤ maxInt64Nat ∧
  idx.dictSize.toNat ≤ maxInt64Nat ∧
  (if idx.dictSize.toNat > 0 then isCanonicalLowerChecksum idx.dictSHA256 else idx.dictSHA256 = []) ∧
  ∀ v ∈ idx.variants, VariantEntry.Canonical v

def encodeVariant (v : VariantEntry) : Option (List UInt8) :=
  let comp := if v.compression.isEmpty then defaultCompressionBytes else v.compression
  if !v.sha256.isEmpty && validateChecksum v.sha256 &&
      v.level.length < 256 && v.sha256.length < 256 && comp.length < 256 &&
      v.offset.toNat ≤ maxInt64Nat && v.compressedSize.toNat ≤ maxInt64Nat &&
      v.uncompressedSize.toNat ≤ maxInt64Nat then
    some (
      encodeBytes1 v.level ++
      encodeU64 v.offset ++
      encodeU64 v.compressedSize ++
      encodeU64 v.uncompressedSize ++
      encodeBytes1 v.sha256 ++
      encodeBytes1 comp
    )
  else none

def decodeVariant (input : List UInt8) : Option (VariantEntry × List UInt8) :=
  Option.bind (decodeBytes1 input) fun (level, r1) =>
  Option.bind (decodeU64 r1) fun (vOffset, r2) =>
  Option.bind (decodeU64 r2) fun (compSize, r3) =>
  Option.bind (decodeU64 r3) fun (uncompSize, r4) =>
  Option.bind (decodeBytes1 r4) fun (shaBytes, r5) =>
  if shaBytes.isEmpty || !validateChecksum shaBytes then none
  else Option.bind (decodeBytes1 r5) fun (compBytes, r6) =>
    let comp := if compBytes.isEmpty then defaultCompressionBytes else compBytes
    some ({
      level := level,
      offset := vOffset,
      compressedSize := compSize,
      uncompressedSize := uncompSize,
      sha256 := shaBytes.map toLowerASCII,
      compression := comp
    }, r6)

theorem decodeVariant_encodeVariant (v : VariantEntry) (bs rest : List UInt8)
    (hc : v.Canonical) (henc : encodeVariant v = some bs) :
    decodeVariant (bs ++ rest) = some (v, rest) := by
  rcases hc with ⟨hlvl, hof, hcs, hus, hsha, hcompNE, hcompLen⟩
  have hcompNotEmpty : v.compression.isEmpty = false := by simpa using hcompNE
  have hvalSha : validateChecksum v.sha256 = true := validateChecksum_of_canonical hsha
  have hshaNE : v.sha256.isEmpty = false := by
    cases h : v.sha256 with
    | nil => have := hsha.1; simp [h] at this
    | cons _ _ => rfl
  have hsha256Len : v.sha256.length < 256 := by
    have h64 := hsha.1
    dsimp only [maxSHA256HexLen] at h64
    omega
  simp only [encodeVariant, hcompNotEmpty, Bool.false_eq_true, ↓reduceIte,
    hshaNE, hvalSha, hlvl, hsha256Len, hcompLen, hof, hcs, hus,
    Bool.not_false, Bool.true_and, decide_true, Option.some.injEq] at henc
  subst henc
  simp only [decodeVariant, List.append_assoc]
  rw [decodeBytes1_encodeBytes1 _ _ hlvl]; simp only [Option.bind_some]
  rw [decodeU64_encodeU64]; simp only [Option.bind_some]
  rw [decodeU64_encodeU64]; simp only [Option.bind_some]
  rw [decodeU64_encodeU64]; simp only [Option.bind_some]
  rw [decodeBytes1_encodeBytes1 _ _ hsha256Len]
  simp only [Option.bind_some, hshaNE, hvalSha, Bool.false_or, Bool.not_true, Bool.false_eq_true, ↓reduceIte]
  rw [decodeBytes1_encodeBytes1 _ _ hcompLen]
  simp only [Option.bind_some, hcompNotEmpty, Bool.false_eq_true, ↓reduceIte]
  have hmap : v.sha256.map toLowerASCII = v.sha256 := map_toLowerASCII_canonical hsha.2
  cases v
  simp [hmap]

def variantSerializedLen (v : VariantEntry) : Nat :=
  let comp := if v.compression.isEmpty then defaultCompressionBytes else v.compression
  1 + v.level.length + 8 + 8 + 8 + 1 + v.sha256.length + 1 + comp.length

theorem decodeVariant_exact_bound {input rest : List UInt8} {v : VariantEntry}
    (h : decodeVariant input = some (v, rest)) :
    input.length ≤ variantSerializedLen v + rest.length ∧
    input.length + 4 ≥ variantSerializedLen v + rest.length := by
  simp only [decodeVariant, Option.bind_eq_some_iff] at h
  rcases h with ⟨⟨lvl, r1⟩, h1, ⟨voff, r2⟩, h2, ⟨vcs, r3⟩, h3, ⟨vus, r4⟩, h4, ⟨sha, r5⟩, h5, hbranch⟩
  have hl1 := (decodeBytes1_len h1).1
  have hl2 := decodeU64_len h2
  have hl3 := decodeU64_len h3
  have hl4 := decodeU64_len h4
  have hl5 := (decodeBytes1_len h5).1
  dsimp only at hbranch
  split at hbranch
  · contradiction
  · simp only [Option.bind_eq_some_iff] at hbranch
    rcases hbranch with ⟨⟨comp, r6⟩, h6, hres⟩
    have hl6 := (decodeBytes1_len h6).1
    simp only [Option.some.injEq, Prod.mk.injEq] at hres
    rcases hres with ⟨rfl, rfl⟩
    simp only [variantSerializedLen, List.length_map]
    have htotal : input.length = 27 + lvl.length + sha.length + comp.length + r6.length := by
      rw [hl1, hl2, hl3, hl4, hl5, hl6]
      omega
    by_cases hce : comp.isEmpty = true
    · have hcl0 : comp.length = 0 := by
        cases comp with
        | nil => rfl
        | cons _ _ => simp at hce
      have hdef : defaultCompressionBytes.length = 4 := rfl
      simp only [hce, ↓reduceIte]
      have hdef2 : (if defaultCompressionBytes.isEmpty = true then defaultCompressionBytes else defaultCompressionBytes).length = 4 := rfl
      rw [hdef2]
      exact ⟨by omega, by omega⟩
    · have hce' : comp.isEmpty = false := by
        cases hcb : comp.isEmpty <;> simp_all
      simp only [hce', Bool.false_eq_true, ↓reduceIte]
      exact ⟨by omega, by omega⟩

def encodeVariants : List VariantEntry → Option (List UInt8)
  | [] => some []
  | v :: vs =>
      match encodeVariant v, encodeVariants vs with
      | some b, some bs => some (b ++ bs)
      | _, _ => none

def decodeVariants : Nat → List UInt8 → Option (List VariantEntry × List UInt8)
  | 0, rest => some ([], rest)
  | n + 1, input =>
      Option.bind (decodeVariant input) fun (v, r1) =>
      Option.bind (decodeVariants n r1) fun (vs, r2) =>
      some (v :: vs, r2)

theorem decodeVariants_encodeVariants (vs : List VariantEntry) (bs rest : List UInt8)
    (hc : ∀ v ∈ vs, VariantEntry.Canonical v) (henc : encodeVariants vs = some bs) :
    decodeVariants vs.length (bs ++ rest) = some (vs, rest) := by
  induction vs generalizing bs with
  | nil =>
      simp [encodeVariants] at henc
      subst henc
      simp [decodeVariants]
  | cons v vs ih =>
      simp only [encodeVariants] at henc
      cases hv : encodeVariant v with
      | none => simp [hv] at henc
      | some bv =>
          cases hvs : encodeVariants vs with
          | none => simp [hv, hvs] at henc
          | some bvs =>
              simp only [hv, hvs, Option.some.injEq] at henc
              subst henc
              simp only [List.length_cons, decodeVariants, List.append_assoc]
              have hcv : v.Canonical := hc v (by simp)
              rw [decodeVariant_encodeVariant v bv (bvs ++ rest) hcv hv]
              simp only [Option.bind_some]
              rw [ih bvs (fun w hw => hc w (by simp [hw])) hvs]
              rfl

theorem decodeVariants_wire_bound : ∀ (n : Nat) (input rest : List UInt8) (vs : List VariantEntry),
    decodeVariants n input = some (vs, rest) →
    input.length ≤ (vs.map variantSerializedLen).sum + rest.length ∧
    input.length + 4 * vs.length ≥ (vs.map variantSerializedLen).sum + rest.length
  | 0, input, rest, vs, h => by
      simp [decodeVariants] at h
      rcases h with ⟨rfl, rfl⟩
      simp
  | n + 1, input, rest, vs, h => by
      simp only [decodeVariants, Option.bind_eq_some_iff] at h
      rcases h with ⟨⟨v, r1⟩, h1, ⟨vs', r2⟩, h2, hres⟩
      simp only [Option.some.injEq, Prod.mk.injEq] at hres
      rcases hres with ⟨rfl, rfl⟩
      have hl1 := decodeVariant_exact_bound h1
      have hl2 := decodeVariants_wire_bound n r1 r2 vs' h2
      simp [List.map_cons, List.sum_cons, List.length_cons]
      omega

def marshalBinaryIndex (idx : Index) : Option (List UInt8) :=
  if idx.targetOS.length < 256 && idx.targetArch.length < 256 &&
     idx.appName.length < 65536 && idx.variants.length < 65536 &&
     idx.dictSHA256.length < 256 &&
     idx.dictOffset.toNat ≤ maxInt64Nat && idx.dictSize.toNat ≤ maxInt64Nat &&
     !(idx.dictSize.toNat > 0 && (idx.dictSHA256.isEmpty || !validateChecksum idx.dictSHA256)) then
    match encodeVariants idx.variants with
    | none => none
    | some varBytes =>
        some (
          indexMagicV2 ++
          encodeU16 formatVersion2 ++
          encodeU64 idx.createdUnix ++
          encodeU64 idx.dictOffset ++
          encodeU64 idx.dictSize ++
          encodeU32 idx.dictID ++
          encodeBytes1 idx.dictSHA256 ++
          encodeBytes1 idx.targetOS ++
          encodeBytes1 idx.targetArch ++
          encodeBytes2 idx.appName ++
          encodeU16 (UInt16.ofNat idx.variants.length) ++
          varBytes
        )
  else none

def unmarshalBinaryIndex (data : List UInt8) : Option Index :=
  if data.length < 34 then none
  else
  let magic := data.take 4
  let afterMagic := data.drop 4
  if magic != indexMagicV2 then none
  else
  Option.bind (decodeU16 afterMagic) fun (version, r0) =>
  if version != formatVersion2 then none
  else
  Option.bind (decodeU64 r0) fun (createdUnix, r1) =>
  Option.bind (decodeU64 r1) fun (dictOffset, r2) =>
  Option.bind (decodeU64 r2) fun (dictSize, r3) =>
  Option.bind (decodeU32 r3) fun (dictID, r4) =>
  Option.bind (decodeBytes1 r4) fun (dictSHA, r5) =>
  if dictSize.toNat > 0 && (dictSHA.isEmpty || !validateChecksum dictSHA) then none
  else
  Option.bind (decodeBytes1 r5) fun (targetOS, r6) =>
  Option.bind (decodeBytes1 r6) fun (targetArch, r7) =>
  Option.bind (decodeBytes2 r7) fun (appName, r8) =>
  Option.bind (decodeU16 r8) fun (variantCount, r9) =>
  Option.bind (decodeVariants variantCount.toNat r9) fun (variants, trailing) =>
  -- Exact consumption check (matching the v0.4.1 post-audit fix in internal/format/format.go)
  if !trailing.isEmpty then none
  else
    some {
      version := version,
      appName := appName,
      targetOS := targetOS,
      targetArch := targetArch,
      createdUnix := createdUnix,
      dictOffset := dictOffset,
      dictSize := dictSize,
      dictSHA256 := dictSHA.map toLowerASCII,
      dictID := dictID,
      variants := variants
    }

private theorem canonical_dict_props (idx : Index) (hc : idx.Canonical) :
    idx.dictSHA256.length < 256 ∧
    (idx.dictSize.toNat > 0 && (idx.dictSHA256.isEmpty || !validateChecksum idx.dictSHA256)) = false ∧
    idx.dictSHA256.map toLowerASCII = idx.dictSHA256 := by
  rcases hc with ⟨_, _, _, _, _, _, _, hdsha, _⟩
  by_cases hpos : idx.dictSize.toNat > 0
  · simp only [hpos, ↓reduceIte] at hdsha
    have hval := validateChecksum_of_canonical hdsha
    have hne : idx.dictSHA256.isEmpty = false := by
      cases h : idx.dictSHA256 with
      | nil => have := hdsha.1; simp [h] at this
      | cons _ _ => rfl
    have hlen : idx.dictSHA256.length < 256 := by
      have h64 := hdsha.1
      dsimp only [maxSHA256HexLen] at h64
      omega
    exact ⟨hlen, by simp [hpos, hne, hval], map_toLowerASCII_canonical hdsha.2⟩
  · simp only [hpos, ↓reduceIte] at hdsha
    simp [hpos, hdsha]

theorem marshal_unmarshal_roundtrip (idx : Index) (bs : List UInt8)
    (hc : idx.Canonical) (hm : marshalBinaryIndex idx = some bs) :
    unmarshalBinaryIndex bs = some idx := by
  have ⟨hdsLen, hdBad, hmapDict⟩ := canonical_dict_props idx hc
  rcases hc with ⟨hver, hos, harch, happ, hvlen, hdof, hdsz, _, hvars⟩
  simp only [marshalBinaryIndex, hos, harch, happ, hvlen, hdsLen, hdof, hdsz, hdBad,
    Bool.not_false, Bool.true_and, decide_true, ↓reduceIte] at hm
  cases hev : encodeVariants idx.variants with
  | none => simp [hev] at hm
  | some varBytes =>
      simp only [hev, Option.some.injEq] at hm
      subst hm
      have hlen34 : ¬((indexMagicV2 ++ encodeU16 formatVersion2 ++ encodeU64 idx.createdUnix ++
          encodeU64 idx.dictOffset ++ encodeU64 idx.dictSize ++ encodeU32 idx.dictID ++
          encodeBytes1 idx.dictSHA256 ++ encodeBytes1 idx.targetOS ++ encodeBytes1 idx.targetArch ++
          encodeBytes2 idx.appName ++ encodeU16 (UInt16.ofNat idx.variants.length) ++ varBytes).length < 34) := by
        simp [indexMagicV2, encodeU16, encodeU64, encodeU32, encodeBytes1, encodeBytes2]
      simp only [unmarshalBinaryIndex, List.append_assoc]
      have hlen34' : ¬((indexMagicV2 ++ (encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ varBytes))))))))))).length < 34) := by
        simpa [List.append_assoc] using hlen34
      simp only [hlen34', ↓reduceIte]
      have htake : (indexMagicV2 ++ (encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ varBytes))))))))))).take 4 =
          indexMagicV2 := rfl
      have hdrop : (indexMagicV2 ++ (encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ varBytes))))))))))).drop 4 =
          encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ varBytes))))))))) := rfl
      rw [htake, hdrop]
      simp only [bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      rw [decodeU16_encodeU16]
      simp only [Option.bind_some, bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      rw [decodeU64_encodeU64]; simp only [Option.bind_some]
      rw [decodeU64_encodeU64]; simp only [Option.bind_some]
      rw [decodeU64_encodeU64]; simp only [Option.bind_some]
      rw [decodeU32_encodeU32]; simp only [Option.bind_some]
      rw [decodeBytes1_encodeBytes1 _ _ hdsLen]
      simp only [Option.bind_some, hdBad, Bool.false_eq_true, ↓reduceIte]
      rw [decodeBytes1_encodeBytes1 _ _ hos]; simp only [Option.bind_some]
      rw [decodeBytes1_encodeBytes1 _ _ harch]; simp only [Option.bind_some]
      rw [decodeBytes2_encodeBytes2 _ _ happ]; simp only [Option.bind_some]
      rw [decodeU16_encodeU16]; simp only [Option.bind_some]
      have hvc : (UInt16.ofNat idx.variants.length).toNat = idx.variants.length := by
        simp; omega
      rw [hvc]
      have hdv := decodeVariants_encodeVariants idx.variants varBytes [] hvars hev
      rw [List.append_nil] at hdv
      rw [hdv]
      simp only [Option.bind_some, List.isEmpty_nil, Bool.not_true, Bool.false_eq_true, ↓reduceIte]
      cases idx
      simp only [Option.some.injEq, Index.mk.injEq, true_and, and_true]
      exact ⟨hver.symm, hmapDict⟩

theorem unmarshal_rejects_trailing_bytes (idx : Index) (bs suffix : List UInt8)
    (hc : idx.Canonical) (hm : marshalBinaryIndex idx = some bs) (hs : suffix ≠ []) :
    unmarshalBinaryIndex (bs ++ suffix) = none := by
  have ⟨hdsLen, hdBad, _⟩ := canonical_dict_props idx hc
  rcases hc with ⟨_, hos, harch, happ, hvlen, hdof, hdsz, _, hvars⟩
  simp only [marshalBinaryIndex, hos, harch, happ, hvlen, hdsLen, hdof, hdsz, hdBad,
    Bool.not_false, Bool.true_and, decide_true, ↓reduceIte] at hm
  cases hev : encodeVariants idx.variants with
  | none => simp [hev] at hm
  | some varBytes =>
      simp only [hev, Option.some.injEq] at hm
      subst hm
      have hlen34 : ¬((indexMagicV2 ++ encodeU16 formatVersion2 ++ encodeU64 idx.createdUnix ++
          encodeU64 idx.dictOffset ++ encodeU64 idx.dictSize ++ encodeU32 idx.dictID ++
          encodeBytes1 idx.dictSHA256 ++ encodeBytes1 idx.targetOS ++ encodeBytes1 idx.targetArch ++
          encodeBytes2 idx.appName ++ encodeU16 (UInt16.ofNat idx.variants.length) ++ varBytes ++
          suffix).length < 34) := by
        simp [indexMagicV2, encodeU16, encodeU64, encodeU32, encodeBytes1, encodeBytes2]
      simp only [unmarshalBinaryIndex, List.append_assoc]
      have hlen34' : ¬((indexMagicV2 ++ (encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ (varBytes ++ suffix)))))))))))).length < 34) := by
        simpa [List.append_assoc] using hlen34
      simp only [hlen34', ↓reduceIte]
      have htake : (indexMagicV2 ++ (encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ (varBytes ++ suffix)))))))))))).take 4 =
          indexMagicV2 := rfl
      have hdrop : (indexMagicV2 ++ (encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ (varBytes ++ suffix)))))))))))).drop 4 =
          encodeU16 formatVersion2 ++ (encodeU64 idx.createdUnix ++
          (encodeU64 idx.dictOffset ++ (encodeU64 idx.dictSize ++ (encodeU32 idx.dictID ++
          (encodeBytes1 idx.dictSHA256 ++ (encodeBytes1 idx.targetOS ++ (encodeBytes1 idx.targetArch ++
          (encodeBytes2 idx.appName ++ (encodeU16 (UInt16.ofNat idx.variants.length) ++ (varBytes ++ suffix)))))))))) := rfl
      rw [htake, hdrop]
      simp only [bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      rw [decodeU16_encodeU16]
      simp only [Option.bind_some, bne_self_eq_false, Bool.false_eq_true, ↓reduceIte]
      rw [decodeU64_encodeU64]; simp only [Option.bind_some]
      rw [decodeU64_encodeU64]; simp only [Option.bind_some]
      rw [decodeU64_encodeU64]; simp only [Option.bind_some]
      rw [decodeU32_encodeU32]; simp only [Option.bind_some]
      rw [decodeBytes1_encodeBytes1 _ _ hdsLen]
      simp only [Option.bind_some, hdBad, Bool.false_eq_true, ↓reduceIte]
      rw [decodeBytes1_encodeBytes1 _ _ hos]; simp only [Option.bind_some]
      rw [decodeBytes1_encodeBytes1 _ _ harch]; simp only [Option.bind_some]
      rw [decodeBytes2_encodeBytes2 _ _ happ]; simp only [Option.bind_some]
      rw [decodeU16_encodeU16]; simp only [Option.bind_some]
      have hvc : (UInt16.ofNat idx.variants.length).toNat = idx.variants.length := by
        simp; omega
      rw [hvc]
      rw [decodeVariants_encodeVariants idx.variants varBytes suffix hvars hev]
      have hne : suffix.isEmpty = false := by
        cases suffix with
        | nil => contradiction
        | cons _ _ => rfl
      simp only [Option.bind_some, hne, Bool.not_false, ↓reduceIte]

def indexSerializedLen (idx : Index) : Nat :=
  4 + 2 + 8 + 8 + 8 + 4 +
  (1 + idx.dictSHA256.length) +
  (1 + idx.targetOS.length) +
  (1 + idx.targetArch.length) +
  (2 + idx.appName.length) +
  2 +
  (idx.variants.map variantSerializedLen).sum

theorem unmarshal_exact_consumption (data : List UInt8) (idx : Index)
    (h : unmarshalBinaryIndex data = some idx) :
    data.length ≤ indexSerializedLen idx ∧
    data.length + 4 * idx.variants.length ≥ indexSerializedLen idx := by
  simp only [unmarshalBinaryIndex] at h
  split at h
  · contradiction
  · rename_i hge34
    split at h
    · contradiction
    · simp only [Option.bind_eq_some_iff] at h
      rcases h with ⟨⟨ver, r0⟩, h0, hrest0⟩
      have hl0 := decodeU16_len h0
      dsimp only at hrest0
      split at hrest0
      · contradiction
      · simp only [Option.bind_eq_some_iff] at hrest0
        rcases hrest0 with ⟨⟨cu, r1⟩, h1, ⟨doff, r2⟩, h2, ⟨dsz, r3⟩, h3, ⟨did, r4⟩, h4, ⟨dsha, r5⟩, h5, hrest1⟩
        have hl1 := decodeU64_len h1
        have hl2 := decodeU64_len h2
        have hl3 := decodeU64_len h3
        have hl4 := decodeU32_len h4
        have hl5 := (decodeBytes1_len h5).1
        dsimp only at hrest1
        split at hrest1
        · contradiction
        · simp only [Option.bind_eq_some_iff] at hrest1
          rcases hrest1 with ⟨⟨tos, r6⟩, h6, ⟨tarch, r7⟩, h7, ⟨app, r8⟩, h8, ⟨vc, r9⟩, h9, ⟨vars, trailing⟩, h10, hrest2⟩
          have hl6 := (decodeBytes1_len h6).1
          have hl7 := (decodeBytes1_len h7).1
          have hl8 := (decodeBytes2_len h8).1
          have hl9 := decodeU16_len h9
          have hl10 := decodeVariants_wire_bound vc.toNat r9 trailing vars h10
          dsimp only at hrest2
          split at hrest2
          · contradiction
          · rename_i htrail
            have htr0 : trailing.length = 0 := by
              cases trailing with
              | nil => rfl
              | cons _ _ => simp at htrail
            simp only [Option.some.injEq] at hrest2
            subst hrest2
            have hdrop4 : (data.drop 4).length = data.length - 4 := List.length_drop
            have hge4 : 4 ≤ data.length := by omega
            have hchain : (data.drop 4).length = 37 + dsha.length + tos.length + tarch.length + app.length + r9.length := by
              rw [hl0, hl1, hl2, hl3, hl4, hl5, hl6, hl7, hl8, hl9]
              omega
            simp only [indexSerializedLen, List.length_map]
            omega

/-! ### Interval Non-Overlapping & In-Bounds Proof (`ValidateBounds`) -/

def validateVariantSeq (indexOffset : Nat) : List VariantEntry → Nat → Bool
  | [], _ => true
  | v :: vs, lastEnd =>
      let off := v.offset.toNat
      let csz := v.compressedSize.toNat
      let usz := v.uncompressedSize.toNat
      (0 < csz) && (0 < usz) && (usz ≤ maxPayloadSize) &&
      (off ≤ indexOffset) && (csz ≤ indexOffset - off) &&
      (lastEnd ≤ off) &&
      (!v.sha256.isEmpty && validateChecksum v.sha256) &&
      validateVariantSeq indexOffset vs (off + csz)

def validateBounds (idx : Index) (indexOffset : Nat) : Bool :=
  (idx.version == formatVersion2) &&
  (!idx.variants.isEmpty) &&
  (idx.targetArch == archAMD64Bytes || idx.targetArch == archARM64Bytes) &&
  (idx.dictSize.toNat ≤ maxDictionarySize) &&
  (if idx.dictSize.toNat == 0 then
      validateVariantSeq indexOffset idx.variants 0
   else
      (!idx.dictSHA256.isEmpty && validateChecksum idx.dictSHA256) &&
      (idx.dictOffset.toNat ≤ indexOffset) &&
      (idx.dictSize.toNat ≤ indexOffset - idx.dictOffset.toNat) &&
      validateVariantSeq indexOffset idx.variants (idx.dictOffset.toNat + idx.dictSize.toNat))

def IntervalInBounds (start size bound : Nat) : Prop :=
  0 < size ∧ start + size ≤ bound

def OrderedDisjointSeq : List (Nat × Nat) → Nat → Prop
  | [], _ => True
  | (s, sz) :: rest, minStart =>
      minStart ≤ s ∧ 0 < sz ∧ OrderedDisjointSeq rest (s + sz)

theorem validateVariantSeq_sound (indexOffset : Nat) (vs : List VariantEntry) (lastEnd : Nat)
    (h : validateVariantSeq indexOffset vs lastEnd = true) :
    (∀ v ∈ vs, IntervalInBounds v.offset.toNat v.compressedSize.toNat indexOffset) ∧
    OrderedDisjointSeq (vs.map (fun v => (v.offset.toNat, v.compressedSize.toNat))) lastEnd := by
  induction vs generalizing lastEnd with
  | nil => simp [OrderedDisjointSeq]
  | cons v vs ih =>
      simp only [validateVariantSeq, Bool.and_eq_true, decide_eq_true_eq] at h
      rcases h with ⟨⟨⟨⟨⟨⟨⟨hcsz, husz⟩, _⟩, hoff⟩, hcbound⟩, hlast⟩, _⟩, hrest⟩
      have htail := ih (v.offset.toNat + v.compressedSize.toNat) hrest
      refine ⟨?_, ?_⟩
      · intro w hw
        simp only [List.mem_cons] at hw
        rcases hw with rfl | hw
        · exact ⟨hcsz, by omega⟩
        · exact htail.1 w hw
      · simp only [List.map_cons, OrderedDisjointSeq]
        exact ⟨hlast, hcsz, htail.2⟩

theorem validateBounds_disjoint_and_inbounds (idx : Index) (indexOffset : Nat)
    (h : validateBounds idx indexOffset = true) :
    (idx.dictSize.toNat > 0 →
      IntervalInBounds idx.dictOffset.toNat idx.dictSize.toNat indexOffset) ∧
    (∀ v ∈ idx.variants, IntervalInBounds v.offset.toNat v.compressedSize.toNat indexOffset) ∧
    OrderedDisjointSeq
      (idx.variants.map (fun v => (v.offset.toNat, v.compressedSize.toNat)))
      (if idx.dictSize.toNat == 0 then 0 else idx.dictOffset.toNat + idx.dictSize.toNat) := by
  simp only [validateBounds, Bool.and_eq_true, decide_eq_true_eq] at h
  rcases h with ⟨⟨⟨⟨_, _⟩, _⟩, _⟩, hbody⟩
  by_cases hdz : (idx.dictSize.toNat == 0) = true
  · have hdz0 : idx.dictSize.toNat = 0 := by simpa using hdz
    simp only [hdz, ↓reduceIte] at hbody ⊢
    have hvs := validateVariantSeq_sound indexOffset idx.variants 0 hbody
    exact ⟨fun hpos => by omega, hvs.1, hvs.2⟩
  · have hdz0 : idx.dictSize.toNat ≠ 0 := by simpa using hdz
    have hdzF : (idx.dictSize.toNat == 0) = false := by simp [hdz0]
    simp only [hdzF, Bool.false_eq_true, ↓reduceIte] at hbody ⊢
    simp only [Bool.and_eq_true, decide_eq_true_eq] at hbody
    rcases hbody with ⟨⟨⟨_, hdoff⟩, hdbnd⟩, hseq⟩
    have hvs := validateVariantSeq_sound indexOffset idx.variants
      (idx.dictOffset.toNat + idx.dictSize.toNat) hseq
    exact ⟨fun _ => ⟨by omega, by omega⟩, hvs.1, hvs.2⟩

end Microfat.FormatV2
