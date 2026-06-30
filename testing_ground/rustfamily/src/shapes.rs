//! Geometric shapes: the `Shape` trait (a default method + an associated const),
//! a named struct, a tuple struct, a unit struct, and a newtype wrapping an
//! internal type. The inherent and trait `area` methods deliberately coexist
//! (Rust resolves bare `c.area()` to the inherent one).

/// A geometric shape that can report its area and a name.
pub trait Shape {
    /// Number of sides; an associated const with a default value.
    const SIDES: u32 = 0;

    /// Compute the area of the shape (required method).
    fn area(&self) -> f64;

    /// A human-readable name (default method).
    fn name(&self) -> &str {
        "shape"
    }
}

/// A circle defined by its radius (named struct).
pub struct Circle {
    pub radius: f64,
}

/// A length in meters (tuple struct / newtype over `f64`).
pub struct Meters(pub f64);

/// The coordinate-system origin (unit struct).
pub struct Origin;

/// A radius value (type alias / named type over a primitive).
pub type Radius = f64;

/// A newtype wrapping an internal `Circle`.
pub struct Disk(pub Circle);

impl Circle {
    /// Construct a new circle with the given radius (associated constructor).
    pub fn new(r: f64) -> Self {
        Circle { radius: r }
    }

    /// The area of the circle: pi * r^2 (inherent method).
    pub fn area(&self) -> f64 {
        3.14159 * self.radius * self.radius
    }

    /// The diameter: twice the radius.
    pub fn diameter(&self) -> f64 {
        self.radius * 2.0
    }
}

impl Shape for Circle {
    const SIDES: u32 = 0;

    fn area(&self) -> f64 {
        3.14159 * self.radius * self.radius
    }

    fn name(&self) -> &str {
        "circle"
    }
}

impl Disk {
    /// Wrap an existing circle in a `Disk` (associated constructor over an
    /// internal type).
    pub fn wrap(c: Circle) -> Self {
        Disk(c)
    }
}
