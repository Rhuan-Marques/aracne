package com.aracne.annotations;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

// @interface (annotation type) -> ResourceInterface with IsAnnotation. Declares
// two elements (value, priority-with-default). Classes that USE @Marker record a
// `uses_interface` edge to this type. Retention RUNTIME so a scanner that reads
// annotations at runtime could see it (not required, but realistic).
@Retention(RetentionPolicy.RUNTIME)
@Target({ElementType.TYPE, ElementType.FIELD, ElementType.METHOD})
public @interface Marker {
    String value();

    int priority() default 0;
}
