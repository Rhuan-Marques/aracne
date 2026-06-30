package com.aracne.annotations;

// A class that USES the internal @Marker annotation at the type, field, and
// method level -> `uses_interface` edge(s) to com.aracne.annotations.Marker.
@Marker(value = "demo", priority = 5)
public class Marked {

    @Marker(value = "field")
    private int tagged;

    @Marker(value = "method")
    public int compute() {
        return tagged;
    }
}
