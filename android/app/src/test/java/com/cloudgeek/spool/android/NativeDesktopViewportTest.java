package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import org.junit.Test;

public class NativeDesktopViewportTest {
    @Test public void fitAndZoomKeepRemotePointUnderMovingPinchMidpoint() {
        NativeDesktopViewport v = new NativeDesktopViewport();
        v.resize(400, 800, 1920, 1080);
        assertEquals(960, v.remoteX(200), .001); assertEquals(540, v.remoteY(400), .001);
        v.zoomAt(3, 200, 400, 200, 400);
        double x = v.remoteX(180), y = v.remoteY(390);
        v.zoomAt(4, 180, 390, 210, 420);
        assertEquals(x, v.remoteX(210), .001); assertEquals(y, v.remoteY(420), .001);
    }
    @Test public void limitsAndLetterboxingNeverMapOutsideDesktop() {
        NativeDesktopViewport v = new NativeDesktopViewport(); v.resize(400, 800, 1920, 1080);
        assertFalse(v.contains(200, 100)); assertTrue(v.contains(200, 400));
        v.zoomAt(100, 200, 400, 200, 400); assertEquals(5, v.zoom, 0);
        v.pan(100000, -100000);
        assertTrue(v.remoteX(0) >= 0); assertTrue(v.remoteY(800) <= 1079);
        v.reset(); assertEquals(0, v.x, 0); assertEquals(0, v.y, 0); assertEquals(1, v.zoom, 0);
        v.resize(800, 400, 1920, 1080);
        assertEquals(960, v.remoteX(400), .001); assertEquals(540, v.remoteY(200), .001);
    }
    @Test public void unicodeKeysMatchVoxlKeysyms() {
        assertEquals(65, NativeDesktopProtocol.keysym('A'));
        assertEquals(0xe9, NativeDesktopProtocol.keysym(0xe9));
        assertEquals(0x01004e2d, NativeDesktopProtocol.keysym('中'));
        assertEquals(0x0101f642, NativeDesktopProtocol.keysym(0x1f642));
    }
}
