package com.aracne.inheritance;

// SEALED interface with an explicit `permits` list. Only Lit and Neg may
// implement it (both in this package, both `final`). Drives the `permits`
// property + `inherited_by` to exactly the permitted types.
public sealed interface Expr permits Lit, Neg {
    int eval();
}
