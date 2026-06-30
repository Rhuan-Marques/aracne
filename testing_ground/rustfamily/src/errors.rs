//! Error handling: an error enum, `?`-propagation, a sentinel const, and an
//! optional serde-backed wrapper. The `use serde::Serialize;` + `#[derive(
//! Serialize)]` exercise the external-dependency (`uses_dependency`) edge; both
//! are gated behind the `serde` feature so a default build compiles offline.

use crate::shapes::Circle;

#[cfg(feature = "serde")]
use serde::Serialize;

/// A sentinel "not found" error code (const).
pub const NOT_FOUND: i32 = 404;

/// Errors that can occur while loading a shape (enum: `Missing` is a unit
/// variant, `BadRadius` a tuple variant).
pub enum MyError {
    /// The requested shape was missing.
    Missing,
    /// The radius was outside the valid range.
    BadRadius(f64),
}

/// Validate a radius and return a circle, or an error (constructs `MyError` and
/// `Circle`).
pub fn validate(r: f64) -> Result<Circle, MyError> {
    if r <= 0.0 {
        return Err(MyError::BadRadius(r));
    }
    Ok(Circle::new(r))
}

/// Try to load a circle, propagating failures with `?` (calls `validate`).
pub fn load() -> Result<Circle, MyError> {
    let c = validate(1.0)?;
    Ok(c)
}

/// A serde-serializable wrapper, built only with the `serde` feature enabled.
#[cfg(feature = "serde")]
#[derive(Serialize)]
pub struct Wrapper {
    pub radius: f64,
    pub label: String,
}
