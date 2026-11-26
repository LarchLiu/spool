package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.ClipData;
import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Intent;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.Looper;
import android.os.SystemClock;
import android.provider.OpenableColumns;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.widget.TextView;
import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowContentResolver;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeProjectUploadTest {
    static JSONObject json(String text) { try { return new JSONObject(text); } catch (Exception e) { throw new AssertionError(e); } }
    static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    static void layout(View root) {
        root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, 360, 800); idle();
    }
    static final class Provider extends ContentProvider {
        String name = "中文 & report.bin";
        Long size;
        public boolean onCreate() { return true; }
        public String getType(Uri uri) { return "application/octet-stream"; }
        public Cursor query(Uri uri, String[] projection, String selection, String[] args, String sort) {
            MatrixCursor cursor = new MatrixCursor(new String[]{OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE});
            cursor.addRow(new Object[]{name, size}); return cursor;
        }
        public Uri insert(Uri uri, ContentValues value) { return null; }
        public int delete(Uri uri, String selection, String[] args) { return 0; }
        public int update(Uri uri, ContentValues values, String selection, String[] args) { return 0; }
    }
    static Provider provider(Activity activity) {
        Provider provider = new Provider(); android.content.pm.ProviderInfo info = new android.content.pm.ProviderInfo(); info.authority = "project-documents";
        provider.attachInfo(activity, info); ShadowContentResolver.registerProviderInternal(info.authority, provider); return provider;
    }
    static Uri document(Activity activity, String id, InputStream input) {
        Uri uri = Uri.parse("content://project-documents/" + id);
        Shadows.shadowOf(activity.getContentResolver()).registerInputStream(uri, input); return uri;
    }
    static final class Transport implements NativeProjectUpload.Transport {
        final List<String> paths = new ArrayList<>(), names = new ArrayList<>();
        final List<byte[]> bytes = new ArrayList<>();
        int closes;
        Runnable duringUpload = () -> {};
        Exception failure;
        public void upload(String path, String name, InputStream input) throws Exception {
            paths.add(path); names.add(name); duringUpload.run();
            byte[] buffer = new byte[256 << 10]; java.io.ByteArrayOutputStream output = new java.io.ByteArrayOutputStream(); int count;
            while ((count = input.read(buffer)) >= 0) output.write(buffer, 0, count);
            bytes.add(output.toByteArray()); if (failure != null) throw failure;
        }
        public void close() { closes++; }
    }
    private static final class Fixture implements AutoCloseable {
        { org.robolectric.RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi"); }
        final ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup().visible();
        final NativeUi ui = new NativeUi(activity.get(), false, "en");
        final NativeFileModel state = new NativeFileModel("project-one", "chat-one");
        final NativeFileBrowser browser;
        final ArrayDeque<Runnable> jobs = new ArrayDeque<>();
        final Transport transport = new Transport();
        final List<Exception> failures = new ArrayList<>();
        final List<String> reads = new ArrayList<>();
        final NativeProjectUpload upload;
        boolean allowed = true, current = true;
        Fixture() {
            state.workspace = json("{path:'/repo/work tree',available:true}");
            state.accept(".", json("{entries:[{name:'src',path:'src',kind:'directory'},{name:'root.txt',path:'root.txt',kind:'file'}]}"));
            state.accept("src", json("{entries:[{name:'nested.txt',path:'src/nested.txt',kind:'file'}]}"));
            state.expanded.add("src"); state.requested.add("."); state.requested.add("src");
            browser = new NativeFileBrowser(ui, "Project", state, null, (path, done) -> {
                reads.add(path);
                if (path.endsWith("/workspace")) done.done(json("{path:'/repo/work tree',available:true}"), null);
                else done.done(json("{entries:[]}"), null);
            }, file -> {}, link -> {}, () -> {}, split -> {});
            activity.get().setContentView(browser); layout(browser);
            activity.windowFocusChanged(true);
            upload = new NativeProjectUpload(activity.get(), ui, browser, jobs::add, () -> transport,
                    () -> current, () -> allowed, failures::add);
            browser.canUpload = () -> allowed; browser.upload = upload::pick; browser.uploadScopeChanged = upload::cancel;
        }
        int pick(String path) {
            upload.pick(path);
            var started = Shadows.shadowOf(activity.get()).getNextStartedActivityForResult(); assertNotNull(started);
            assertEquals(Intent.ACTION_OPEN_DOCUMENT, started.intent.getAction()); assertTrue(started.intent.hasCategory(Intent.CATEGORY_OPENABLE));
            assertTrue(started.intent.getBooleanExtra(Intent.EXTRA_ALLOW_MULTIPLE, false)); return started.requestCode;
        }
        void drain() { while (!jobs.isEmpty()) { jobs.remove().run(); idle(); } }
        void longPress(float x, float y) {
            assertTrue("List is enabled", browser.list.isEnabled());
            long down = SystemClock.uptimeMillis();
            MotionEvent start = MotionEvent.obtain(down, down, MotionEvent.ACTION_DOWN, x, y, 0); assertTrue(browser.list.dispatchTouchEvent(start)); start.recycle();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(ViewConfiguration.getLongPressTimeout() + 100));
            assertNotNull("Long press at " + x + "," + y + "; list size " + browser.list.getWidth() + "x" + browser.list.getHeight()
                    + "; position " + browser.list.pointToPosition((int) x, (int) y) + "; window focus " + browser.list.hasWindowFocus(), ui.activePopover);
            MotionEvent end = MotionEvent.obtain(down, SystemClock.uptimeMillis(), MotionEvent.ACTION_UP, x, y, 0); browser.list.dispatchTouchEvent(end); end.recycle(); idle();
        }
        void destination(String expected) {
            assertNotNull(ui.activePopover); assertTrue(ui.activePopover.isShowing());
            assertEquals("Upload to " + expected, ((TextView) ui.activePopover.content.getChildAt(0)).getText().toString());
        }
        @Override public void close() { upload.close(); browser.deactivate(); activity.close(); }
    }

    @Test public void realLongPressTargetsFoldersFileParentsAndBlankRootWithoutOpeningPreview() throws Exception {
        try (Fixture f = new Fixture()) {
            View folder = f.browser.list.getChildAt(0);
            f.longPress(f.browser.list.getWidth() - 30, folder.getTop() + folder.getHeight() / 2f); f.destination("src");
            View panel = f.ui.activePopover.panel(); assertTrue(panel.getWidth() > 0); assertTrue(panel.getHeight() > 0);
            java.nio.file.Path artifacts = java.nio.file.Path.of(System.getProperty("spool.ui.artifacts"), "project-upload");
            java.nio.file.Files.createDirectories(artifacts);
            android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(panel.getWidth(), panel.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
            panel.draw(new android.graphics.Canvas(image));
            try (var output = java.nio.file.Files.newOutputStream(artifacts.resolve("menu-api" + android.os.Build.VERSION.SDK_INT + ".png"))) {
                assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
            } finally { image.recycle(); }
            assertTrue(f.state.expanded.contains("src")); assertNull(f.state.target);
            f.ui.activePopover.dismiss();
            View file = f.browser.list.getChildAt(1);
            f.longPress(80, file.getTop() + file.getHeight() / 2f); f.destination("src"); assertNull(f.state.target);
            f.ui.activePopover.dismiss();
            View last = f.browser.list.getChildAt(f.browser.list.getChildCount() - 1);
            f.longPress(100, last.getBottom() + 50); f.destination("/");
            f.ui.activePopover.content.getChildAt(1).performClick(); assertTrue(f.upload.waiting());
            assertNotNull(Shadows.shadowOf(f.activity.get()).getNextStartedActivityForResult());
        }
    }

    @Test public void emptyDirectoryAndPermissionChangesKeepTheMenuUnavailableUntilAuthorized() {
        try (Fixture f = new Fixture()) {
            f.allowed = false; f.browser.list.getOnItemLongClickListener().onItemLongClick(f.browser.list, f.browser.list.getChildAt(0), 0, 0); f.destination("src");
            assertFalse(f.ui.activePopover.content.getChildAt(1).isEnabled());
            assertEquals("File upload permission is disabled", ((TextView) f.ui.activePopover.content.getChildAt(2)).getText().toString());
            f.ui.activePopover.dismiss(); f.allowed = true;
            f.browser.list.getOnItemLongClickListener().onItemLongClick(f.browser.list, f.browser.list.getChildAt(0), 0, 0);
            View upload = f.ui.activePopover.content.getChildAt(1); f.allowed = false; upload.performClick();
            assertFalse(f.upload.waiting()); assertNull(Shadows.shadowOf(f.activity.get()).getNextStartedActivityForResult());
            f.state.resetDirectories(); f.state.accept(".", json("{entries:[]}")); f.browser.renderTree(); layout(f.browser);
            assertEquals(View.VISIBLE, f.browser.empty.getVisibility()); assertTrue(f.browser.empty.performLongClick()); f.destination("/");
        }
    }

    @Test public void scrollingDoesNotOpenAnUploadMenu() {
        try (Fixture f = new Fixture()) {
            long down = SystemClock.uptimeMillis(); float y = f.browser.list.getHeight() - 40;
            for (int action : new int[]{MotionEvent.ACTION_DOWN, MotionEvent.ACTION_MOVE}) {
                MotionEvent event = MotionEvent.obtain(down, SystemClock.uptimeMillis(), action, 100, action == MotionEvent.ACTION_DOWN ? y : y - 150, 0);
                f.browser.list.dispatchTouchEvent(event); event.recycle();
            }
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(ViewConfiguration.getLongPressTimeout() + 100));
            assertNull(f.ui.activePopover);
            MotionEvent up = MotionEvent.obtain(down, SystemClock.uptimeMillis(), MotionEvent.ACTION_UP, 100, y - 150, 0);
            f.browser.list.dispatchTouchEvent(up); up.recycle();
        }
    }

    @Test public void pickerRoundTripStreamsMultipleDocumentsToCapturedWorktreeAndRefreshesTarget() {
        try (Fixture f = new Fixture()) {
            provider(f.activity.get()); byte[] payload = new byte[17 << 20]; payload[123] = (byte) 255;
            Uri first = document(f.activity.get(), "first", new ByteArrayInputStream(payload));
            Uri second = document(f.activity.get(), "second", new ByteArrayInputStream(new byte[]{0, 1, (byte) 255}));
            int request = f.pick("src"); f.browser.deactivate();
            ClipData clips = ClipData.newRawUri("files", first); clips.addItem(new ClipData.Item(second));
            Intent result = new Intent(); result.setClipData(clips);
            f.upload.result(request, Activity.RESULT_OK, result); f.browser.resume(); f.drain();
            assertEquals(List.of("/api/chats/chat-one/upload?path=src&workspace=%2Frepo%2Fwork%20tree",
                    "/api/chats/chat-one/upload?path=src&workspace=%2Frepo%2Fwork%20tree"), f.transport.paths);
            assertEquals(List.of("中文 & report.bin", "中文 & report.bin"), f.transport.names);
            assertArrayEquals(payload, f.transport.bytes.get(0)); assertArrayEquals(new byte[]{0, 1, (byte) 255}, f.transport.bytes.get(1));
            assertTrue(f.reads.stream().anyMatch(path -> path.contains("/files?path=src")));
            assertFalse(f.browser.uploadBusy); assertTrue(f.failures.isEmpty()); assertEquals(1, f.transport.closes);
            assertTrue(((TextView) f.browser.notices.getChildAt(0)).getText().toString().contains("Uploaded 2 files to src"));
        }
    }

    @Test public void changedScopeOwnerPermissionAndClosedPickerNeverUploadStaleSelections() {
        for (int mode = 0; mode < 5; mode++) try (Fixture f = new Fixture()) {
            provider(f.activity.get()); Uri uri = document(f.activity.get(), "test", new ByteArrayInputStream(new byte[]{1}));
            int request = f.pick("src");
            if (mode == 0) f.state.workspace = json("{path:'/other',available:true}");
            if (mode == 1) f.state.clearScope();
            if (mode == 2) f.current = false;
            if (mode == 3) f.allowed = false;
            if (mode == 4) f.upload.close();
            f.upload.result(request, Activity.RESULT_OK, new Intent().setData(uri)); f.drain();
            assertTrue(f.transport.paths.isEmpty()); assertFalse(f.browser.uploadBusy);
            assertEquals(mode == 3 ? 1 : 0, f.failures.size());
        }
    }

    @Test public void providerSizeLimitConflictAndMidTransferCancellationStopTheBatchAndCloseSources() {
        for (int mode = 0; mode < 4; mode++) try (Fixture f = new Fixture()) {
            Provider provider = provider(f.activity.get()); boolean[] closed = {false};
            InputStream input = new ByteArrayInputStream(new byte[]{1, 2, 3}) { @Override public void close() { closed[0] = true; } };
            if (mode == 0) provider.size = (64L << 20) + 1;
            if (mode == 1) input = new InputStream() {
                long remaining = (64L << 20) + 1;
                @Override public int read() { return remaining-- > 0 ? 0 : -1; }
                @Override public int read(byte[] data, int start, int count) {
                    if (remaining == 0) return -1; int length = (int) Math.min(remaining, count); remaining -= length; return length;
                }
                @Override public void close() { closed[0] = true; }
            };
            if (mode == 2) f.transport.failure = new java.io.IOException("A file with this name already exists");
            if (mode == 3) f.transport.duringUpload = f.upload::cancel;
            Uri first = document(f.activity.get(), "first", input), second = document(f.activity.get(), "second", new ByteArrayInputStream(new byte[]{4}));
            int request = f.pick("."); ClipData clips = ClipData.newRawUri("files", first); clips.addItem(new ClipData.Item(second));
            Intent result = new Intent(); result.setClipData(clips);
            f.upload.result(request, Activity.RESULT_OK, result); f.drain();
            assertEquals(mode == 0 ? 0 : 1, f.transport.paths.size()); assertFalse(f.browser.uploadBusy);
            if (mode != 0) { assertTrue(closed[0]); assertEquals(1, f.transport.closes); }
            assertEquals(mode == 3 ? 0 : 1, f.failures.size());
            if (mode < 2) assertTrue(f.failures.get(0).getMessage().contains("64 MB"));
        }
    }
}
