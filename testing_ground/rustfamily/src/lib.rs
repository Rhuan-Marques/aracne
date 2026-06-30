//! rustfamily — a hand-built, single-crate Rust corpus for the aracne topology
//! engine. Every module is interconnected so the scanner has cross-module
//! `imports_module`, `uses_dependency`, `implements`, `inherits`, and
//! return-type-inferred `calls` edges to resolve.
//!
//! Resource-ID scheme (rooted at the `[package].name`, `::`-separated):
//!   struct   `rustfamily::shapes::Circle`
//!   method   `rustfamily::shapes::Circle::area`
//!   assoc fn `rustfamily::shapes::Circle::new`
//!   trait    `rustfamily::shapes::Shape`
//!   free fn  `rustfamily::factory::make_circle`
//!   const    `rustfamily::util::math::PI`

#![allow(dead_code, unused)]

pub mod shapes;
pub mod geometry;
pub mod factory;
pub mod consumer;
pub mod util;
pub mod traits;
pub mod errors;
pub mod redprobes;

// Crate-root re-export (exercises a `use` that resolves to a sibling module's
// struct, producing an `imports_module` edge from the crate root to `shapes`).
pub use shapes::Circle;
