package com.aracne.inheritance;

// Abstract base class. Has an abstract method (rank), a concrete method (label)
// that calls the abstract one, and a copy() returning Base (covariantly
// overridden in Derived). Derived `extends Base` -> struct<->struct `inherits`.
public abstract class Base {
    protected final String name;

    protected Base(String name) {
        this.name = name;
    }

    public abstract int rank();

    public Base copy() {
        return this;
    }

    public String label() {
        return name + ":" + rank();
    }
}
