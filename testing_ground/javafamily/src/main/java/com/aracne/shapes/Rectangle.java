package com.aracne.shapes;

// Second implementer of Shape (so Shape has >1 `implemented_by`). Two-arg
// constructor Rectangle.<init>(double,double).
public class Rectangle implements Shape {
    private final double width;
    private final double height;

    public Rectangle(double width, double height) {
        this.width = width;
        this.height = height;
    }

    @Override
    public double area() {
        return width * height;
    }
}
