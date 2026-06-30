package com.aracne.exceptions;

// Custom checked exception: `extends Exception` (an EXTERNAL supertype -> an
// `inherits` edge whose target is outside the corpus, no internal node). The
// constructor chains to super(message).
public class CustomException extends Exception {
    private final int code;

    public CustomException(String message, int code) {
        super(message);
        this.code = code;
    }

    public int code() {
        return code;
    }
}
