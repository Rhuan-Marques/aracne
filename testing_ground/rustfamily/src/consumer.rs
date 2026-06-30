//! FLAGSHIP: cross-module constructor + method resolution. Mirrors the jsfamily
//! `consumer` pattern — import a sibling module, receive a constructor's return
//! into a local, then call a method on it.

use crate::factory::make_circle;
use crate::shapes::{Circle, Shape};

/// Build shapes two ways and sum their areas. Resolves:
///   `Circle::new` (assoc fn), `Circle::area`/`Circle::diameter` (methods via
///   the inferred type of `c`/`s`), and `make_circle` (free fn).
pub fn report() -> f64 {
    let c = Circle::new(2.0);
    let a = c.area();
    let s = make_circle(1.0);
    let _ = s.diameter();
    a + s.area()
}
