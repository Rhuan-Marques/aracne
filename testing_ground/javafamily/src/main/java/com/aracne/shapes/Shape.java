package com.aracne.shapes;

// Anchor interface: the single-method contract every shape implements. Multiple
// implementers (Circle, Rectangle) -> `implemented_by` fans out from here.
public interface Shape {
    double area();
}
