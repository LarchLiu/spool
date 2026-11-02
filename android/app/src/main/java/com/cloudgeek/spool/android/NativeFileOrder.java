package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/** Native adaptation of Pierre's ASCII-digit natural order and UTF-16 tie breaks. */
final class NativeFileOrder {
    static final class Key {
        final String original, lower;
        final List<Object> tokens = new ArrayList<>();

        Key(String value) {
            original = value; lower = value.toLowerCase(Locale.ROOT);
            int start = 0, index = 0;
            while (index < lower.length()) {
                while (index < lower.length() && !digit(lower.charAt(index))) index++;
                if (index == lower.length()) break;
                if (index > start) tokens.add(lower.substring(start, index));
                double number = 0;
                while (index < lower.length() && digit(lower.charAt(index))) number = number * 10 + (lower.charAt(index++) - '0');
                tokens.add(number); start = index;
            }
            if (start < lower.length() || tokens.isEmpty()) tokens.add(lower.substring(start));
        }
    }

    private static boolean digit(char value) { return value >= '0' && value <= '9'; }

    static int compare(Key left, Key right) {
        for (int i = 0; i < Math.min(left.tokens.size(), right.tokens.size()); i++) {
            Object a = left.tokens.get(i), b = right.tokens.get(i);
            if (a.equals(b)) continue;
            int comparison;
            if (a instanceof Double && b instanceof Double) comparison = Double.compare((double) a, (double) b);
            else {
                // A string token starts with a non-digit, so finite number formatting cannot affect this comparison.
                comparison = a.toString().compareTo(b.toString());
            }
            if (comparison != 0) return comparison;
        }
        int comparison = Integer.compare(left.tokens.size(), right.tokens.size());
        if (comparison == 0) comparison = left.lower.compareTo(right.lower);
        return comparison == 0 ? left.original.compareTo(right.original) : comparison;
    }

    private NativeFileOrder() {}
}
