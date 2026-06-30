package com.aracne.nested;

// Every nested-type flavour in one top-level class:
//   - STATIC NESTED class   -> com.aracne.nested.Outer.Nested
//   - INNER (non-static)     -> com.aracne.nested.Outer.Inner
//   - LOCAL class in a method-> com.aracne.nested.Outer$Helper
//   - ANONYMOUS class        -> com.aracne.nested.Outer$anon1 (implements Runnable)
// NAMESPACE DISJOINTNESS: the int field `Inner` shares its simple name with the
// inner class `Inner`; in expression context the name binds to the FIELD, in
// type context to the CLASS. They must remain two distinct nodes.
public class Outer {
    private int Inner;
    private final String tag = "outer";

    public static class Nested {
        public int twice(int x) {
            return x * 2;
        }
    }

    public class Inner {
        public String describe() {
            return tag + ":" + Inner;
        }
    }

    public int compute(int seed) {
        class Helper {
            int boost() {
                return seed + 1;
            }
        }
        Helper helper = new Helper();
        return helper.boost();
    }

    public Runnable task() {
        return new Runnable() {
            @Override
            public void run() {
                System.out.println(tag);
            }
        };
    }
}
