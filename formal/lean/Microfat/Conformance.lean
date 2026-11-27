import Microfat.FormatV2
import Microfat.Microarch
import Microfat.CgroupMath
import Microfat.ElfFilter

/-!
Canonical cross-language conformance test vector generator.
Executes verified Lean 4 specification functions and emits deterministic JSON
for execution by Go (`internal/conformance`) and Rust (`tests/conformance.rs`).
-/

open Microfat.FormatV2
open Microfat.Microarch
open Microfat.CgroupMath
open Microfat.ElfFilter

namespace Microfat.Conformance

def hexDigit (n : Nat) : Char :=
  if n < 10 then Char.ofNat ('0'.toNat + n)
  else Char.ofNat ('a'.toNat + (n - 10))

def byteToHex (b : UInt8) : String :=
  let v := b.toNat
  String.ofList [hexDigit (v / 16), hexDigit (v % 16)]

def bytesToHex (bs : List UInt8) : String :=
  String.join (bs.map byteToHex)

def jsonStr (s : String) : String :=
  "\"" ++ s ++ "\""

def jsonBool (b : Bool) : String :=
  if b then "true" else "false"

def jsonOptNat : Option Nat → String
  | none => "null"
  | some n => toString n

def jsonOptStr : Option String → String
  | none => "null"
  | some s => jsonStr s

def jsonArray (items : List String) : String :=
  "[\n    " ++ String.intercalate ",\n    " items ++ "\n  ]"

def strBytes (s : String) : List UInt8 :=
  s.toUTF8.toList

def repHex (c : Char) : List UInt8 :=
  List.replicate 64 (UInt8.ofNat c.toNat)

/-! ### Microarchitecture Bitmask Helpers -/

def bitSet (mask bit : Nat) : Bool :=
  (mask &&& (1 <<< bit)) != 0

def x86FromMask (m : Nat) : X86Features :=
  { hasLAHFSAHF := bitSet m 0
    hasCX16     := bitSet m 1
    hasPOPCNT   := bitSet m 2
    hasSSE3     := bitSet m 3
    hasSSSE3    := bitSet m 4
    hasSSE41    := bitSet m 5
    hasSSE42    := bitSet m 6
    hasAVX      := bitSet m 7
    hasAVX2     := bitSet m 8
    hasBMI1     := bitSet m 9
    hasBMI2     := bitSet m 10
    hasFMA      := bitSet m 11
    hasOSXSAVE  := bitSet m 12
    hasF16C     := bitSet m 13
    hasLZCNT    := bitSet m 14
    hasMOVBE    := bitSet m 15
    hasAVX512F  := bitSet m 16
    hasAVX512BW := bitSet m 17
    hasAVX512CD := bitSet m 18
    hasAVX512DQ := bitSet m 19
    hasAVX512VL := bitSet m 20 }

def amd64LevelStr : AMD64Level → String
  | .v1 => "v1"
  | .v2 => "v2"
  | .v3 => "v3"
  | .v4 => "v4"

def arm64FromMask (m : Nat) : ARM64Features :=
  { hasFP      := bitSet m 0
    hasASIMD   := bitSet m 1
    hasATOMICS := bitSet m 2
    hasCRC32   := bitSet m 3
    hasFPHP    := bitSet m 4
    hasASIMDHP := bitSet m 5
    hasJSCVT   := bitSet m 6
    hasFCMA    := bitSet m 7
    hasLRCPC   := bitSet m 8
    hasDCPOP   := bitSet m 9
    hasASIMDDP := bitSet m 10
    hasDIT     := bitSet m 11
    hasI8MM    := bitSet m 12
    hasBF16    := bitSet m 13
    hasWFxT    := bitSet m 14
    hasMOPS    := bitSet m 15
    hasNMI     := bitSet m 16
    hasHBC     := bitSet m 17
    hasGCS     := bitSet m 18
    hasTHE     := bitSet m 19
    hasSVE     := bitSet m 20
    hasSVE2    := bitSet m 21
    hasSME     := bitSet m 22
    hasSME2    := bitSet m 23 }

def allArm64Levels : List (Nat × ARM64Level × String) :=
  [ (0,  .v8_0, "v8.0"), (1,  .v8_1, "v8.1"), (2,  .v8_2, "v8.2"), (3,  .v8_3, "v8.3")
  , (4,  .v8_4, "v8.4"), (5,  .v8_5, "v8.5"), (6,  .v8_6, "v8.6"), (7,  .v8_7, "v8.7")
  , (8,  .v8_8, "v8.8"), (9,  .v8_9, "v8.9"), (10, .v9_0, "v9.0"), (11, .v9_1, "v9.1")
  , (12, .v9_2, "v9.2"), (13, .v9_3, "v9.3"), (14, .v9_4, "v9.4"), (15, .v9_5, "v9.5")
  ]

def evalArm64SupportedMask (f : ARM64Features) : Nat :=
  allArm64Levels.foldl (fun acc (bit, lvl, _) =>
    if arm64Satisfied lvl f then acc ||| (1 <<< bit) else acc) 0

def evalArm64Highest (f : ARM64Features) : String :=
  allArm64Levels.reverse.findSome? (fun (_, lvl, name) =>
    if arm64Satisfied lvl f then some name else none) |>.getD "v8.0"

def levelStr : Level → String
  | .amd64 l => amd64LevelStr l
  | .arm64 l =>
      match allArm64Levels.find? (fun (_, al, _) => al == l) with
      | some (_, _, s) => s
      | none => "unknown"

def archStr : Arch → String
  | .amd64 => "amd64"
  | .arm64 => "arm64"

/-! ### Vector Builders -/

structure FormatCase where
  name        : String
  indexOffset : Nat
  wireHex     : String
  validIndex  : Bool
  boundsValid : Bool

def mkVariant (lvl : String) (off csz usz : Nat) (hexChar : Char) (comp : String) : VariantEntry :=
  { level := strBytes lvl
    offset := UInt64.ofNat off
    compressedSize := UInt64.ofNat csz
    uncompressedSize := UInt64.ofNat usz
    sha256 := repHex hexChar
    compression := strBytes comp }

def formatCases : List FormatCase :=
  let amdIdx : Index :=
    { version := formatVersion2
      appName := strBytes "demo"
      targetOS := strBytes "linux"
      targetArch := archAMD64Bytes
      createdUnix := 1700000000
      dictOffset := 0
      dictSize := 0
      dictSHA256 := []
      dictID := 0
      variants :=
        [ mkVariant "v1" 1000 100 200 'a' "zstd"
        , mkVariant "v2" 1100 120 250 'b' "lz4"
        , mkVariant "v4" 1220 150 300 'c' "none"
        ] }
  let armIdx : Index :=
    { version := formatVersion2
      appName := strBytes "arm-app"
      targetOS := strBytes "linux"
      targetArch := archARM64Bytes
      createdUnix := 1700000001
      dictOffset := 0
      dictSize := 0
      dictSHA256 := []
      dictID := 0
      variants :=
        [ mkVariant "v8.0" 1000 100 200 '1' "none"
        , mkVariant "v8.6" 1100 100 200 '2' "zstd"
        , mkVariant "v9.0" 1200 100 200 '3' "lz4"
        , mkVariant "v9.1" 1300 100 200 '4' "zstd"
        ] }
  let dictIdx : Index :=
    { version := formatVersion2
      appName := strBytes "dict-app"
      targetOS := strBytes "linux"
      targetArch := archAMD64Bytes
      createdUnix := 1700000002
      dictOffset := 500
      dictSize := 200
      dictSHA256 := repHex 'F'
      dictID := 42
      variants := [ mkVariant "v1" 700 100 200 'A' "zstd" ] }
  let overlapIdx : Index :=
    { amdIdx with variants :=
        [ mkVariant "v1" 1000 150 200 'a' "zstd"
        , mkVariant "v2" 1100 100 200 'b' "zstd"
        ] }
  let dictOobIdx : Index :=
    { dictIdx with dictOffset := 900, dictSize := 200 }
  let varOobIdx : Index :=
    { amdIdx with variants := [ mkVariant "v1" 1950 100 200 'a' "zstd" ] }

  let amdBytes := (marshalBinaryIndex amdIdx).getD []
  let armBytes := (marshalBinaryIndex armIdx).getD []
  let dictBytes := (marshalBinaryIndex dictIdx).getD []
  let badMagicBytes := (UInt8.ofNat 0xFF) :: amdBytes.drop 1
  let trailingBytes := amdBytes ++ [UInt8.ofNat 0x99]
  let overlapBytes := (marshalBinaryIndex overlapIdx).getD []
  let dictOobBytes := (marshalBinaryIndex dictOobIdx).getD []
  let varOobBytes := (marshalBinaryIndex varOobIdx).getD []

  let eval (name : String) (off : Nat) (wire : List UInt8) : FormatCase :=
    let parsed := unmarshalBinaryIndex wire
    { name := name
      indexOffset := off
      wireHex := bytesToHex wire
      validIndex := parsed.isSome
      boundsValid := match parsed with
        | some i => validateBounds i off
        | none => false }
  [ eval "amd64_multi_variant" 2000 amdBytes
  , eval "arm64_nonlinear_dag" 2000 armBytes
  , eval "dict_uppercase_hex_norm" 1000 dictBytes
  , eval "reject_bad_magic" 2000 badMagicBytes
  , eval "reject_trailing_bytes" 2000 trailingBytes
  , eval "reject_overlapping_variants" 2000 overlapBytes
  , eval "reject_dict_past_index" 1000 dictOobBytes
  , eval "reject_variant_past_index" 2000 varOobBytes
  ]

def renderFormatCase (c : FormatCase) : String :=
  "{" ++
  "\"name\": " ++ jsonStr c.name ++ ", " ++
  "\"index_offset\": " ++ toString c.indexOffset ++ ", " ++
  "\"wire_hex\": " ++ jsonStr c.wireHex ++ ", " ++
  "\"valid_index\": " ++ jsonBool c.validIndex ++ ", " ++
  "\"bounds_valid\": " ++ jsonBool c.boundsValid ++
  "}"

def v2Mask : Nat := 0x7F
def v3OnlyMask : Nat := 0xFF80
def v4OnlyMask : Nat := 0x1F0000
def allX86Mask : Nat := v2Mask ||| v3OnlyMask ||| v4OnlyMask

def amd64Masks : List (String × Nat) :=
  [ ("empty", 0)
  , ("missing_cx16", v2Mask &&& (allX86Mask ^^^ (1 <<< 1)))
  , ("exact_v2", v2Mask)
  , ("missing_osxsave", (v2Mask ||| v3OnlyMask) &&& (allX86Mask ^^^ (1 <<< 12)))
  , ("v2_plus_v4_no_v3", v2Mask ||| v4OnlyMask)
  , ("exact_v3", v2Mask ||| v3OnlyMask)
  , ("missing_avx512vl", allX86Mask &&& (allX86Mask ^^^ (1 <<< 20)))
  , ("all_v4", allX86Mask)
  ]

def renderAmd64Case (pair : String × Nat) : String :=
  let lvl := evaluateAMD64 (x86FromMask pair.2)
  "{" ++
  "\"name\": " ++ jsonStr pair.1 ++ ", " ++
  "\"mask\": " ++ toString pair.2 ++ ", " ++
  "\"level\": " ++ jsonStr (amd64LevelStr lvl) ++ ", " ++
  "\"rank\": " ++ toString (amd64Rank lvl) ++
  "}"

def armBase85Mask : Nat := (1 <<< 12) - 1
def armAll24Mask : Nat := (1 <<< 24) - 1
def armUserspaceAll : Nat := armAll24Mask &&& (armAll24Mask ^^^ ((1 <<< 16) ||| (1 <<< 19)))

def arm64Masks : List (String × Nat) :=
  [ ("none", 0)
  , ("v8_0_base", (1 <<< 0) ||| (1 <<< 1))
  , ("v8_4_missing_dit", (1 <<< 11) - 1)
  , ("v8_5", armBase85Mask)
  , ("v9_0_without_v8_6", armBase85Mask ||| (1 <<< 20))
  , ("v9_1_requires_both", armBase85Mask ||| (1 <<< 12) ||| (1 <<< 13) ||| (1 <<< 20))
  , ("v8_7_without_mops", armBase85Mask ||| (1 <<< 12) ||| (1 <<< 13) ||| (1 <<< 14))
  , ("userspace_all_v9_5_blocks_v8_8_v8_9", armUserspaceAll)
  , ("all_24_bits", armAll24Mask)
  ]

def renderArm64Case (pair : String × Nat) : String :=
  let f := arm64FromMask pair.2
  "{" ++
  "\"name\": " ++ jsonStr pair.1 ++ ", " ++
  "\"mask\": " ++ toString pair.2 ++ ", " ++
  "\"supported_mask\": " ++ toString (evalArm64SupportedMask f) ++ ", " ++
  "\"highest_level\": " ++ jsonStr (evalArm64Highest f) ++
  "}"

structure SelectionCase where
  name         : String
  arch         : Arch
  x86Mask      : Nat
  armMask      : Nat
  available    : List Level
  forceLevel   : Option Level
  maxLevel     : Option Level
  disabled     : List Level

def selectionCases : List SelectionCase :=
  let v1 := Level.amd64 .v1
  let v2 := Level.amd64 .v2
  let v3 := Level.amd64 .v3
  let v4 := Level.amd64 .v4
  let a80 := Level.arm64 .v8_0
  let a85 := Level.arm64 .v8_5
  let a86 := Level.arm64 .v8_6
  let a90 := Level.arm64 .v9_0
  let a91 := Level.arm64 .v9_1
  let m90 := armBase85Mask ||| (1 <<< 20)
  [ { name := "amd64_default_picks_v3", arch := .amd64, x86Mask := v2Mask ||| v3OnlyMask, armMask := 0
      available := [v1, v2, v3, v4], forceLevel := none, maxLevel := none, disabled := [] }
  , { name := "amd64_force_v1_picks_v1", arch := .amd64, x86Mask := v2Mask ||| v3OnlyMask, armMask := 0
      available := [v1, v2, v3], forceLevel := some v1, maxLevel := none, disabled := [] }
  , { name := "amd64_force_v4_on_v3_refuses", arch := .amd64, x86Mask := v2Mask ||| v3OnlyMask, armMask := 0
      available := [v1, v2, v3, v4], forceLevel := some v4, maxLevel := none, disabled := [] }
  , { name := "amd64_max_v2_caps_at_v2", arch := .amd64, x86Mask := allX86Mask, armMask := 0
      available := [v1, v2, v3, v4], forceLevel := none, maxLevel := some v2, disabled := [] }
  , { name := "amd64_disable_v3_v4_picks_v2", arch := .amd64, x86Mask := allX86Mask, armMask := 0
      available := [v1, v2, v3, v4], forceLevel := none, maxLevel := none, disabled := [v3, v4] }
  , { name := "arm64_nonlinear_v9_0_picks_v9_0", arch := .arm64, x86Mask := 0, armMask := m90
      available := [a80, a86, a90, a91], forceLevel := none, maxLevel := none, disabled := [] }
  , { name := "arm64_nonlinear_fallback_skips_v8_6_for_v8_5", arch := .arm64, x86Mask := 0, armMask := m90
      available := [a80, a85, a86, a91], forceLevel := none, maxLevel := none, disabled := [] }
  ]

def renderSelectionCase (c : SelectionCase) : String :=
  let host : HostInfo :=
    { arch := c.arch
      x86 := x86FromMask c.x86Mask
      arm64 := arm64FromMask c.armMask
      isSkylakeXOrCascadeLake := false }
  let policy : Policy :=
    { forceLevel := c.forceLevel
      maxLevel := c.maxLevel
      disabledVariants := c.disabled
      avx512DownclockProtection := false }
  let selected := (selectVariantForHost c.arch host c.available policy).map levelStr
  let availJson := "[" ++ String.intercalate ", " (c.available.map (fun l => jsonStr (levelStr l))) ++ "]"
  let disJson := "[" ++ String.intercalate ", " (c.disabled.map (fun l => jsonStr (levelStr l))) ++ "]"
  "{" ++
  "\"name\": " ++ jsonStr c.name ++ ", " ++
  "\"arch\": " ++ jsonStr (archStr c.arch) ++ ", " ++
  "\"x86_mask\": " ++ toString c.x86Mask ++ ", " ++
  "\"arm64_mask\": " ++ toString c.armMask ++ ", " ++
  "\"available\": " ++ availJson ++ ", " ++
  "\"force_level\": " ++ jsonOptStr (c.forceLevel.map levelStr) ++ ", " ++
  "\"max_level\": " ++ jsonOptStr (c.maxLevel.map levelStr) ++ ", " ++
  "\"disabled\": " ++ disJson ++ ", " ++
  "\"expected_selected\": " ++ jsonOptStr selected ++
  "}"

def retainedCases : List (Nat × Nat) :=
  [ (1000, 300), (1000, 1000), (1000, 1500), (0, 0), (CgroupMath.maxInt64Nat, 1) ]

def renderRetainedCase (p : Nat × Nat) : String :=
  "{" ++
  "\"limit\": " ++ toString p.1 ++ ", " ++
  "\"retained\": " ++ toString p.2 ++ ", " ++
  "\"expected\": " ++ toString (retainedMemoryLimit p.1 p.2) ++
  "}"

def extractionCases : List (Nat × Nat × Nat × Nat) :=
  [ (10, 20, 30, 40)
  , (CgroupMath.maxInt64Nat, 0, 0, 0)
  , (CgroupMath.maxInt64Nat, 1, 0, 0)
  , (CgroupMath.maxInt64Nat - 10, 5, 5, 1)
  , (CgroupMath.maxInt64Nat - 10, 5, 5, 0)
  ]

def renderExtractionCase (t : Nat × Nat × Nat × Nat) : String :=
  let (p, d, dec, res) := t
  "{" ++
  "\"payload\": " ++ toString p ++ ", " ++
  "\"dictionary\": " ++ toString d ++ ", " ++
  "\"decoder\": " ++ toString dec ++ ", " ++
  "\"reserve\": " ++ toString res ++ ", " ++
  "\"expected\": " ++ jsonOptNat (extractionMemory p d dec res) ++
  "}"

def effectiveCases : List (Option Nat × Option Nat) :=
  [ (some 500, some 300)
  , (some 300, some 500)
  , (some 500, none)
  , (none, some 300)
  , (none, none)
  ]

def renderEffectiveCase (p : Option Nat × Option Nat) : String :=
  "{" ++
  "\"max_bytes\": " ++ jsonOptNat p.1 ++ ", " ++
  "\"high_bytes\": " ++ jsonOptNat p.2 ++ ", " ++
  "\"expected\": " ++ jsonOptNat (calculateEffectiveMemoryLimit p.1 p.2) ++
  "}"

def mib (n : Nat) : Nat := n * 1024 * 1024

def gomemlimitCases : List (Nat × Nat × Nat) :=
  [ (mib 16,  9000, mib 64)
  , (mib 64,  9000, mib 64)
  , (mib 128, 9000, mib 64)
  , (mib 512, 9000, mib 64)
  , (mib 2048, 9000, mib 64)
  , (mib 2048, 8000, mib 64)
  , (mib 512, 5000, mib 64)
  , (mib 256, 10000, mib 64)
  ]

def renderGomemlimitCase (t : Nat × Nat × Nat) : String :=
  let (lim, ratioBps, minHeadroom) := t
  let got := calculateGOMEMLIMIT lim ratioBps 10000 minHeadroom
  "{" ++
  "\"limit\": " ++ toString lim ++ ", " ++
  "\"ratio_bps\": " ++ toString ratioBps ++ ", " ++
  "\"min_headroom\": " ++ toString minHeadroom ++ ", " ++
  "\"expected\": " ++ toString got ++
  "}"

def zstdDecoderCases : List (Nat × Nat × Nat × Nat) :=
  [ (mib 8, mib 128, mib 4, mib 512)
  , (256, 256, 0, 512)
  , (256, 256, 1, 512)
  , (0, mib 128, mib 4, mib 512)
  , (1, 0, mib 4, mib 512)
  , (mib 128 + 1, mib 128, mib 4, mib 512)
  , (CgroupMath.maxInt64Nat, CgroupMath.maxInt64Nat, 0, CgroupMath.maxInt64Nat)
  ]

def renderZstdCase (t : Nat × Nat × Nat × Nat) : String :=
  let (win, maxWin, ovh, maxDec) := t
  "{" ++
  "\"window\": " ++ toString win ++ ", " ++
  "\"max_window\": " ++ toString maxWin ++ ", " ++
  "\"overhead\": " ++ toString ovh ++ ", " ++
  "\"max_decoder\": " ++ toString maxDec ++ ", " ++
  "\"expected\": " ++ jsonOptNat (zstdDecoderMemory win maxWin ovh maxDec) ++
  "}"

def x86InputBytes : List UInt8 :=
  [ 0x55, 0xE8, 0x10, 0x00, 0x00, 0x00, 0x48, 0x8D, 0x05, 0x20, 0x01, 0x00, 0x00
  , 0x0F, 0x84, 0x08, 0x00, 0x00, 0x00, 0x48, 0x8D, 0x00, 0xE8
  ]

def arm64InputBytes : List UInt8 :=
  [ 0x01, 0x00, 0x00, 0x94
  , 0xFE, 0xFF, 0xFF, 0x17
  , 0x00, 0x00, 0x00, 0x90
  , 0x1F, 0x20, 0x03, 0xD5
  , 0xAA, 0xBB
  ]

def renderElfCase (name arch : String) (basePos : UInt32) (input encoded : List UInt8) : String :=
  "{" ++
  "\"name\": " ++ jsonStr name ++ ", " ++
  "\"arch\": " ++ jsonStr arch ++ ", " ++
  "\"base_pos\": " ++ toString basePos.toNat ++ ", " ++
  "\"input_hex\": " ++ jsonStr (bytesToHex input) ++ ", " ++
  "\"encoded_hex\": " ++ jsonStr (bytesToHex encoded) ++
  "}"

def elfCases : List String :=
  [ renderElfCase "x86_64_bcj_rip_pos_0" "x86_64" 0 x86InputBytes (x86Transform true x86InputBytes 0)
  , renderElfCase "x86_64_bcj_rip_pos_4096" "x86_64" 4096 x86InputBytes (x86Transform true x86InputBytes 4096)
  , renderElfCase "arm64_bl_b_adrp_pos_4096" "arm64" 0x1000 arm64InputBytes (arm64Transform true arm64InputBytes 0x1000)
  ]

def renderAll : String :=
  "{\n" ++
  "  \"format_v2\": " ++ jsonArray (formatCases.map renderFormatCase) ++ ",\n" ++
  "  \"microarch_amd64\": " ++ jsonArray (amd64Masks.map renderAmd64Case) ++ ",\n" ++
  "  \"microarch_arm64\": " ++ jsonArray (arm64Masks.map renderArm64Case) ++ ",\n" ++
  "  \"microarch_selection\": " ++ jsonArray (selectionCases.map renderSelectionCase) ++ ",\n" ++
  "  \"cgroup_retained_memory\": " ++ jsonArray (retainedCases.map renderRetainedCase) ++ ",\n" ++
  "  \"cgroup_extraction_memory\": " ++ jsonArray (extractionCases.map renderExtractionCase) ++ ",\n" ++
  "  \"cgroup_effective_memory\": " ++ jsonArray (effectiveCases.map renderEffectiveCase) ++ ",\n" ++
  "  \"cgroup_gomemlimit\": " ++ jsonArray (gomemlimitCases.map renderGomemlimitCase) ++ ",\n" ++
  "  \"cgroup_zstd_decoder_memory\": " ++ jsonArray (zstdDecoderCases.map renderZstdCase) ++ ",\n" ++
  "  \"elf_filter\": " ++ jsonArray elfCases ++ "\n" ++
  "}"

end Microfat.Conformance

def main : IO Unit :=
  IO.println Microfat.Conformance.renderAll
