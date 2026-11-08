package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class NativePerformanceProbeTest {
    private String[] command(NativePerformanceProbe probe, String speed, int selection) {
        return new String[] {"spool-performance", "fling", speed, probe.token, Integer.toString(selection)};
    }

    @Test public void onlyTheExactInteractivePageCanFling() {
        NativePerformanceProbe probe = new NativePerformanceProbe();
        String[] args = command(probe, "-7000", 4);
        assertEquals(Integer.valueOf(-7000), probe.fling(args, true, 4));
        assertNull(probe.fling(args, false, 4));
        assertNull(probe.fling(args, true, 5));
        assertNull(new NativePerformanceProbe().fling(args, true, 4));
    }

    @Test public void legacyMalformedAndUnboundedGesturesCannotBypassTargetChecks() {
        NativePerformanceProbe probe = new NativePerformanceProbe();
        assertNull(probe.fling(new String[] {"spool-performance", "fling", "7000"}, true, 1));
        assertNull(probe.fling(null, true, 1));
        assertNull(probe.fling(command(probe, "invalid", 1), true, 1));
        assertNull(probe.fling(command(probe, "2147483648", 1), true, 1));
        assertEquals(Integer.valueOf(-10000), probe.fling(command(probe, "-10001", 1), true, 1));
        assertEquals(Integer.valueOf(10000), probe.fling(command(probe, "10001", 1), true, 1));
        String[] args = command(probe, "7000", 1); args[0] = "other";
        assertNull(probe.fling(args, true, 1));
        args[0] = "spool-performance"; args[1] = "other";
        assertNull(probe.fling(args, true, 1));
    }

    @Test public void positioningAndReuseControlsRequireTheSameFocusedPage() {
        NativePerformanceProbe probe = new NativePerformanceProbe();
        String[] args = {"spool-performance", "position", "120", probe.token, "4"};
        assertEquals(Integer.valueOf(120), probe.position(args, true, 4));
        assertNull(probe.position(args, false, 4)); assertNull(probe.position(args, true, 5));
        assertNull(new NativePerformanceProbe().position(args, true, 4));
        for (String value : new String[] {"-1", "bad", "2147483648"}) {
            args[2] = value; assertNull(probe.position(args, true, 4));
        }
        args[1] = "activity-reuse"; args[2] = "off";
        assertEquals(Boolean.FALSE, probe.reuse(args, true, 4));
        assertNull(probe.reuse(args, false, 4)); assertNull(probe.reuse(args, true, 5));
        assertNull(new NativePerformanceProbe().reuse(args, true, 4));
        args[2] = "on"; assertEquals(Boolean.TRUE, probe.reuse(args, true, 4));
        args[2] = "other"; assertNull(probe.reuse(args, true, 4));
    }
}
