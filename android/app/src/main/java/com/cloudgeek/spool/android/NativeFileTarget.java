package com.cloudgeek.spool.android;

import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Same local-file link policy as web/src/local-file-links.ts, without touching local storage. */
final class NativeFileTarget {
    final String path;
    final int line, column;
    NativeFileTarget(String path, int line, int column) {
        this.path = path;
        this.line = line;
        this.column = column;
    }
    private static final Pattern LOCATION = Pattern.compile("(?:#L?(\\d+)(?:C(\\d+))?(?:-L?\\d+)?|:(\\d+)(?::(\\d+))?)$", Pattern.CASE_INSENSITIVE);
    private static final Pattern SCHEME = Pattern.compile("^[a-z][a-z\\d+.-]*:", Pattern.CASE_INSENSITIVE);
    private static final Pattern CONTROL = Pattern.compile("[\\x00-\\x1f]");

    static NativeFileTarget parse(String href) {
        return parse(href, "");
    }

    static NativeFileTarget parse(String href, String basePath) {
        if (href == null) return null;
        String path = href.trim().replaceAll("^<|>$", "");
        if (path.isEmpty() || path.startsWith("#") || path.startsWith("//") || CONTROL.matcher(path).find()) return null;
        try {
            if (path.regionMatches(true, 0, "file:", 0, 5)) {
                URI uri = new URI(path);
                if (uri.getHost() != null && !uri.getHost().equalsIgnoreCase("localhost")) return null;
                if (uri.getRawPath() == null) return null;
                path = uri.getRawPath() + (uri.getRawFragment() == null ? "" : "#" + uri.getRawFragment());
                if (path.matches("^/[A-Za-z]:/.*")) path = path.substring(1);
            } else if (SCHEME.matcher(path).find() && !path.matches("^[A-Za-z]:[\\\\/].*")
                    && !path.matches("(?i)^[^/?#]+\\.[a-z\\d]+:\\d+(?::\\d+)?$")) return null;
            path = URLDecoder.decode(path.replace("+", "%2B"), StandardCharsets.UTF_8.name());
        } catch (Exception invalid) { return null; }
        if (CONTROL.matcher(path).find() || path.matches("(?is)^(https?|javascript|data|vbscript|mailto|tel):.*") || path.startsWith("//")) return null;
        Matcher location = LOCATION.matcher(path);
        int line = 0, column = 0;
        if (location.find()) {
            line = number(location.group(1) == null ? location.group(3) : location.group(1));
            column = number(location.group(2) == null ? location.group(4) : location.group(2));
            path = path.substring(0, location.start());
        } else path = path.replaceFirst("#.*$", "");
        path = path.replace('\\', '/');
        if (!basePath.isEmpty() && !path.startsWith("/") && !path.matches("(?i)^[a-z]:/.*"))
            path = basePath.substring(0, basePath.lastIndexOf('/') + 1) + path;
        return path.isEmpty() || path.contains("://") ? null : new NativeFileTarget(path, line, column);
    }

    static boolean isSafeLink(String href) {
        if (href == null) return false;
        try {
            String decoded = URLDecoder.decode(href.replace("+", "%2B"), StandardCharsets.UTF_8.name());
            return OriginPolicy.isExternalWebURL(href) && OriginPolicy.isExternalWebURL(decoded) || parse(href) != null;
        } catch (Exception invalid) { return false; }
    }

    private static int number(String raw) {
        try { int value = Integer.parseInt(raw); return value > 0 && value <= 1_000_000 ? value : 0; }
        catch (Exception invalid) { return 0; }
    }
}
