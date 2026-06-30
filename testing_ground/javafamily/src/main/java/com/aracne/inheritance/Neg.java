package com.aracne.inheritance;

// Permitted subtype of sealed Expr that wraps another Expr. eval() calls
// inner.eval() where `inner` is typed Expr -> `uses_interface` Expr + `calls`
// Expr.eval() through the field's declared interface type.
public final class Neg implements Expr {
    private final Expr inner;

    public Neg(Expr inner) {
        this.inner = inner;
    }

    @Override
    public int eval() {
        return -inner.eval();
    }
}
