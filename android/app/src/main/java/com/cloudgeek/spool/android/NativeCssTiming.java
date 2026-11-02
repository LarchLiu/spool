package com.cloudgeek.spool.android;

import android.animation.TimeInterpolator;

/** Evaluate CSS cubic-bezier timing without platform Path approximation. */
final class NativeCssTiming {
    static final TimeInterpolator EASE_IN_OUT = cubicBezier(.42, 0, .58, 1);

    private static double point(double t, double first, double second) {
        double inverse = 1 - t;
        return 3 * inverse * inverse * t * first + 3 * inverse * t * t * second + t * t * t;
    }

    static TimeInterpolator cubicBezier(double x1, double y1, double x2, double y2) {
        if (!Double.isFinite(x1) || !Double.isFinite(y1) || !Double.isFinite(x2) || !Double.isFinite(y2)
                || x1 < 0 || x1 > 1 || x2 < 0 || x2 > 1) throw new IllegalArgumentException("Invalid CSS timing control points");
        return input -> {
            if (input <= 0) return 0;
            if (input >= 1) return 1;
            double low = 0, high = 1;
            // Time is the curve's x coordinate, not its parameter t.
            for (int i = 0; i < 24; i++) {
                double middle = (low + high) / 2;
                double x = point(middle, x1, x2);
                if (Math.abs(x - input) < 1e-12) return (float) point(middle, y1, y2);
                if (x < input) low = middle; else high = middle;
            }
            return (float) point((low + high) / 2, y1, y2);
        };
    }

    private NativeCssTiming() {}
}
