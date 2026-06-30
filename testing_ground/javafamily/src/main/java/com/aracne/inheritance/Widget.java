package com.aracne.inheritance;

// Class that IMPLEMENTS MULTIPLE interfaces -> two `implements` edges
// (Widget -> Named, Widget -> Sized), so each interface gets a `implemented_by`.
public class Widget implements Named, Sized {
    private final String id;

    public Widget(String id) {
        this.id = id;
    }

    @Override
    public String name() {
        return id;
    }

    @Override
    public int size() {
        return id.length();
    }
}
