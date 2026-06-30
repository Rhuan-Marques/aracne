package com.aracne.enums;

// Enum implementing an interface, with BOTH an enum-level method (apply, used by
// MINUS) AND a CONSTANT BODY (PLUS overrides apply). The constant body PLUS is
// its own struct node com.aracne.enums.Op$PLUS that `inherits` Op and owns the
// overriding method. Variants=[PLUS,MINUS]; `implements` Operation.
public enum Op implements Operation {
    PLUS {
        @Override
        public int apply(int a, int b) {
            return a + b;
        }
    },
    MINUS;

    @Override
    public int apply(int a, int b) {
        return a - b;
    }

    public String symbol() {
        return name();
    }
}
