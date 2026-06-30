package com.aracne.external;

import com.google.common.collect.ImmutableList;

import java.util.ArrayList;
import java.util.List;

// External-only edges. Imports the JDK (java.util.* via List/ArrayList) and the
// SOLE pom dependency (com.google.guava). Calls into both:
//   - new ArrayList<>() / List.add -> java.* external root, NO internal edge
//   - ImmutableList.of(...)        -> `imports_dependency` to com.google.guava:guava
// Nothing here should produce a false internal edge.
public class ExternalUser {

    public List<String> names() {
        List<String> out = new ArrayList<>();
        out.add("alpha");
        out.add("beta");
        return out;
    }

    public ImmutableList<String> frozen() {
        return ImmutableList.of("x", "y", "z");
    }
}
