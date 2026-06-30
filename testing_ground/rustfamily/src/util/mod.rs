//! Utility module root reached via `mod.rs` (nested module). Declares the leaf
//! `math` module, a shared `Unit` type, and a crate-visible constant. The
//! `use self::math::circumference` forms a deliberate import cycle with
//! `math.rs` (which does `use super::Unit`).

pub mod math;

use self::math::circumference;

/// A unit of measurement marker (pub visibility).
pub struct Unit {
    pub name: &'static str,
}

/// Tau, two times pi (pub(crate) visibility).
pub(crate) const TAU: f64 = 6.28318;

/// A module-private free function (private visibility).
fn internal_scale() -> f64 {
    2.0
}

impl Unit {
    /// Construct a named unit (associated constructor).
    pub fn new(name: &'static str) -> Self {
        Unit { name }
    }

    /// Compute a circumference via the `math` submodule (cross-module `calls`).
    pub fn around(&self, r: f64) -> f64 {
        circumference(r)
    }
}
