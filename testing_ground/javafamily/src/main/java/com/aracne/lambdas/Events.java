package com.aracne.lambdas;

import com.aracne.shapes.Circle;

import java.util.function.Function;
import java.util.function.Supplier;

// Lambdas + method references. Calls inside lambda bodies should attribute to
// the ENCLOSING method. Exercises:
//   - a lambda: Runnable r = () -> increment();  (calls Events.increment)
//   - an instance method reference Type::m  (Circle::area)
//   - a constructor method reference Type::new (Circle::new -> Circle.<init>(double))
//   - a lambda that constructs a Circle (constructor edge).
public class Events {
    private int counter;

    public void wire() {
        Runnable r = () -> increment();
        r.run();
    }

    public double areaOf(Circle c) {
        Function<Circle, Double> f = Circle::area;
        return f.apply(c);
    }

    public Supplier<Circle> maker() {
        return () -> new Circle(1.0);
    }

    public Function<Double, Circle> makerRef() {
        Function<Double, Circle> ctor = Circle::new;
        return ctor;
    }

    private void increment() {
        counter++;
    }
}
