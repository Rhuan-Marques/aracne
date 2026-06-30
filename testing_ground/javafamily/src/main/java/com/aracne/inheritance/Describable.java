package com.aracne.inheritance;

// Interface that EXTENDS MULTIPLE interfaces -> two iface<->iface `inherits`
// edges (Describable inherits Named AND Sized), each gets `inherited_by`.
public interface Describable extends Named, Sized {
    String describe();
}
