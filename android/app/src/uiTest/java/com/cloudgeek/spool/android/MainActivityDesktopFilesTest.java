package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** Real Activity lifecycle and SAF result routing, without a live account or server. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityDesktopFilesTest {
    private static void idle() { org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); }
    @Test public void documentPickerRetainsTheViewButNormalBackgroundingClosesIt() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.call("openDesktop"); NativeDesktopDialog desktop = h.get("desktopDialog");
            int[] selected = {0};
            desktop.documents.pick(new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*"), uri -> selected[0]++);
            h.controller.pause().stop(); assertTrue(desktop.isShowing()); assertSame(desktop, h.get("desktopDialog"));
            h.controller.restart().start().resume();
            h.activity.onActivityResult(desktop.documents.requestCode, Activity.RESULT_CANCELED, null);
            assertEquals(1, selected[0]); assertFalse(desktop.documents.waiting());
            h.controller.pause().stop(); idle();
            assertNull("Normal backgrounding must detach the desktop", h.get("desktopDialog"));
            assertFalse("Normal backgrounding must close the desktop window", desktop.isShowing());
            h.controller.restart().start().resume();
        }
    }
    @Test public void closingTheViewDiscardsLateDocumentPickerResults() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.call("openDesktop"); NativeDesktopDialog desktop = h.get("desktopDialog");
            int[] selected = {0};
            desktop.documents.pick(new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*"), uri -> selected[0]++);
            h.call("closeDesktop");
            idle();
            h.activity.onActivityResult(desktop.documents.requestCode, Activity.RESULT_OK,
                    new Intent().setData(Uri.parse("content://documents/late")));
            assertEquals(0, selected[0]); assertFalse(desktop.isShowing()); assertFalse(desktop.documents.waiting());
            h.call("openDesktop"); NativeDesktopDialog next = h.get("desktopDialog");
            next.documents.pick(new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("*/*"), uri -> selected[0]++);
            h.activity.onActivityResult(desktop.documents.requestCode, Activity.RESULT_OK,
                    new Intent().setData(Uri.parse("content://documents/previous-server")));
            assertEquals(0, selected[0]); assertTrue(next.documents.waiting());
            h.activity.onActivityResult(next.documents.requestCode, Activity.RESULT_CANCELED, null);
            assertEquals(1, selected[0]); assertFalse(next.documents.waiting());
        }
    }
}
