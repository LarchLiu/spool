package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.os.Looper;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadow.api.Shadow;
import org.robolectric.shadows.ShadowToast;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityDownloadTest.ShadowClient.class,
        MainActivityDownloadTest.ShadowFiles.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityDownloadTest {
    @Implements(value = spoolnet.Client.class, isInAndroidSdk = false)
    public static class ShadowClient extends MainActivityGoalTest.ShadowClient {
        @Implementation protected spoolnet.ProjectFiles openProjectFiles() { return Shadow.newInstanceOf(spoolnet.ProjectFiles.class); }
    }
    @Implements(value = spoolnet.ProjectFiles.class, isInAndroidSdk = false)
    public static class ShadowFiles {
        static final List<String> paths = new ArrayList<>();
        static String response = "{status:200,body:'{}'}";
        static int closes, chunks = 80;
        static Runnable during = () -> {};
        static Runnable closing = () -> {};
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected void __constructor__() {}
        @Implementation protected String download(String path, String language, spoolnet.DesktopFileSink sink) throws Exception {
            paths.add(path); assertEquals("en", language); during.run();
            if (response.contains("403")) return response;
            for (int i = 0; i < chunks; i++) sink.write(new byte[256 << 10]);
            return response;
        }
        @Implementation protected void close() {
            synchronized (ShadowFiles.class) { closes++; }
            closing.run();
        }
    }
    private void reset() { ShadowFiles.paths.clear(); ShadowFiles.closes = 0; ShadowFiles.response = "{status:200,body:'{}'}"; ShadowFiles.during = () -> {}; ShadowFiles.closing = () -> {}; }
    private spoolnet.ProjectFiles activeDownload(MainActivityGoalTest.Harness h) {
        return h.<AtomicReference<spoolnet.ProjectFiles>>get("activeDownload").get();
    }
    private static void await(CountDownLatch latch) {
        try { assertTrue("Download worker did not reach its checkpoint", latch.await(5, TimeUnit.SECONDS)); }
        catch (InterruptedException error) { Thread.currentThread().interrupt(); throw new AssertionError(error); }
    }
    private org.robolectric.shadows.ShadowActivity.IntentForResult pick(MainActivityGoalTest.Harness h) {
        ReflectionHelpers.callInstanceMethod(h.activity, "downloadFile",
                ReflectionHelpers.ClassParameter.from(String.class, "/api/chats/chat-one/file?path=large.bin&download=1&workspace=%2Frepo%2Ftree"),
                ReflectionHelpers.ClassParameter.from(String.class, "large.bin"));
        var picker = Shadows.shadowOf(h.activity).getNextStartedActivityForResult();
        assertNotNull(picker); assertEquals(Intent.ACTION_CREATE_DOCUMENT, picker.intent.getAction());
        assertTrue("No HTTP read before a destination is selected", h.io.jobs.isEmpty());
        return picker;
    }
    private void drain(MainActivityGoalTest.Harness h) {
        while (!h.io.jobs.isEmpty()) { h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    }
    @Test public void largeFilesStreamAndSuccessFollowsTheActualOutputClose() throws Exception {
        try (var h = new MainActivityGoalTest.Harness(true)) {
            reset(); var picker = pick(h); Uri uri = Uri.parse("content://com.android.externalstorage.documents/document/primary%3ADownload%2Flarge%20(2).bin");
            long[] bytes = {0}; boolean[] closed = {false};
            Shadows.shadowOf(h.activity.getContentResolver()).registerOutputStream(uri, new OutputStream() {
                @Override public void write(int value) { bytes[0]++; }
                @Override public void write(byte[] data, int offset, int length) {
                    assertTrue(h.<Boolean>get("downloading")); bytes[0] += length;
                    assertFalse(ShadowToast.getTextOfLatestToast().contains("Downloaded to"));
                }
                @Override public void close() { closed[0] = true; }
            });
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri));
            assertTrue(h.<Boolean>get("downloading")); drain(h);
            assertEquals(20L << 20, bytes[0]); assertTrue(closed[0]); assertFalse(h.<Boolean>get("downloading"));
            assertTrue(ShadowToast.getTextOfLatestToast().contains("/Download/large (2).bin"));
            assertEquals(1, ShadowFiles.closes); assertEquals(1, ShadowFiles.paths.size());
        }
    }
    @Test public void cancellationAndExpiredSessionNeverFetchOrWrite() throws Exception {
        for (boolean stale : new boolean[]{false, true}) try (var h = new MainActivityGoalTest.Harness(true)) {
            reset(); var picker = pick(h);
            if (stale) h.set("authEpoch", h.<Integer>get("authEpoch") + 1);
            h.activity.onActivityResult(picker.requestCode, stale ? Activity.RESULT_OK : Activity.RESULT_CANCELED,
                    new Intent().setData(Uri.parse("content://documents/cancelled")));
            drain(h); assertTrue(ShadowFiles.paths.isEmpty()); assertFalse(h.<Boolean>get("downloading"));
            if (!stale) assertEquals("Download cancelled", ShadowToast.getTextOfLatestToast());
        }
    }
    @Test public void permissionWriteAndCloseFailuresAreVisibleAndReleaseTheDownload() throws Exception {
        for (int mode = 0; mode < 3; mode++) try (var h = new MainActivityGoalTest.Harness(true)) {
            reset(); var picker = pick(h); Uri uri = Uri.parse("content://documents/failure");
            final int failure = mode;
            if (mode == 0) ShadowFiles.response = "{status:403,body:'{error:\"File is protected\"}'}";
            Shadows.shadowOf(h.activity.getContentResolver()).registerOutputStream(uri, new ByteArrayOutputStream() {
                @Override public void write(byte[] data, int offset, int length) { if (failure == 1) throw new IllegalStateException("disk full"); }
                @Override public void close() throws IOException { if (failure == 2) throw new IOException("provider close failed"); }
            });
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri)); drain(h);
            assertFalse(h.<Boolean>get("downloading")); assertEquals(1, ShadowFiles.closes);
            assertTrue(ShadowToast.getTextOfLatestToast().contains("Could not download large.bin:"));
            assertTrue(ShadowToast.getTextOfLatestToast().contains(mode == 0 ? "File is protected" : mode == 1 ? "disk full" : "provider close failed"));
        }
    }
    @Test public void accountChangeCancelsAnActiveTransferBeforeItsNextChunk() throws Exception {
        try (var h = new MainActivityGoalTest.Harness(true)) {
            reset(); var picker = pick(h); Uri uri = Uri.parse("content://documents/stale");
            ByteArrayOutputStream output = new ByteArrayOutputStream();
            Shadows.shadowOf(h.activity.getContentResolver()).registerOutputStream(uri, output);
            ShadowFiles.during = () -> h.call("clearWorkspace");
            h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri)); drain(h);
            assertEquals(0, output.size()); assertTrue(ShadowFiles.closes > 0);
            assertFalse(h.<Boolean>get("downloading")); assertNull(activeDownload(h));
            assertFalse(ShadowToast.getTextOfLatestToast().contains("Downloaded to"));
        }
    }

    @Test public void transferCompletionCanRaceAccountCleanupAndActivityDestruction() throws Exception {
        for (boolean destroy : new boolean[]{false, true}) {
            var h = new MainActivityGoalTest.Harness(true);
            CountDownLatch started = new CountDownLatch(1), release = new CountDownLatch(1), finished = new CountDownLatch(1);
            AtomicReference<Throwable> failure = new AtomicReference<>();
            Thread owner = Thread.currentThread(), worker = null;
            boolean closed = false;
            try {
                reset(); var picker = pick(h); Uri uri = Uri.parse("content://documents/concurrent");
                ByteArrayOutputStream output = new ByteArrayOutputStream();
                Shadows.shadowOf(h.activity.getContentResolver()).registerOutputStream(uri, output);
                ShadowFiles.during = () -> { started.countDown(); await(release); };
                ShadowFiles.closing = () -> {
                    if (Thread.currentThread() != owner) return;
                    // Finish on the IO thread while lifecycle cleanup is still closing the handle.
                    assertNull("Cancellation must detach the handle before closing it", activeDownload(h));
                    release.countDown(); await(finished);
                };
                h.activity.onActivityResult(picker.requestCode, Activity.RESULT_OK, new Intent().setData(uri));
                Runnable transfer = h.io.jobs.remove(0);
                worker = new Thread(() -> {
                    try { transfer.run(); }
                    catch (Throwable error) { failure.set(error); }
                    finally { finished.countDown(); }
                }, "download-completion-test");
                worker.start(); await(started);
                if (destroy) { closed = true; h.close(); }
                else h.call("clearWorkspace");
                assertNull(failure.get()); assertNull(activeDownload(h)); assertEquals(0, output.size());
                assertTrue(ShadowFiles.closes > 0);
                if (!destroy) assertFalse(h.<Boolean>get("downloading"));
                assertFalse(ShadowToast.getTextOfLatestToast().contains("Downloaded to"));
            } finally {
                release.countDown();
                try {
                    if (worker != null) { worker.join(5000); assertFalse("Download worker did not stop", worker.isAlive()); }
                } finally {
                    ShadowFiles.during = () -> {}; ShadowFiles.closing = () -> {};
                    if (!closed) h.close();
                }
            }
        }
    }
}
