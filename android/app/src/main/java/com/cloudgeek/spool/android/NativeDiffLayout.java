package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/** Shared split-diff rows, using Pierre's bounded similarity realignment and word-alt spans. */
final class NativeDiffLayout {
    static final class Cell {
        final String text;
        final int number, hunk;
        final char kind;
        List<NativeWordDiff.Range> emphasis = Collections.emptyList();
        List<NativeSyntaxEngine.Run> syntax = Collections.emptyList();
        Cell(String text, int number, char kind) { this(text, number, kind, 0); }
        Cell(String text, int number, char kind, int hunk) { this.text = text; this.number = number; this.kind = kind; this.hunk = hunk; }
    }
    static final class Row {
        final Cell old, next;
        final String separator;
        final boolean simple;
        Row(Cell old, Cell next, boolean precise) {
            this.old = old; this.next = next; separator = null; simple = false;
            if (precise && old != null && next != null && old.kind == '-' && next.kind == '+') {
                NativeWordDiff.Pair pair = NativeWordDiff.ranges(old.text, next.text);
                old.emphasis = pair.old; next.emphasis = pair.next;
            }
        }
        Row(Cell old, Cell next) { this(old, next, true); }
        Row(String separator, boolean simple) { old = next = null; this.separator = separator; this.simple = simple; }
    }

    static List<Row> rows(NativePatch.File file) {
        return rows(file, true);
    }

    static List<Row> rows(NativePatch.File file, boolean precise) {
        List<Row> rows = new ArrayList<>();
        for (int h = 0; h < file.hunks.size(); h++) {
            NativePatch.Hunk hunk = file.hunks.get(h);
            if (h > 0 || !file.lineNumbersUnavailable && Math.max(hunk.deletionStart, hunk.additionStart) > 1)
                rows.add(new Row(hunk.specs.trim(), file.lineNumbersUnavailable));
            int oldNumber = hunk.deletionStart, newNumber = hunk.additionStart;
            for (int i = 0; i < hunk.lines.size();) {
                if (Thread.currentThread().isInterrupted()) throw new java.util.concurrent.CancellationException();
                NativePatch.Line line = hunk.lines.get(i);
                if (line.kind == ' ') {
                    rows.add(new Row(new Cell(line.text, oldNumber++, ' ', h), new Cell(line.text, newNumber++, ' ', h)));
                    i++;
                } else {
                    List<Cell> old = new ArrayList<>(), next = new ArrayList<>();
                    while (i < hunk.lines.size() && hunk.lines.get(i).kind != ' ') {
                        line = hunk.lines.get(i++);
                        if (line.kind == '-') old.add(new Cell(line.text, oldNumber++, '-', h));
                        else next.add(new Cell(line.text, newNumber++, '+', h));
                    }
                    append(rows, old, next, precise);
                }
            }
            if (hunk.noEOFCRAdditions || hunk.noEOFCRDeletions) rows.add(new Row(
                    hunk.noEOFCRDeletions ? new Cell("No newline at end of file", 0, 'n') : null,
                    hunk.noEOFCRAdditions ? new Cell("No newline at end of file", 0, 'n') : null));
        }
        return rows;
    }

    private static void append(List<Row> rows, List<Cell> old, List<Cell> next, boolean precise) {
        int pairs = Math.min(old.size(), next.size()), surplus = Math.abs(old.size() - next.size());
        boolean additionsLonger = next.size() > old.size();
        int offset = precise ? alignment(old, next) : 0;
        for (int i = 0; i < offset; i++) rows.add(new Row(additionsLonger ? null : old.get(i), additionsLonger ? next.get(i) : null, precise));
        for (int i = 0; i < pairs; i++) {
            if (Thread.currentThread().isInterrupted()) throw new java.util.concurrent.CancellationException();
            rows.add(new Row(old.get(i + (additionsLonger ? 0 : offset)), next.get(i + (additionsLonger ? offset : 0)), precise));
        }
        for (int i = offset; i < surplus; i++) rows.add(new Row(additionsLonger ? null : old.get(pairs + i), additionsLonger ? next.get(pairs + i) : null, precise));
    }

    private static int alignment(List<Cell> old, List<Cell> next) {
        int pairs = Math.min(old.size(), next.size()), surplus = Math.abs(old.size() - next.size());
        if (pairs == 0 || surplus == 0 || (long) pairs * (surplus + 1) > 4096) return 0;
        List<String> a = stripped(old), b = stripped(next);
        double bestScore = -1;
        int bestOffset = 0;
        for (int offset = 0; offset <= surplus; offset++) {
            double score = 0;
            for (int pair = 0; pair < pairs; pair++) score += similarity(a.get(pair + (old.size() > next.size() ? offset : 0)), b.get(pair + (next.size() > old.size() ? offset : 0)));
            if (offset == 0) bestScore = score + pairs * .5;
            else if (score > bestScore) { bestScore = score; bestOffset = offset; }
        }
        return bestOffset;
    }
    private static List<String> stripped(List<Cell> cells) {
        List<String> result = new ArrayList<>();
        for (Cell cell : cells) {
            StringBuilder value = new StringBuilder();
            for (int i = 0; i < cell.text.length(); i++) if (!NativeWordDiff.whitespace(cell.text.charAt(i))) value.append(cell.text.charAt(i));
            result.add(value.toString());
        }
        return result;
    }
    private static double similarity(String a, String b) {
        if (a.equals(b)) return 1;
        int min = Math.min(a.length(), b.length()), max = Math.max(a.length(), b.length());
        if (min == 0) return 0;
        int prefix = 0, suffix = 0;
        while (prefix < min && a.charAt(prefix) == b.charAt(prefix)) prefix++;
        while (suffix < min - prefix && a.charAt(a.length() - 1 - suffix) == b.charAt(b.length() - 1 - suffix)) suffix++;
        return (double) (prefix + suffix) / max;
    }
}
