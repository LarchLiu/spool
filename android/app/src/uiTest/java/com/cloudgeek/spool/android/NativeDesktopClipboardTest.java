package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.os.Looper;
import android.os.Parcel;
import android.view.KeyEvent;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.view.inputmethod.InputContentInfo;
import java.io.ByteArrayOutputStream;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadow.api.Shadow;
import org.robolectric.shadows.ShadowClipboardManager;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeDesktopClipboardTest {
    static final class Queue implements Executor {
        final ArrayDeque<Runnable> tasks = new ArrayDeque<>();
        public void execute(Runnable value) { tasks.add(value); }
        void drain() { while (!tasks.isEmpty()) tasks.remove().run(); idle(); }
    }
    static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    static KeyEvent key(int action, int code, int modifiers) { return new KeyEvent(0, 0, action, code, 0, modifiers); }
    static void ready(NativeDesktopView view, boolean readOnly, boolean mac) throws Exception {
        var layout = NativeDesktopView.class.getDeclaredField("layout"); layout.setAccessible(true);
        layout.set(view, new NativeDesktopProtocol.Layout(new JSONObject().put("w", 100).put("h", 100).put("readOnly", readOnly).put("os", mac ? "macos" : "linux")));
        var active = NativeDesktopView.class.getDeclaredField("active"); active.setAccessible(true); active.setBoolean(view, true);
    }
    static byte[] png(int color) {
        Bitmap bitmap = Bitmap.createBitmap(2, 2, Bitmap.Config.ARGB_8888); bitmap.eraseColor(color);
        ByteArrayOutputStream out = new ByteArrayOutputStream(); bitmap.compress(Bitmap.CompressFormat.PNG, 100, out); bitmap.recycle();
        return out.toByteArray();
    }
    static byte[] packet(byte[] png) {
        byte[] packet = new byte[png.length + 1]; packet[0] = 4; System.arraycopy(png, 0, packet, 1, png.length); return packet;
    }
    static final class Fixture implements AutoCloseable {
        final Activity activity;
        final Queue io = new Queue();
        final List<String> commands = new ArrayList<>();
        final List<byte[]> images = new ArrayList<>();
        final List<String> errors = new ArrayList<>();
        final ClipboardManager system;
        final org.robolectric.android.controller.ContentProviderController<NativeDesktopClipboardProvider> provider;
        final NativeDesktopView view;
        final NativeDesktopClipboard clipboard;
        Fixture(Activity activity) throws Exception {
            this.activity = activity;
            system = (ClipboardManager) activity.getSystemService(Context.CLIPBOARD_SERVICE);
            // Attach the manifest provider for this test's cache directory before
            // FileProvider resolves a URI; AndroidX caches roots by authority.
            provider = Robolectric.buildContentProvider(NativeDesktopClipboardProvider.class).create();
            view = new NativeDesktopView(new NativeUi(activity, false), commands::add, reason -> fail(reason), () -> {}, () -> {});
            activity.setContentView(view); ready(view, false, false);
            clipboard = new NativeDesktopClipboard(activity, io, view::controllable, view::pasteText,
                    (bytes, modifiers) -> { images.add(bytes); commands.add("image"); view.pasteShortcut(modifiers); },
                    () -> errors.add("clipboard"), "Remote desktop");
            view.clipboard(clipboard); clipboard.focus(true); commands.clear();
        }
        public void close() { clipboard.close(); view.close(); io.drain(); provider.shutdown(); }
    }
    @Test public void remoteTextWaitsForFocusAndCloseRejectsLateUpdates() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.system.setPrimaryClip(ClipData.newPlainText("local", "sentinel"));
            f.clipboard.focus(false); f.clipboard.remoteText("旧内容"); f.clipboard.remoteText("中文\ntext,😀");
            assertEquals("sentinel", f.system.getPrimaryClip().getItemAt(0).getText());
            f.clipboard.focus(true);
            assertEquals("中文\ntext,😀", f.system.getPrimaryClip().getItemAt(0).getText());
            assertTrue("Remote clipboard writes must never echo back", f.commands.isEmpty());
            f.clipboard.close(); f.clipboard.remoteText("late");
            assertEquals("中文\ntext,😀", f.system.getPrimaryClip().getItemAt(0).getText());
        }
    }
    @Test public void toolbarImeAndExternalPasteSendClipboardBeforeHostShortcut() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            String value = "中文\ntext,😀";
            f.system.setPrimaryClip(ClipData.newPlainText("local", value));
            assertTrue(f.commands.isEmpty());
            assertTrue(f.clipboard.paste());
            assertEquals(List.of("c," + value, "k,65507,1", "k,118,1", "k,118,0", "k,65507,0"), f.commands);
            f.commands.clear(); ready(f.view, false, true);
            InputConnection input = f.view.onCreateInputConnection(new EditorInfo());
            assertTrue(input.performContextMenuAction(android.R.id.paste));
            assertTrue(f.commands.contains("k,65515,1"));
            assertTrue(f.commands.indexOf("c," + value) < f.commands.indexOf("k,118,1"));
            f.commands.clear();
            KeyEvent paste = new KeyEvent(0, 0, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_V, 0, KeyEvent.META_CTRL_ON);
            assertTrue(f.view.onKeyDown(KeyEvent.KEYCODE_V, paste));
            assertTrue(f.view.onKeyUp(KeyEvent.KEYCODE_V, new KeyEvent(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_V)));
            assertEquals(1, f.commands.stream().filter(command -> command.equals("c," + value)).count());
            f.commands.clear(); f.view.copy();
            assertEquals(List.of("r", "k,65515,1", "k,99,1", "k,99,0", "k,65515,0"), f.commands);
            f.commands.clear(); ready(f.view, true, true);
            assertFalse(f.clipboard.paste()); f.view.copy(); assertTrue(f.commands.isEmpty());
            f.clipboard.remoteText("read-only copy");
            assertEquals("read-only copy", f.system.getPrimaryClip().getItemAt(0).getText());
        }
    }
    @Test public void externalShiftPastePreservesPhysicalModifiersAndTheirKeyups() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.system.setPrimaryClip(ClipData.newPlainText("local", "terminal paste"));
            int modifiers = KeyEvent.META_CTRL_ON | KeyEvent.META_SHIFT_ON;
            for (boolean mac : new boolean[]{false, true}) {
                ready(f.view, false, mac);
                int ctrl = mac ? KeyEvent.KEYCODE_CTRL_RIGHT : KeyEvent.KEYCODE_CTRL_LEFT;
                int remoteCtrl = f.view.shortcut() + (mac ? 1 : 0);
                f.view.onKeyDown(ctrl, key(KeyEvent.ACTION_DOWN, ctrl, KeyEvent.META_CTRL_ON));
                f.view.onKeyDown(KeyEvent.KEYCODE_SHIFT_LEFT, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_SHIFT_LEFT, modifiers));
                f.commands.clear();
                f.view.onKeyDown(KeyEvent.KEYCODE_V, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_V, modifiers));
                f.view.onKeyUp(KeyEvent.KEYCODE_V, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_V, modifiers));
                assertEquals(List.of("c,terminal paste", "k,118,1", "k,118,0"), f.commands);
                f.view.onKeyUp(KeyEvent.KEYCODE_SHIFT_LEFT, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_SHIFT_LEFT, KeyEvent.META_CTRL_ON));
                f.view.onKeyUp(ctrl, key(KeyEvent.ACTION_UP, ctrl, 0));
                assertEquals(List.of("c,terminal paste", "k,118,1", "k,118,0", "k,65505,0", "k," + remoteCtrl + ",0"), f.commands);
            }
        }
    }
    @Test public void asynchronousImagePasteRetainsOriginalShiftAfterKeysAreReleased() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.clipboard.remoteImage(packet(png(0xff123456))); f.io.drain();
            int modifiers = KeyEvent.META_CTRL_ON | KeyEvent.META_SHIFT_ON;
            f.view.onKeyDown(KeyEvent.KEYCODE_CTRL_LEFT, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_CTRL_LEFT, KeyEvent.META_CTRL_ON));
            f.view.onKeyDown(KeyEvent.KEYCODE_SHIFT_LEFT, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_SHIFT_LEFT, modifiers));
            f.view.onKeyDown(KeyEvent.KEYCODE_V, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_V, modifiers));
            f.view.onKeyUp(KeyEvent.KEYCODE_V, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_V, modifiers));
            f.view.onKeyUp(KeyEvent.KEYCODE_SHIFT_LEFT, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_SHIFT_LEFT, KeyEvent.META_CTRL_ON));
            f.view.onKeyUp(KeyEvent.KEYCODE_CTRL_LEFT, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_CTRL_LEFT, 0));
            assertTrue(f.images.isEmpty()); f.commands.clear(); f.io.drain();
            assertEquals(List.of("image", "k,65507,1", "k,65505,1", "k,118,1", "k,118,0", "k,65507,0", "k,65505,0"), f.commands);
            assertEquals(1, f.images.size());
        }
    }
    @Test public void stickyShiftSurvivesExternalPasteAndToolbarPasteRestoresHeldKeys() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.system.setPrimaryClip(ClipData.newPlainText("local", "sticky paste"));
            f.view.modifier(0xffe1); f.commands.clear();
            f.view.onKeyDown(KeyEvent.KEYCODE_V, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_V, KeyEvent.META_CTRL_ON));
            assertEquals(List.of("c,sticky paste", "k,65507,1", "k,118,1", "k,118,0", "k,65507,0"), f.commands);
            assertTrue(f.view.held(0xffe1));
            f.commands.clear(); assertTrue(f.clipboard.paste());
            assertEquals(List.of("c,sticky paste", "k,65505,0", "k,65507,1", "k,118,1", "k,118,0", "k,65507,0", "k,65505,1"), f.commands);
            assertTrue(f.view.held(0xffe1));
        }
    }
    @Test public void maximumAcceptedTextLeavesBinderHeadroomAndOversizeReportsOnce() throws Exception {
        String maximum = "x".repeat(NativeDesktopClipboard.MAX_TEXT);
        assertTrue(NativeDesktopClipboard.validText(maximum));
        assertFalse(NativeDesktopClipboard.validText("x".repeat(600 * 1024)));
        Parcel parcel = Parcel.obtain();
        try {
            ClipData.newPlainText("Remote desktop", maximum).writeToParcel(parcel, 0);
            assertTrue("Clipboard parcel must leave Binder headroom: " + parcel.dataSize(), parcel.dataSize() < 768 * 1024);
        } finally { parcel.recycle(); }
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.system.setPrimaryClip(ClipData.newPlainText("local", "sentinel"));
            f.clipboard.remoteText("x".repeat(600 * 1024));
            assertEquals(List.of("clipboard"), f.errors);
            f.clipboard.focus(false); f.clipboard.focus(true); f.clipboard.focus(false); f.clipboard.focus(true);
            assertEquals(List.of("clipboard"), f.errors);
            assertEquals("sentinel", f.system.getPrimaryClip().getItemAt(0).getText());
            f.clipboard.remoteText(maximum);
            assertEquals(maximum, f.system.getPrimaryClip().getItemAt(0).getText());
        }
    }
    @Implements(ClipboardManager.class)
    public static class RejectingClipboard extends ShadowClipboardManager {
        boolean reject;
        int writes;
        @Implementation @Override protected void setPrimaryClip(ClipData clip) {
            writes++;
            if (reject) throw new RuntimeException(new android.os.TransactionTooLargeException());
            super.setPrimaryClip(clip);
        }
    }
    @Test @Config(shadows = RejectingClipboard.class)
    public void failedPublicationReportsErrorAndWaitsForExplicitRetry() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            RejectingClipboard shadow = Shadow.extract(f.system);
            shadow.reject = true; f.clipboard.remoteText("remote");
            assertEquals(1, shadow.writes); assertEquals(List.of("clipboard"), f.errors);
            f.clipboard.focus(false); f.clipboard.focus(true);
            assertEquals(1, shadow.writes); assertEquals(List.of("clipboard"), f.errors);
            shadow.reject = false; f.view.copy();
            assertEquals(2, shadow.writes); assertEquals("remote", f.system.getPrimaryClip().getItemAt(0).getText());
        }
    }
    @Test public void repeatedCopyRestoresRemoteTextWithoutAnotherVoxlMessage() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.clipboard.remoteText("remote A");
            f.clipboard.focus(false); f.system.setPrimaryClip(ClipData.newPlainText("Other app", "local B"));
            f.clipboard.focus(true);
            assertEquals("local B", f.system.getPrimaryClip().getItemAt(0).getText());
            f.view.copy();
            assertEquals("remote A", f.system.getPrimaryClip().getItemAt(0).getText());
            f.clipboard.remoteText("new remote");
            assertEquals("new remote", f.system.getPrimaryClip().getItemAt(0).getText());
            f.system.setPrimaryClip(ClipData.newPlainText("local", "sent to desktop")); f.clipboard.paste();
            f.system.setPrimaryClip(ClipData.newPlainText("Other app", "replacement")); f.view.copy();
            assertEquals("sent to desktop", f.system.getPrimaryClip().getItemAt(0).getText());
        }
    }
    @Test public void repeatedCopyRestoresRemoteImageAndNewerTextSupersedesIt() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.clipboard.remoteImage(packet(png(0xffabcdef))); f.io.drain();
            f.clipboard.focus(false); f.system.setPrimaryClip(ClipData.newPlainText("Other app", "replacement"));
            f.clipboard.focus(true); f.view.copy(); f.io.drain();
            try (var input = f.activity.getContentResolver().openInputStream(f.system.getPrimaryClip().getItemAt(0).getUri())) {
                Bitmap bitmap = BitmapFactory.decodeStream(input);
                assertEquals(0xffabcdef, bitmap.getPixel(1, 1)); bitmap.recycle();
            }
            f.view.copy(); // A second image publication is in flight when newer text arrives.
            f.clipboard.remoteText("new remote text"); f.io.drain();
            assertEquals("new remote text", f.system.getPrimaryClip().getItemAt(0).getText());
            f.system.setPrimaryClip(ClipData.newPlainText("Other app", "replacement")); f.view.copy();
            assertEquals("new remote text", f.system.getPrimaryClip().getItemAt(0).getText());
            f.clipboard.close(); f.system.setPrimaryClip(ClipData.newPlainText("Other app", "after close")); f.view.copy();
            assertEquals("after close", f.system.getPrimaryClip().getItemAt(0).getText());
        }
    }
    @Test public void imageClipboardUsesNativeContentUriAndOrderedPngPaste() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.clipboard.remoteImage(packet(png(0xffff0000))); f.io.drain();
            ClipData clip = f.system.getPrimaryClip();
            assertTrue(clip.getDescription().hasMimeType("image/png"));
            assertEquals("content", clip.getItemAt(0).getUri().getScheme());
            assertEquals(f.activity.getPackageName() + ".desktop-clipboard", clip.getItemAt(0).getUri().getAuthority());
            assertTrue(f.commands.isEmpty());
            assertTrue(f.clipboard.paste()); assertTrue(f.images.isEmpty()); f.io.drain();
            assertEquals(1, f.images.size());
            Bitmap bitmap = BitmapFactory.decodeByteArray(f.images.get(0), 0, f.images.get(0).length);
            assertEquals(2, bitmap.getWidth()); assertEquals(0xffff0000, bitmap.getPixel(1, 1)); bitmap.recycle();
            assertEquals("image", f.commands.get(0)); assertTrue(f.commands.indexOf("image") < f.commands.indexOf("k,118,1"));
            f.commands.clear();
            EditorInfo editor = new EditorInfo(); InputConnection input = f.view.onCreateInputConnection(editor);
            assertArrayEquals(new String[]{"image/*"}, editor.contentMimeTypes);
            assertTrue(input.commitContent(new InputContentInfo(clip.getItemAt(0).getUri(), clip.getDescription(), null), 0, null));
            f.clipboard.close(); f.io.drain(); assertTrue(f.commands.isEmpty());
        }
    }
    @Test public void staleAndMalformedImagesCannotReplaceNewerTextOrOutliveViewer() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.clipboard.remoteImage(packet(png(0xff00ff00)));
            f.clipboard.remoteText("newer"); f.io.drain();
            assertEquals("newer", f.system.getPrimaryClip().getItemAt(0).getText());
            f.clipboard.remoteImage(new byte[]{4, 1, 2, 3}); f.io.drain();
            assertEquals("newer", f.system.getPrimaryClip().getItemAt(0).getText()); assertEquals(1, f.errors.size());
            f.clipboard.remoteImage(packet(png(0xff0000ff))); f.clipboard.close(); f.io.drain();
            assertEquals("newer", f.system.getPrimaryClip().getItemAt(0).getText());
            java.io.File directory = new java.io.File(f.activity.getCacheDir(), "desktop-clipboard");
            assertEquals(0, directory.listFiles(file -> file.getName().startsWith("pending-")).length);
        }
    }
    @Test public void clipboardSizeBoundsAndNonContentUrisAreRejected() throws Exception {
        assertTrue(NativeDesktopClipboard.validText("x".repeat(NativeDesktopClipboard.MAX_TEXT)));
        assertFalse(NativeDesktopClipboard.validText("中".repeat(NativeDesktopClipboard.MAX_TEXT / 3 + 1)));
        assertFalse(NativeDesktopClipboard.validText("nul\0text"));
        assertThrows(IllegalArgumentException.class, () -> NativeDesktopClipboard.png(new byte[NativeDesktopClipboard.MAX_IMAGE + 1]));
        try (var controller = Robolectric.buildActivity(Activity.class).setup(); var f = new Fixture(controller.get())) {
            f.system.setPrimaryClip(ClipData.newRawUri("foreign", android.net.Uri.parse("https://example.com/image.png")));
            assertFalse(f.clipboard.paste()); assertTrue(f.io.tasks.isEmpty());
            f.clipboard.focus(false); f.system.setPrimaryClip(ClipData.newPlainText("local", "blocked"));
            assertFalse(f.clipboard.paste()); assertTrue(f.commands.isEmpty());
        }
    }
}
