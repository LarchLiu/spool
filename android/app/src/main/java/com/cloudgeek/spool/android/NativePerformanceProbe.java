package com.cloudgeek.spool.android;

import java.util.UUID;

/** Content-free debug target: never gesture a different page or background Activity. */
final class NativePerformanceProbe {
    final String token = UUID.randomUUID().toString();

    String value(String[] args, String command, boolean interactive, int selection) {
        if (!interactive || args == null || args.length != 5 || !args[0].equals("spool-performance")
                || !args[1].equals(command) || !token.equals(args[3]) || !Integer.toString(selection).equals(args[4])) return null;
        return args[2];
    }

    Integer fling(String[] args, boolean interactive, int selection) {
        String value = value(args, "fling", interactive, selection);
        if (value == null) return null;
        try { return Math.max(-10000, Math.min(10000, Integer.parseInt(value))); }
        catch (NumberFormatException ignored) { return null; }
    }

    Integer position(String[] args, boolean interactive, int selection) {
        String value = value(args, "position", interactive, selection);
        if (value == null) return null;
        try { int position = Integer.parseInt(value); return position < 0 ? null : position; }
        catch (NumberFormatException ignored) { return null; }
    }

    Boolean reuse(String[] args, boolean interactive, int selection) {
        String value = value(args, "activity-reuse", interactive, selection);
        return "on".equals(value) ? Boolean.TRUE : "off".equals(value) ? Boolean.FALSE : null;
    }
}
