//! A sum type over shapes, with a same-file `Shape` implementation and a method
//! that dispatches on the variant with a `match`.

use crate::shapes::{Circle, Shape};

/// Either a circular or rectangular geometry (enum: variants modeled as a
/// property list, not per-variant nodes).
pub enum Geometry {
    /// A circle wrapper variant (tuple variant holding an internal `Circle`).
    Round(Circle),
    /// An axis-aligned rectangle variant (struct variant).
    Rect { w: f64, h: f64 },
}

impl Geometry {
    /// Construct a round geometry from a radius (calls `Circle::new`).
    pub fn round(r: f64) -> Self {
        Geometry::Round(Circle::new(r))
    }

    /// Total area, dispatching on the variant with a `match`.
    pub fn measure(&self) -> f64 {
        match self {
            Geometry::Round(c) => c.area(),
            Geometry::Rect { w, h } => w * h,
        }
    }
}

impl Shape for Geometry {
    fn area(&self) -> f64 {
        self.measure()
    }

    fn name(&self) -> &str {
        "geometry"
    }
}
