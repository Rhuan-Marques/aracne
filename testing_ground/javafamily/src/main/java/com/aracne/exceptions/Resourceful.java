package com.aracne.exceptions;

import java.io.Closeable;

// Exercises exception-handling shapes:
//   - a method declaring `throws CustomException` (INTERNAL type -> Throws record)
//   - a TRY-WITH-RESOURCES block over a static-nested Closeable (Handle)
//   - a CATCH clause that wraps and rethrows the custom exception
//     (constructor edge to CustomException.<init>(String,int)).
public class Resourceful {

    // Static-nested AutoCloseable used by the try-with-resources.
    static final class Handle implements Closeable {
        private final String label;

        Handle(String label) {
            this.label = label;
        }

        String read() {
            return label;
        }

        @Override
        public void close() {
            // no-op (narrows Closeable.close: declares no checked exception)
        }
    }

    public String load(String label) throws CustomException {
        try (Handle h = new Handle(label)) {
            return h.read();
        } catch (RuntimeException e) {
            throw new CustomException(e.getMessage(), 1);
        }
    }
}
