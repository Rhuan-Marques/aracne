package com.aracne.factory;

import com.aracne.shapes.Circle;
import com.aracne.shapes.Rectangle;
import com.aracne.shapes.Shape;

// Final class with two static factory methods. Each returns the Shape interface
// but constructs a concrete type: `new Circle(r)` / `new Rectangle(w,h)` ->
// `constructor` edges to the ctors + `uses_struct` to Circle/Rectangle, and the
// imports record `imports_module` to com.aracne.shapes.
public final class Factory {
    private Factory() {
        // utility class: no instances
    }

    public static Shape makeCircle(double r) {
        return new Circle(r);
    }

    public static Shape makeRect(double w, double h) {
        return new Rectangle(w, h);
    }
}
