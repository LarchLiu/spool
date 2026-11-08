package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.time.Instant;
import java.util.SimpleTimeZone;
import java.util.TimeZone;
import org.junit.Test;

public class NativeMessageTimeTest {
    private static long instant(String value) { return Instant.parse(value).toEpochMilli(); }

    @Test public void repeatedBindingsReuseResultsButDayYearLocaleAndZoneChangesStillUpdate() {
        long timestamp = instant("2026-11-08T03:00:00Z"); TimeZone utc = TimeZone.getTimeZone("UTC");
        NativeMessageTime first = NativeMessageTime.format(timestamp, "en", timestamp, utc);
        assertEquals("03:00", first.label);
        for (int i = 0; i < 100; i++) assertSame(first, NativeMessageTime.format(timestamp, "en", timestamp + i * 1000, utc));
        NativeMessageTime tomorrow = NativeMessageTime.format(timestamp, "en", instant("2026-11-09T00:00:00Z"), utc);
        assertEquals("11/08, 03:00", tomorrow.label); assertNotSame(first, tomorrow);
        assertEquals("11/08/2026, 03:00", NativeMessageTime.format(timestamp, "en", instant("2027-01-01T00:00:00Z"), utc).label);
        assertEquals("11/08 03:00", NativeMessageTime.format(timestamp, "zh-CN", instant("2026-11-09T00:00:00Z"), utc).label);
        assertEquals("11:00", NativeMessageTime.format(timestamp, "en", timestamp, TimeZone.getTimeZone("Asia/Shanghai")).label);
        assertEquals(first.dateTime, tomorrow.dateTime);
    }

    @Test public void mutableCustomTimeZonesCannotPoisonCachedKeysOrFormatters() {
        long timestamp = instant("2026-11-08T03:00:00Z");
        SimpleTimeZone zone = new SimpleTimeZone(0, "same-id");
        NativeMessageTime initial = NativeMessageTime.format(timestamp, "en", timestamp, zone);
        zone.setRawOffset(3600000);
        NativeMessageTime changed = NativeMessageTime.format(timestamp, "en", timestamp, zone);
        assertEquals("03:00", initial.label); assertEquals("04:00", changed.label);
        zone.setRawOffset(0); assertSame(initial, NativeMessageTime.format(timestamp, "en", timestamp, zone));
    }

    @Test public void cachesAreBoundedAndFormatterInstancesAreReusedForNewTimestamps() throws Exception {
        java.util.concurrent.atomic.AtomicReference<Throwable> failure = new java.util.concurrent.atomic.AtomicReference<>();
        Thread thread = new Thread(() -> {
            try {
                long timestamp = instant("2026-11-08T03:00:00Z"); TimeZone utc = TimeZone.getTimeZone("UTC");
                for (int i = 0; i < 1000; i++) NativeMessageTime.format(timestamp + i, "en", timestamp, utc);
                assertEquals(128, NativeMessageTime.cachedResults());
                assertEquals("Only three formatting patterns are needed", 3, NativeMessageTime.formatterCreates());
            } catch (Throwable error) { failure.set(error); }
        });
        thread.start(); thread.join(5000); assertFalse(thread.isAlive());
        if (failure.get() != null) throw new AssertionError(failure.get());
    }
}
