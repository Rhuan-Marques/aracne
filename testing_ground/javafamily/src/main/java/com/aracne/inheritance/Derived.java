package com.aracne.inheritance;

// Concrete subclass: `extends Base` (class<->class `inherits`). Exercises:
//   - this()  constructor chaining (no-arg ctor -> Derived.<init>(int))
//   - super() constructor chaining (-> Base.<init>(String))
//   - @Override of the abstract rank()
//   - COVARIANT return: copy() returns Derived (narrower than Base.copy()),
//     and constructs a new Derived (constructor edge).
public class Derived extends Base {
    private final int level;

    public Derived() {
        this(0);
    }

    public Derived(int level) {
        super("derived");
        this.level = level;
    }

    @Override
    public int rank() {
        return level;
    }

    @Override
    public Derived copy() {
        return new Derived(level);
    }
}
