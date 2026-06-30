package com.aracne.redprobes;

import java.util.List;

// RED PROBE (bug _3): pathological same-simple-name overloads. pick(List<String>)
// and pick(List<Integer>) both ERASE to pick(List) (type arguments are stripped
// from the ID), colliding on a single method ID
// com.aracne.redprobes.Overloaded.pick(List). Ideally they remain TWO distinct
// resources; today they collapse into one.
public class Overloaded {
    public int pick(List<String> xs) {
        return xs.size();
    }

    public int pick(List<Integer> xs) {
        return xs.size();
    }
}
