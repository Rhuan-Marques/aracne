//! Constructors that hand back shapes — by value (the flagship constructor
//! pattern) and as a boxed trait object.

use crate::geometry::Geometry;
use crate::shapes::{Circle, Shape};

/// A function-pointer type alias (named type with a function underlying type).
pub type ShapeFactory = fn(f64) -> Circle;

/// Build a `Circle` of the given radius (value constructor; return type drives
/// cross-module method resolution in `consumer`).
pub fn make_circle(r: f64) -> Circle {
    Circle::new(r)
}

/// Build some shape as a boxed trait object (the `Box<dyn Shape>` return type is
/// intentionally unresolvable to a concrete type).
pub fn make_shape() -> Box<dyn Shape> {
    Box::new(make_circle(1.0))
}

/// Build a `Geometry` value (calls the same-file enum's associated fn).
pub fn make_geometry(r: f64) -> Geometry {
    Geometry::round(r)
}
