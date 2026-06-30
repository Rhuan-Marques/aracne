package com.aracne.inheritance;

// Permitted subtype of the sealed Expr (a leaf literal). `final` satisfies the
// sealed-hierarchy closure requirement. implements Expr -> `implements` edge.
public final class Lit implements Expr {
    private final int value;

    public Lit(int value) {
        this.value = value;
    }

    @Override
    public int eval() {
        return value;
    }
}
