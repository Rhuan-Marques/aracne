//! Trait-level edge cases: a supertrait (`inherits`), a generic trait with a
//! `where` bound, trait-object and `impl Trait` parameters, and a locally
//! derivable marker trait used via `#[derive(...)]` (an `implements` edge to an
//! internal trait).

use crate::shapes::Shape;

/// A drawable is a `Shape` that can also render itself. The supertrait bound
/// produces an `inherits` edge `Drawable -> Shape` (cross-module).
pub trait Drawable: Shape {
    /// Render the shape to a string.
    fn render(&self) -> String;
}

/// A generic container with a type parameter and a `where` bound.
pub trait Container<T>
where
    T: Clone,
{
    /// Insert an item.
    fn put(&mut self, item: T);

    /// Number of items currently held.
    fn len(&self) -> usize;
}

/// A locally-derivable marker trait (named by `#[derive(Tagged)]` below, which
/// the scanner records as an `implements` edge to this internal trait).
pub trait Tagged {
    /// A short tag for the implementing type.
    fn tag(&self) -> &str;
}

/// Measure any shape behind a trait-object reference (`&dyn Shape`).
pub fn describe(shape: &dyn Shape) -> f64 {
    shape.area()
}

/// Measure any shape by static dispatch (`impl Shape`).
pub fn measure(shape: impl Shape) -> f64 {
    shape.area()
}

/// A label that derives the internal `Tagged` trait. Gated behind the
/// (off-by-default) `derive_probe` feature: a plain trait is not a real derive
/// macro, so gating keeps a default `cargo check` clean while tree-sitter still
/// sees the `#[derive(Tagged)]` line and records the internal `implements` edge.
#[cfg(feature = "derive_probe")]
#[derive(Tagged)]
pub struct Label {
    pub text: String,
}
