import Microfat.FormatV2
import Microfat.Microarch
import Microfat.CgroupMath
import Microfat.ElfFilter

/-!
Root module importing all formally verified `microfat` invariants and checking exact
axiom cleanliness across every headline theorem.
-/

#print axioms Microfat.FormatV2.marshal_unmarshal_roundtrip
#print axioms Microfat.FormatV2.unmarshal_rejects_trailing_bytes
#print axioms Microfat.FormatV2.unmarshal_exact_consumption
#print axioms Microfat.FormatV2.validateBounds_disjoint_and_inbounds

#print axioms Microfat.Microarch.amd64_eval_rank_iff
#print axioms Microfat.Microarch.amd64_feature_monotonicity
#print axioms Microfat.Microarch.arm64_prereqs_acyclic
#print axioms Microfat.Microarch.arm64_feature_monotonicity
#print axioms Microfat.Microarch.select_sigill_free
#print axioms Microfat.Microarch.select_optimal_unforced

#print axioms Microfat.CgroupMath.retainedMemoryLimit_le
#print axioms Microfat.CgroupMath.extractionMemory_no_overflow
#print axioms Microfat.CgroupMath.effectiveMemoryLimit_le_both
#print axioms Microfat.CgroupMath.gomemlimit_le_limit
#print axioms Microfat.CgroupMath.gomemlimit_headroom_le_half
#print axioms Microfat.CgroupMath.gomemlimit_monotone
#print axioms Microfat.CgroupMath.admitWorkers_sum_le_budget

#print axioms Microfat.ElfFilter.decode_encodeU32LEBytes
#print axioms Microfat.ElfFilter.encode_decodeU32LEBytes
#print axioms Microfat.ElfFilter.arm64_word_roundtrip
#print axioms Microfat.ElfFilter.arm64_bytes_length
#print axioms Microfat.ElfFilter.arm64_bytes_roundtrip
#print axioms Microfat.ElfFilter.arm64_bytes_inverse_roundtrip
#print axioms Microfat.ElfFilter.x86_length_preserved
#print axioms Microfat.ElfFilter.x86_roundtrip
#print axioms Microfat.ElfFilter.x86_inverse_roundtrip
#print axioms Microfat.ElfFilter.x86_chunk_local_reassembly
#print axioms Microfat.ElfFilter.arm64_chunk_local_reassembly
#print axioms Microfat.ElfFilter.x86_dedup_multi_offset_sound
#print axioms Microfat.ElfFilter.arm64_dedup_multi_offset_sound
#print axioms Microfat.ElfFilter.arm64_aligned_split_commutes
