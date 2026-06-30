package com.aracne.redprobes;

import com.aracne.generics.Box;
import com.aracne.shapes.Rectangle;

// RED PROBE (bug _1): generic return-type propagation. chained() does
// box.get().area() where Box<T extends Shape>.get() returns T (= Rectangle).
// The IDEAL topology has chained() -> calls Rectangle.area() (the instantiated
// element type), but the scanner does not propagate the generic type argument
// through the return type, so the chained call is dropped. Uses Rectangle (NOT
// Circle) so the J2 Circle->Disk rename scenario's assertNoReferences stays valid.
public class GenericReturn {
    public double chained(Box<Rectangle> box) {
        return box.get().area();
    }
}
