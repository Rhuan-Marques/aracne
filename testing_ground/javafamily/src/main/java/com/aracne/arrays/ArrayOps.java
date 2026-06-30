package com.aracne.arrays;

// Varargs + array parameters: a varargs `int...` normalizes to `int[]` in the
// method-ID signature, and an explicit `String[]` keeps its array dims. This
// locks ID normalization for arrays/varargs (OK green lock-in).
public class ArrayOps {
    public int total(int... nums) {
        int acc = 0;
        for (int n : nums) {
            acc += n;
        }
        return acc;
    }

    public String join(String[] parts) {
        return String.join(",", parts);
    }
}
