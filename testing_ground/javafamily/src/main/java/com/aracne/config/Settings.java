package com.aracne.config;

// Initializer blocks:
//   - a STATIC initializer block -> com.aracne.config.Settings.<clinit>() that
//     calls the static helper defaultGlobal()
//   - an INSTANCE initializer block -> Settings.<instance-init>() that calls the
//     instance helper defaultLocal()
// Each block's body produces a `calls` edge to its helper.
public class Settings {
    private static final String GLOBAL;
    private final String local;

    static {
        GLOBAL = defaultGlobal();
    }

    {
        local = defaultLocal();
    }

    public Settings() {
        // instance initializer above runs before this body
    }

    private static String defaultGlobal() {
        return "global";
    }

    private String defaultLocal() {
        return "local-" + GLOBAL;
    }

    public String global() {
        return GLOBAL;
    }

    public String local() {
        return local;
    }
}
