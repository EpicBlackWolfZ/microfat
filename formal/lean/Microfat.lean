import Microfat.FormatV2
import Microfat.Microarch
import Microfat.CgroupMath
import Microfat.ElfFilter
import Microfat.DispatchState
import Microfat.InstallTx

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
#print axioms Microfat.CgroupMath.zstdDecoderMemory_iff
#print axioms Microfat.CgroupMath.zstdDecoderMemory_le_maxDecoder
#print axioms Microfat.CgroupMath.zstdDecoderMemory_monotone

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

#print axioms Microfat.DispatchState.seals_step_monotone
#print axioms Microfat.DispatchState.applyAllTargetSeals_complete_and_monotone
#print axioms Microfat.DispatchState.auto_unsafe_entry_fails_closed_no_mutation
#print axioms Microfat.DispatchState.auto_warm_hit_read_only_no_mutation
#print axioms Microfat.DispatchState.auto_safe_miss_memfd_ok_leaves_cache_untouched
#print axioms Microfat.DispatchState.auto_corrupt_or_budget_never_materializes_cache
#print axioms Microfat.DispatchState.explicit_memfd_never_touches_cache
#print axioms Microfat.DispatchState.explicit_cache_never_uses_memfd
#print axioms Microfat.DispatchState.exec_mode_policy_preserved_and_dispatch_bounded

#print axioms Microfat.InstallTx.pre_activation_failure_preserves_active_and_cleans_staging
#print axioms Microfat.InstallTx.reuse_identical_generation_preserves_active
#print axioms Microfat.InstallTx.existing_generation_never_overwritten
#print axioms Microfat.InstallTx.successful_activation_switches_atomically
#print axioms Microfat.InstallTx.rollback_restores_previous_active_generation
