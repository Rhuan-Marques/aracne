package com.aracne.generics;

import com.aracne.shapes.Shape;

import java.util.List;

// Generic class Box<T extends Shape>: Generics=[T] with an upper bound on the
// internal Shape interface. Exercises:
//   - a bounded type parameter (uses_interface Shape via the bound)
//   - measure(): calls area() on a T-typed field (generic-bound method dispatch)
//   - sum(): a generic STATIC METHOD <U extends Shape>
//   - total(): a WILDCARD parameter List<? extends Shape>, iterated and called.
public class Box<T extends Shape> {
    private final T value;

    public Box(T value) {
        this.value = value;
    }

    public T get() {
        return value;
    }

    public double measure() {
        return value.area();
    }

    public static <U extends Shape> double sum(U a, U b) {
        return a.area() + b.area();
    }

    public static double total(List<? extends Shape> shapes) {
        double acc = 0;
        for (Shape s : shapes) {
            acc += s.area();
        }
        return acc;
    }
}
