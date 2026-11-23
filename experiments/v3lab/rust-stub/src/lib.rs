#![cfg_attr(not(test), no_std)]
#![forbid(unsafe_code)]

//! Dependency-free verified core for the experimental `microfat` Rust launcher stub.
//! Implements Format v2 trailer/index validation, streaming SHA-256, AMD64 feature evaluation,
//! and reversible ELF/ISA branch filters verified with Kani and Lean 4 conformance vectors.

pub mod elffilter;
pub mod format;
pub mod microarch;
pub mod sha256;
