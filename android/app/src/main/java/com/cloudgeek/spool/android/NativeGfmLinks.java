package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.commonmark.internal.util.Escaping;

/** GFM literal links, including balanced URL punctuation and no links inside explicit labels. */
final class NativeGfmLinks {
    static final class Link {
        final int start, end;
        final String target;
        Link(int start, int end, String target) { this.start = start; this.end = end; this.target = target; }
    }
    private static final Pattern URL = Pattern.compile("https?://|www\\.", Pattern.CASE_INSENSITIVE);
    private static final Pattern EMAIL = Pattern.compile("(?<![-A-Za-z0-9_.+/])[-A-Za-z0-9_.+]++@[-A-Za-z0-9_]*+(?:\\.[-A-Za-z0-9_]++)+");

    static String normalize(String target) {
        // micromark's URI normalization also escapes the RFC-3986 bracket characters.
        return Escaping.percentEncodeUrl(target).replace("[", "%5B").replace("]", "%5D");
    }

    static List<Link> find(String source) {
        List<Link> candidates = new ArrayList<>(), links = new ArrayList<>();
        Matcher urls = URL.matcher(source);
        while (urls.find()) {
            int start = urls.start(); boolean www = source.regionMatches(true, start, "www.", 0, 4);
            int previous = start == 0 ? -1 : source.codePointBefore(start);
            if (www ? previous != -1 && !space(previous) && !punctuation(previous) : asciiLetter(previous)) continue;
            int end = urls.end();
            while (end < source.length() && source.charAt(end) != '<' && !space(source.codePointAt(end))) end += Character.charCount(source.codePointAt(end));
            end = trim(source, start, end);
            int domainStart = www ? start : urls.end(), domainEnd = domainStart;
            while (domainEnd < end) {
                int c = source.codePointAt(domainEnd);
                if (c != '-' && c != '_' && c != '.' && punctuation(c)) break;
                domainEnd += Character.charCount(c);
            }
            if (domainEnd <= domainStart) continue;
            String[] labels = source.substring(domainStart, domainEnd).split("\\.", -1);
            boolean valid = true;
            for (int i = Math.max(0, labels.length - 2); i < labels.length; i++) if (labels[i].contains("_")) valid = false;
            if (valid) {
                candidates.add(new Link(start, end, normalize((www ? "http://" : "") + source.substring(start, end))));
                urls.region(Math.max(end, urls.end()), source.length());
            }
        }
        Matcher email = EMAIL.matcher(source);
        while (email.find()) {
            int previous = email.start() == 0 ? -1 : source.codePointBefore(email.start());
            if (previous == '/' || previous != -1 && (asciiLetter(previous) || previous >= '0' && previous <= '9' || "+-._".indexOf(previous) >= 0)
                    || !asciiLetter(source.charAt(email.end() - 1))) continue;
            candidates.add(new Link(email.start(), email.end(), "mailto:" + email.group()));
        }
        candidates.sort(Comparator.comparingInt((Link link) -> link.start).thenComparingInt(link -> -link.end));
        int end = 0;
        for (Link link : candidates) if (link.start >= end) { links.add(link); end = link.end; }
        return links;
    }

    private static int trim(String source, int start, int end) {
        int open = 0, close = 0;
        for (int i = start; i < end; i++) { if (source.charAt(i) == '(') open++; if (source.charAt(i) == ')') close++; }
        while (end > start) {
            char c = source.charAt(end - 1);
            if (c == ';') {
                int ampersand = end - 2;
                while (ampersand >= start && asciiLetter(source.charAt(ampersand))) ampersand--;
                if (ampersand >= start && ampersand < end - 2 && source.charAt(ampersand) == '&') { end = ampersand; continue; }
            }
            if (c == ')') { if (close <= open) break; close--; }
            else if ("!\"',.:;?]*_~".indexOf(c) < 0) break;
            end--;
        }
        return end;
    }

    private static boolean asciiLetter(int c) { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'; }
    private static boolean space(int c) { return Character.isWhitespace(c) || Character.isSpaceChar(c) || c == 0xfeff; }
    private static boolean punctuation(int c) {
        int kind = Character.getType(c);
        return kind >= Character.DASH_PUNCTUATION && kind <= Character.OTHER_PUNCTUATION
                || kind >= Character.MATH_SYMBOL && kind <= Character.OTHER_SYMBOL
                || kind == Character.INITIAL_QUOTE_PUNCTUATION || kind == Character.FINAL_QUOTE_PUNCTUATION;
    }
}
