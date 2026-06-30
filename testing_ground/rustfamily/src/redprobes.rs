//! RED-PROBE constructs. Each is preceded by a `// GAP:` note stating the edge
//! the scanner is expected to *want* but currently leave unresolved. The
//! free-function and constructor `calls` that *do* resolve are kept (so the file
//! still produces real edges); only the chained method call is the gap.

use crate::errors::{load, MyError};
use crate::factory::make_shape;
use crate::shapes::{Circle, Shape};

// GAP: trait-object dispatch. `make_shape()` returns `Box<dyn Shape>`, so the
// boxed value's concrete type is unknown and `s.area()` does NOT resolve to
// `rustfamily::shapes::Circle::area` (only the `make_shape` call resolves).
pub fn via_trait_object() -> f64 {
    let s: Box<dyn Shape> = make_shape();
    s.area()
}

// GAP: generic dispatch. `T` is only known to be `Shape`, so `t.area()` cannot
// be resolved to a concrete implementor's method.
pub fn render<T: Shape>(t: T) -> f64 {
    t.area()
}

// GAP: `?`/Result unwrap. `load()?` yields a `Circle`, but the `?` operator's
// result type is not tracked, so `c.area()` does NOT resolve (the `load` call
// itself does resolve).
pub fn via_question() -> Result<f64, MyError> {
    let c = load()?;
    Ok(c.area())
}

// GAP: closure / iterator chain. The closure parameter `x` carries no tracked
// type, so `x.area()` inside `.map(...)` does NOT resolve. The inline
// `Circle::new` is ALSO unresolved here because it sits inside a `vec!` macro
// token-tree, whose contents tree-sitter leaves unparsed (no call edges).
pub fn via_iterator() -> f64 {
    vec![Circle::new(1.0)].iter().map(|x| x.area()).sum::<f64>()
}
