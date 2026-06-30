package com.aracne.consumer;

import com.aracne.factory.Factory;
import com.aracne.shapes.Shape;

// FLAGSHIP cross-file resolution. Imports Factory + the Shape interface,
// receives a Factory static-method return into a LOCAL VAR typed Shape, then
// calls .area() on it. This forces, in one method:
//   - `imports_module` to com.aracne.factory and com.aracne.shapes
//   - `uses_struct`  Factory (static receiver is the Factory type)
//   - `uses_interface` Shape (local var type)
//   - `calls` Factory.makeCircle(double) / Factory.makeRect(double,double)
//   - `calls` Shape.area() resolved through the local var's declared type.
public class Consumer {
    public double total() {
        Shape s = Factory.makeCircle(2.0);
        double a = s.area();
        Shape r = Factory.makeRect(2.0, 3.0);
        return a + r.area();
    }
}
