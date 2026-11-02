// Adapted from jsdiff, Copyright (c) 2009-2015 Kevin Decker, BSD-3-Clause.
// License and conditions are retained in android/THIRD_PARTY_NOTICES.
package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/** Native adaptation of jsdiff's diffWordsWithSpace and Pierre's word-alt decorations. */
final class NativeWordDiff {
    static final class Range {
        final int start, end;
        Range(int start, int end) { this.start = start; this.end = end; }
    }
    static final class Pair {
        final List<Range> old = new ArrayList<>(), next = new ArrayList<>();
    }
    private static final class Component {
        final int kind, count;
        final Component previous;
        Component(int kind, int count, Component previous) { this.kind = kind; this.count = count; this.previous = previous; }
    }
    private static final class Path {
        int oldPosition;
        Component tail;
        Path(int oldPosition, Component tail) { this.oldPosition = oldPosition; this.tail = tail; }
        Path extend(int kind) {
            Component component = tail != null && tail.kind == kind
                    ? new Component(kind, tail.count + 1, tail.previous) : new Component(kind, 1, tail);
            return new Path(oldPosition + (kind == -1 ? 1 : 0), component);
        }
    }

    static Pair ranges(String old, String next) {
        Pair result = new Pair();
        old = cleanLastNewline(old); next = cleanLastNewline(next);
        // FileDiff deliberately skips intra-line diffing beyond its default maxLineDiffLength.
        if (old.length() > 1000 || next.length() > 1000 || old.equals(next)) return result;
        List<String> a = tokenize(old), b = tokenize(next);
        List<Component> components = components(a, b);
        int oldIndex = 0, newIndex = 0;
        List<Span> oldSpans = new ArrayList<>(), newSpans = new ArrayList<>();
        for (int i = 0; i < components.size(); i++) {
            Component component = components.get(i);
            int size = 0;
            for (int token = 0; token < component.count; token++) size += (component.kind == -1 ? a.get(oldIndex + token) : b.get(newIndex + token)).length();
            boolean neutral = component.kind == 0, last = i == components.size() - 1;
            if (component.kind != 1) { join(oldSpans, size, neutral, last); oldIndex += component.count; }
            if (component.kind != -1) { join(newSpans, size, neutral, last); newIndex += component.count; }
        }
        collect(oldSpans, result.old); collect(newSpans, result.next);
        return result;
    }

    private static final class Span {
        final boolean neutral;
        int length;
        Span(int length, boolean neutral) { this.length = length; this.neutral = neutral; }
    }
    private static void join(List<Span> spans, int size, boolean neutral, boolean last) {
        Span previous = spans.isEmpty() ? null : spans.get(spans.size() - 1);
        if (previous != null && !last && (previous.neutral == neutral || neutral && size == 1 && !previous.neutral)) previous.length += size;
        else spans.add(new Span(size, neutral));
    }
    private static void collect(List<Span> spans, List<Range> target) {
        int offset = 0;
        for (Span span : spans) {
            if (!span.neutral && span.length > 0) target.add(new Range(offset, offset + span.length));
            offset += span.length;
        }
    }

    private static List<Component> components(List<String> old, List<String> next) {
        int limit = old.size() + next.size(), center = limit + 1;
        Path[] paths = new Path[limit * 2 + 3];
        Path seed = new Path(-1, null);
        int newPosition = common(seed, next, old, 0);
        if (seed.oldPosition + 1 >= old.size() && newPosition + 1 >= next.size()) return forward(seed.tail);
        paths[center] = seed;
        int minDiagonal = -limit, maxDiagonal = limit;
        for (int edits = 1; edits <= limit; edits++) {
            if (Thread.currentThread().isInterrupted()) throw new java.util.concurrent.CancellationException();
            for (int diagonal = Math.max(minDiagonal, -edits); diagonal <= Math.min(maxDiagonal, edits); diagonal += 2) {
                Path remove = paths[center + diagonal - 1], add = paths[center + diagonal + 1];
                if (remove != null) paths[center + diagonal - 1] = null;
                boolean canAdd = add != null && add.oldPosition - diagonal >= 0 && add.oldPosition - diagonal < next.size();
                boolean canRemove = remove != null && remove.oldPosition + 1 < old.size();
                if (!canAdd && !canRemove) { paths[center + diagonal] = null; continue; }
                // Match jsdiff's tie-breaking, including repeated punctuation/whitespace tokens.
                Path path = !canRemove || canAdd && remove.oldPosition < add.oldPosition ? add.extend(1) : remove.extend(-1);
                newPosition = common(path, next, old, diagonal);
                if (path.oldPosition + 1 >= old.size() && newPosition + 1 >= next.size()) return forward(path.tail);
                paths[center + diagonal] = path;
                if (path.oldPosition + 1 >= old.size()) maxDiagonal = Math.min(maxDiagonal, diagonal - 1);
                if (newPosition + 1 >= next.size()) minDiagonal = Math.max(minDiagonal, diagonal + 1);
            }
        }
        throw new IllegalStateException("Unreachable word diff path");
    }

    private static int common(Path path, List<String> next, List<String> old, int diagonal) {
        int newPosition = path.oldPosition - diagonal, count = 0;
        while (newPosition + 1 < next.size() && path.oldPosition + 1 < old.size()
                && old.get(path.oldPosition + 1).equals(next.get(newPosition + 1))) {
            newPosition++; path.oldPosition++; count++;
        }
        if (count > 0) path.tail = new Component(0, count, path.tail);
        return newPosition;
    }
    private static List<Component> forward(Component tail) {
        List<Component> result = new ArrayList<>();
        for (Component component = tail; component != null; component = component.previous) result.add(component);
        Collections.reverse(result);
        return result;
    }

    private static List<String> tokenize(String text) {
        List<String> result = new ArrayList<>();
        for (int start = 0; start < text.length();) {
            int point = text.codePointAt(start), end = start + Character.charCount(point);
            if (point == '\r' && end < text.length() && text.charAt(end) == '\n') end++;
            else if (word(point)) {
                while (end < text.length() && word(text.codePointAt(end))) end += Character.charCount(text.codePointAt(end));
            } else if (whitespace(point) && point != '\r' && point != '\n') {
                while (end < text.length() && whitespace(text.codePointAt(end)) && text.charAt(end) != '\r' && text.charAt(end) != '\n')
                    end += Character.charCount(text.codePointAt(end));
            }
            result.add(text.substring(start, end)); start = end;
        }
        return result;
    }
    private static boolean word(int point) {
        return point >= 'a' && point <= 'z' || point >= 'A' && point <= 'Z' || point >= '0' && point <= '9' || point == '_' || point == 0xad
                || point >= 0xc0 && point <= 0xd6 || point >= 0xd8 && point <= 0xf6 || point >= 0xf8 && point <= 0x2c6
                || point >= 0x2c8 && point <= 0x2d7 || point >= 0x2de && point <= 0x2ff || point >= 0x1e00 && point <= 0x1eff;
    }
    static boolean whitespace(int point) {
        return point >= 9 && point <= 13 || point == 0x20 || point == 0xa0 || point == 0x1680 || point >= 0x2000 && point <= 0x200a
                || point == 0x2028 || point == 0x2029 || point == 0x202f || point == 0x205f || point == 0x3000 || point == 0xfeff;
    }
    static String cleanLastNewline(String text) {
        if (!text.endsWith("\n")) return text;
        int end = text.length() - 1;
        return text.substring(0, end > 0 && text.charAt(end - 1) == '\r' ? end - 1 : end);
    }
}
