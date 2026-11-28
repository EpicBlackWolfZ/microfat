; SMT-LIB2 bit-vector proof for non-linear ARM64 microarchitecture DAG and policy rank caps.
; Matches internal/microarch/microarch.go and internal/microarch/policy_math.go.
;
; Expected output: sat, sat, unsat, unsat, unsat

(set-logic QF_BV)

; 24-bit ARM64 feature vector bit positions:
; 0:fp, 1:asimd, 2:atomics, 3:crc32, 4:fphp, 5:asimdhp, 6:jscvt, 7:fcma,
; 8:lrcpc, 9:dcpop, 10:asimddp, 11:dit, 12:i8mm, 13:bf16, 14:wfxt,
; 15:mops, 16:nmi, 17:hbc, 18:gcs, 19:the, 20:sve, 21:sve2, 22:sme, 23:sme2
(define-fun hasBit ((mask (_ BitVec 24)) (bitMask (_ BitVec 24))) Bool
  (not (= (bvand mask bitMask) #x000000)))

(define-fun bit ((idx (_ BitVec 24))) (_ BitVec 24)
  (bvshl #x000001 idx))

(define-fun sat_v8_0 ((m (_ BitVec 24))) Bool (and (hasBit m (bit #x000000)) (hasBit m (bit #x000001))))
(define-fun sat_v8_1 ((m (_ BitVec 24))) Bool (and (sat_v8_0 m) (hasBit m (bit #x000002)) (hasBit m (bit #x000003))))
(define-fun sat_v8_2 ((m (_ BitVec 24))) Bool (and (sat_v8_1 m) (hasBit m (bit #x000004)) (hasBit m (bit #x000005))))
(define-fun sat_v8_3 ((m (_ BitVec 24))) Bool (and (sat_v8_2 m) (hasBit m (bit #x000006)) (hasBit m (bit #x000007)) (hasBit m (bit #x000008))))
(define-fun sat_v8_4 ((m (_ BitVec 24))) Bool (and (sat_v8_3 m) (hasBit m (bit #x000009)) (hasBit m (bit #x00000a))))
(define-fun sat_v8_5 ((m (_ BitVec 24))) Bool (and (sat_v8_4 m) (hasBit m (bit #x00000b))))
(define-fun sat_v8_6 ((m (_ BitVec 24))) Bool (and (sat_v8_5 m) (hasBit m (bit #x00000c)) (hasBit m (bit #x00000d))))
(define-fun sat_v8_7 ((m (_ BitVec 24))) Bool (and (sat_v8_6 m) (hasBit m (bit #x00000e))))
(define-fun sat_v8_8 ((m (_ BitVec 24))) Bool (and (sat_v8_7 m) (hasBit m (bit #x00000f)) (hasBit m (bit #x000010)) (hasBit m (bit #x000011))))
(define-fun sat_v8_9 ((m (_ BitVec 24))) Bool (and (sat_v8_8 m) (hasBit m (bit #x000012)) (hasBit m (bit #x000013))))
(define-fun sat_v9_0 ((m (_ BitVec 24))) Bool (and (sat_v8_5 m) (hasBit m (bit #x000014))))
(define-fun sat_v9_1 ((m (_ BitVec 24))) Bool (and (sat_v9_0 m) (sat_v8_6 m)))
(define-fun sat_v9_2 ((m (_ BitVec 24))) Bool (and (sat_v9_1 m) (hasBit m (bit #x000015))))

(declare-const hostFeatures (_ BitVec 24))

; 1. Non-linear DAG check A: v9.0 (SVE) does not imply v8.6 (I8MM+BF16).
(push 1)
(assert (sat_v9_0 hostFeatures))
(assert (not (sat_v8_6 hostFeatures)))
(check-sat)
(pop 1)

; 2. Non-linear DAG check B: v8.6 (I8MM+BF16) does not imply v9.0 (SVE).
(push 1)
(assert (sat_v8_6 hostFeatures))
(assert (not (sat_v9_0 hostFeatures)))
(check-sat)
(pop 1)

; 3. v9.1 requires both v9.0 and v8.6 prerequisites simultaneously.
(push 1)
(assert (sat_v9_1 hostFeatures))
(assert (not (and (sat_v9_0 hostFeatures) (sat_v8_6 hostFeatures))))
(check-sat)
(pop 1)

; 4. Conservative native Linux safety: when nmi=0 and the=0, neither v8.8 nor v8.9 can be satisfied.
(push 1)
(assert (not (hasBit hostFeatures (bit #x000010)))) ; nmi unobservable -> false
(assert (not (hasBit hostFeatures (bit #x000013)))) ; the unobservable -> false
(assert (or (sat_v8_8 hostFeatures) (sat_v8_9 hostFeatures)))
(check-sat)
(pop 1)

; 5. Policy max-rank capping & exclusion guard (32-bit signed ranks):
(declare-const hostRank (_ BitVec 32))
(declare-const explicitMaxRank (_ BitVec 32))
(declare-const hasExplicitMax Bool)
(declare-const applyAVX512Cap Bool)
(declare-const avx512CapRank (_ BitVec 32))
(declare-const candRank (_ BitVec 32))

(define-fun maxAfterExplicit () (_ BitVec 32)
  (ite (and hasExplicitMax (bvsge explicitMaxRank #x00000000) (bvslt explicitMaxRank hostRank))
    explicitMaxRank hostRank))
(define-fun effectiveMax () (_ BitVec 32)
  (ite (and applyAVX512Cap (bvsge avx512CapRank #x00000000) (bvslt avx512CapRank maxAfterExplicit))
    avx512CapRank maxAfterExplicit))
(define-fun candAllowed () Bool
  (and (bvsge candRank #x00000000) (bvsle candRank hostRank) (bvsle candRank effectiveMax)))

(push 1)
(assert (bvsge hostRank #x00000000))
(assert candAllowed)
(assert (not (and
  (bvsle candRank hostRank)
  (=> (and hasExplicitMax (bvsge explicitMaxRank #x00000000)) (bvsle candRank explicitMaxRank))
  (=> (and applyAVX512Cap (bvsge avx512CapRank #x00000000)) (bvsle candRank avx512CapRank)))))
(check-sat)
(pop 1)
