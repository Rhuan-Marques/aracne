package com.aracne.enums;

// Interface implemented by the Op enum, so the enum gets an `implements` edge.
public interface Operation {
    int apply(int a, int b);
}
