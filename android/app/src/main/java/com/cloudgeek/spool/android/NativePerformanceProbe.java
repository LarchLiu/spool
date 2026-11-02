package com.cloudgeek.spool.android;

import java.util.UUID;

/** Content-free debug target: never gesture a different page or background Activity. */
final class NativePerformanceProbe {
    final String token = UUID.randomUUID().toString();

    Integer fling(String[] args, boolean interactive, int selection) {
        if (!interactive || args == null || args.length != 5 || !args[0].equals("spool-performance")
                || !args[1].equals("fling") || !token.equals(args[3]) || !Integer.toString(selection).equals(args[4])) return null;
        try { return Math.max(-10000, Math.min(10000, Integer.parseInt(args[2]))); }
        catch (NumberFormatException ignored) { return null; }
    }
}
