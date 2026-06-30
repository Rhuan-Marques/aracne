//! Math constants and helpers (a leaf module under `util`, module path
//! `rustfamily::util::math`). Demonstrates `use super::...`, a `const`, a
//! `static`, and the three visibility variants (pub / pub(crate) / private).

use super::Unit;

/// Pi, visible across the crate (pub(crate) const).
pub(crate) const PI: f64 = 3.14159;

/// A module-private tolerance (private const).
const EPSILON: f64 = 0.0001;

/// A global accumulator (pub static).
pub static ACC: f64 = 0.0;

/// Double a value (private free function).
fn double(x: f64) -> f64 {
    x * 2.0
}

/// Circumference of a circle of radius `r`, using the crate const `PI` and the
/// private `double` (produces `uses_variable` + `calls` edges).
pub fn circumference(r: f64) -> f64 {
    double(PI) * r
}

/// Build a unit named after this module (uses the parent module's `Unit`).
pub fn unit_of_measure() -> Unit {
    Unit::new("meter")
}
