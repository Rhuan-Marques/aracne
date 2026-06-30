package com.aracne.records;

// RECORD implementing an interface. Components=[x,y] synthesize accessor methods
// x() and y() (Loc = record header line). Exercises:
//   - a COMPACT CONSTRUCTOR (validation, Point.<init>(int,int))
//   - `implements` Located + an @Override of manhattan()
//   - an extra method translate() that builds a new Point (constructor edge).
public record Point(int x, int y) implements Located {

    public Point {
        if (x < 0 || y < 0) {
            throw new IllegalArgumentException("negative coordinate");
        }
    }

    @Override
    public int manhattan() {
        return Math.abs(x) + Math.abs(y);
    }

    public Point translate(int dx, int dy) {
        return new Point(x + dx, y + dy);
    }
}
