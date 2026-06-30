package com.aracne.shapes;

// Anchor class implementing Shape. Exercises: a field, a constructor
// (Circle.<init>(double)), an @Override of area(), and an OVERLOAD area(int)
// whose ID (area(int)) is distinct from area() and which calls the no-arg
// sibling (intra-class `calls` edge).
public class Circle implements Shape {
    private final double radius;

    public Circle(double radius) {
        this.radius = radius;
    }

    @Override
    public double area() {
        return Math.PI * radius * radius;
    }

    // OVERLOAD: distinct ID com.aracne.shapes.Circle.area(int); calls area().
    public double area(int scale) {
        return area() * scale;
    }
}
