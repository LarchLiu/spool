package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class NativeFileTargetTest {
    @Test public void matchesWebLocalLocationsWithoutOpeningDeviceFiles() {
        NativeFileTarget target = NativeFileTarget.parse("src/native.java#L42C3");
        assertEquals("src/native.java", target.path);
        assertEquals(42, target.line);
        assertEquals(3, target.column);
        assertEquals("src/a+b.java", NativeFileTarget.parse("src/a+b.java").path);
        assertEquals("/project/a b.md", NativeFileTarget.parse("file:///project/a%20b.md:9").path);
        assertEquals("C:/project/source.ts", NativeFileTarget.parse("C:\\project\\source.ts:23").path);
        assertEquals(12, NativeFileTarget.parse("source.ts:12:4").line);
        assertEquals(0, NativeFileTarget.parse("source.ts:1000001").line);
    }

    @Test public void rejectsActiveRemoteAndEncodedTargets() {
        for (String unsafe : new String[] {"javascript:alert(1)", "javascript%3Aalert(1)",
                "data:text/html,test", "%64ata:text/html,test", "vbscript:test", "//tracker.example/x",
                "file://remote/etc/passwd", "file:relative", "src/%00.java", "https://example.com/image.png"})
            assertNull(unsafe, NativeFileTarget.parse(unsafe));
        assertTrue(NativeFileTarget.isSafeLink("https://example.com/docs"));
        assertTrue(NativeFileTarget.isSafeLink("src/file.java:42"));
        assertFalse(NativeFileTarget.isSafeLink("https://example.com/%0a"));
        assertFalse(NativeFileTarget.isSafeLink("javascript:alert(1)"));
    }
}
