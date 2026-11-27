#![cfg_attr(not(test), no_std)]
#![forbid(unsafe_code)]

//! Dependency-free verified core for the experimental `microfat` Rust launcher stub.
//! Implements Format v2 trailer/index validation, streaming SHA-256, AMD64 feature evaluation,
//! and reversible ELF/ISA branch filters verified with Kani, Verus, and Lean 4 conformance vectors.

#[cfg(verus_keep_ghost)]
use vstd::prelude::*;

pub mod cgroup;
#[cfg(not(verus_keep_ghost))]
pub mod elffilter;
#[cfg(not(verus_keep_ghost))]
pub mod format;
#[cfg(not(verus_keep_ghost))]
pub mod microarch;
#[cfg(not(verus_keep_ghost))]
pub mod sha256;

#[cfg(all(test, not(verus_keep_ghost)))]
mod conformance_tests;
