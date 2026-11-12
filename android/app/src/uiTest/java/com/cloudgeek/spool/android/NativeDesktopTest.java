package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.os.Handler;
import android.os.Looper;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.view.MotionEvent;
import android.view.View;
import android.view.KeyEvent;
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
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeDesktopTest {
    private static final String TOKEN = "x".repeat(43);
    private static JSONObject json(String value) {
        try { return new JSONObject(value); } catch (Exception error) { throw new AssertionError(error); }
    }
    private static final class Queue implements Executor {
        final ArrayDeque<Runnable> tasks = new ArrayDeque<>();
        public void execute(Runnable task) { tasks.add(task); }
        void next() { tasks.remove().run(); }
        void drain() { while (!tasks.isEmpty()) next(); }
    }
    private static final class Fixture implements NativeDesktopConnection.Transport {
        final Queue io = new Queue();
        final List<String> requests = new ArrayList<>(), commands = new ArrayList<>(), errors = new ArrayList<>();
        NativeDesktopConnection.Listener listener;
        Runnable duringConnect = () -> {}, duringOpen = () -> {};
        boolean available = true;
        int opened, closed;
        final NativeDesktopConnection connection = new NativeDesktopConnection(this, io, new Handler(Looper.getMainLooper()), new NativeDesktopConnection.Events() {
            public void message(byte[] data, boolean binary) {}
            public void failed(String reason) { errors.add(reason); }
        });
        public JSONObject request(String path, boolean post) {
            requests.add((post ? "POST " : "GET ") + path);
            if (path.equals("/desktop/connect")) { duringConnect.run(); return json("{\"token\":\"" + TOKEN + "\"}"); }
            return json("{\"available\":" + available + "}");
        }
        public NativeDesktopConnection.Wire open(String token, NativeDesktopConnection.Listener listener) {
            assertEquals(TOKEN, token); opened++; this.listener = listener; duringOpen.run(); listener.opened();
            return new NativeDesktopConnection.Wire() {
                public void send(String command) { commands.add(command); }
                public void close() { closed++; }
            };
        }
    }
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    @Test public void lateConnectAndLateSocketAreDisposedAfterClose() {
        Fixture first = new Fixture();
        first.duringConnect = first.connection::close;
        first.connection.start(); first.io.drain(); idle();
        assertEquals(0, first.opened);
        assertTrue(first.requests.contains("POST /desktop/disconnect?token=" + TOKEN));
        Fixture second = new Fixture(); second.duringOpen = second.connection::close;
        second.connection.start(); second.io.drain(); idle();
        assertEquals(1, second.opened); assertEquals(1, second.closed);
        assertEquals(1, second.requests.stream().filter(s -> s.startsWith("POST /desktop/disconnect")).count());
    }
    @Test public void revocationClosesWireAndViewWithoutReconnectingOrSendingInput() {
        Fixture f = new Fixture();
        f.connection.start(); f.io.next(); idle();
        f.available = false; f.io.next(); idle(); f.io.drain();
        assertEquals(1, f.closed); assertEquals(1, f.opened); assertEquals(1, f.errors.size());
        f.connection.send("k,65,1"); f.connection.start(); idle(); f.io.drain();
        assertTrue(f.commands.isEmpty()); assertEquals(1, f.opened);
        assertTrue(f.requests.contains("POST /desktop/disconnect?token=" + TOKEN));
    }
    @Test public void wireClosureDoesNotAutomaticallyCreateAnotherView() {
        Fixture f = new Fixture();
        f.connection.start(); f.io.next(); idle();
        f.listener.ended(); idle(); f.io.drain(); idle();
        assertEquals(1, f.opened); assertEquals(1, f.closed); assertEquals(1, f.errors.size());
    }
    @Test public void surfaceCommandsBeforeSocketAssignmentAreNotLost() {
        Fixture f = new Fixture();
        f.duringOpen = () -> { f.connection.send("v,1"); f.connection.send("kf"); };
        f.connection.start(); f.io.next(); idle();
        assertEquals(List.of("v,1", "kf"), f.commands);
        f.connection.close(); f.io.drain();
    }
    @Test public void layoutAndVideoHeadersAreValidatedBeforeAllocatingDecoders() throws Exception {
        NativeDesktopProtocol.Layout layout = new NativeDesktopProtocol.Layout(json("{\"w\":1920,\"h\":1080,\"stripes\":[[0,540],[540,540]],\"readOnly\":true}"));
        assertTrue(layout.readOnly); assertArrayEquals(new int[]{0, 540}, layout.y);
        for (String value : new String[]{"{\"w\":0,\"h\":1080}", "{\"w\":999999,\"h\":999999}", "{\"w\":100,\"h\":100,\"stripes\":[[1,99]]}", "{\"w\":100,\"h\":100,\"stripes\":[[0,20]]}"}) {
            assertThrows(Exception.class, () -> new NativeDesktopProtocol.Layout(json(value)));
        }
        byte[] data = packet(0xffffffffL, 1, 2);
        NativeDesktopProtocol.Packet packet = new NativeDesktopProtocol.Packet(data, 2);
        assertEquals(0xffffffffL, packet.frame); assertEquals(1, packet.stripe); assertEquals(2, packet.parts);
        assertThrows(IllegalArgumentException.class, () -> new NativeDesktopProtocol.Packet(new byte[16], 1));
        data[12] = 3; assertThrows(IllegalArgumentException.class, () -> new NativeDesktopProtocol.Packet(data, 2));
    }
    private static byte[] packet(long id, int stripe, int parts) {
        byte[] bytes = new byte[17]; bytes[0] = 1; bytes[1] = 1;
        for (int i = 0; i < 4; i++) bytes[2+i] = (byte)(id >>> (24 - i*8));
        bytes[10] = (byte)stripe; bytes[12] = (byte)parts; return bytes;
    }
    @Test public void allStripesArePresentedBeforeAcknowledgingFramesInOrder() {
        List<String> events = new ArrayList<>();
        NativeDesktopProtocol.Frames frames = new NativeDesktopProtocol.Frames(id -> events.add("ack" + id));
        NativeDesktopProtocol.Packet a = new NativeDesktopProtocol.Packet(packet(10, 0, 2), 2);
        NativeDesktopProtocol.Packet b = new NativeDesktopProtocol.Packet(packet(10, 1, 2), 2);
        NativeDesktopProtocol.Packet c = new NativeDesktopProtocol.Packet(packet(11, 0, 1), 2);
        frames.expect(a); frames.expect(b); frames.expect(c);
        frames.ready(c, render -> events.add("c" + render)); frames.ready(b, render -> events.add("b" + render));
        assertTrue(events.isEmpty());
        frames.ready(a, render -> events.add("a" + render));
        assertEquals(5, events.size()); assertEquals("ack10", events.get(2)); assertEquals("ctrue", events.get(3)); assertEquals("ack11", events.get(4));
        frames.expect(a); frames.ready(a, render -> events.add("cancel" + render)); frames.clear();
        assertEquals("cancelfalse", events.get(events.size()-1));
    }
    @Test public void nativeImeCommitsChineseOnceAndReadOnlySuppressesInput() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            List<String> commands = new ArrayList<>();
            NativeDesktopView view = new NativeDesktopView(new NativeUi(controller.get(), false), commands::add, reason -> fail(reason), () -> {}, () -> {});
            try {
                var layoutField = NativeDesktopView.class.getDeclaredField("layout"); layoutField.setAccessible(true);
                var activeField = NativeDesktopView.class.getDeclaredField("active"); activeField.setAccessible(true);
                layoutField.set(view, new NativeDesktopProtocol.Layout(json("{\"w\":100,\"h\":100}"))); activeField.setBoolean(view, true);
                InputConnection input = view.onCreateInputConnection(new EditorInfo());
                input.setComposingText("zhong", 1); assertTrue(commands.isEmpty());
                input.commitText("中", 1); input.finishComposingText(); idle();
                assertEquals(List.of("k,16797229,1", "k,16797229,0"), commands);
                input.deleteSurroundingText(1, 0);
                assertEquals("k,65288,1", commands.get(2));
                layoutField.set(view, new NativeDesktopProtocol.Layout(json("{\"w\":100,\"h\":100,\"readOnly\":true}")));
                int count = commands.size(); input.commitText("blocked", 1); view.tap(0xff0d); idle(); assertEquals(count, commands.size());
            } finally { view.close(); }
        }
    }

    private static void ready(NativeDesktopView view, boolean readOnly) throws Exception {
        var layoutField = NativeDesktopView.class.getDeclaredField("layout"); layoutField.setAccessible(true);
        var activeField = NativeDesktopView.class.getDeclaredField("active"); activeField.setAccessible(true);
        layoutField.set(view, new NativeDesktopProtocol.Layout(json("{\"w\":1920,\"h\":1080,\"readOnly\":" + readOnly + "}")));
        activeField.setBoolean(view, true);
        view.measure(View.MeasureSpec.makeMeasureSpec(400, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, 400, 800);
        // Gesture tests omit codec-backed surfaces, which normally size the viewport.
        view.viewport.resize(400, 800, 1920, 1080);
    }
    private static void touch(NativeDesktopView view, int action, float... points) {
        int count = points.length / 2;
        MotionEvent.PointerProperties[] properties = new MotionEvent.PointerProperties[count];
        MotionEvent.PointerCoords[] coords = new MotionEvent.PointerCoords[count];
        for (int i = 0; i < count; i++) {
            properties[i] = new MotionEvent.PointerProperties(); properties[i].id = i; properties[i].toolType = MotionEvent.TOOL_TYPE_FINGER;
            coords[i] = new MotionEvent.PointerCoords(); coords[i].x = points[i*2]; coords[i].y = points[i*2+1]; coords[i].pressure = 1; coords[i].size = 1;
        }
        MotionEvent event = MotionEvent.obtain(0, 0, action, count, properties, coords, 0, 0, 1, 1, 0, 0, android.view.InputDevice.SOURCE_TOUCHSCREEN, 0);
        view.onTouchEvent(event); event.recycle();
    }
    @Test public void touchClickDragRightClickAndCancelUseRemoteCoordinates() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            List<String> commands = new ArrayList<>();
            NativeDesktopView view = new NativeDesktopView(new NativeUi(controller.get(), false), commands::add, reason -> fail(reason), () -> {}, () -> {});
            try {
                ready(view, false);
                touch(view, MotionEvent.ACTION_DOWN, 200, 400); touch(view, MotionEvent.ACTION_UP, 200, 400);
                assertEquals(List.of("m,960,540", "b,1,1", "b,1,0"), commands); commands.clear();
                touch(view, MotionEvent.ACTION_DOWN, 200, 400); touch(view, MotionEvent.ACTION_MOVE, 260, 400); touch(view, MotionEvent.ACTION_CANCEL, 260, 400);
                assertEquals(List.of("m,960,540", "b,1,1", "m,1248,540", "b,1,0"), commands); commands.clear();
                touch(view, MotionEvent.ACTION_DOWN, 200, 400);
                Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(550));
                touch(view, MotionEvent.ACTION_UP, 200, 400);
                assertEquals(List.of("m,960,540", "b,3,1", "b,3,0"), commands); commands.clear();
                touch(view, MotionEvent.ACTION_DOWN, 200, 20); touch(view, MotionEvent.ACTION_UP, 200, 20);
                assertTrue(commands.isEmpty());
            } finally { view.close(); }
        }
    }
    @Test public void twoFingerScrollAndLocalPinchHaveSeparateModes() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            List<String> commands = new ArrayList<>();
            NativeDesktopView view = new NativeDesktopView(new NativeUi(controller.get(), false), commands::add, reason -> fail(reason), () -> {}, () -> {});
            try {
                ready(view, false);
                touch(view, MotionEvent.ACTION_DOWN, 160, 400);
                touch(view, MotionEvent.ACTION_POINTER_DOWN | (1 << MotionEvent.ACTION_POINTER_INDEX_SHIFT), 160, 400, 240, 400);
                touch(view, MotionEvent.ACTION_MOVE, 160, 380, 240, 380);
                assertEquals(List.of("sp,0.0,96.0"), commands);
                touch(view, MotionEvent.ACTION_CANCEL, 160, 380, 240, 380);
                view.panMode(true); commands.clear();
                touch(view, MotionEvent.ACTION_DOWN, 160, 400);
                touch(view, MotionEvent.ACTION_POINTER_DOWN | (1 << MotionEvent.ACTION_POINTER_INDEX_SHIFT), 160, 400, 240, 400);
                touch(view, MotionEvent.ACTION_MOVE, 120, 400, 280, 400);
                assertEquals(2, view.viewport.zoom, .001); assertTrue(commands.isEmpty());
                touch(view, MotionEvent.ACTION_CANCEL, 120, 400, 280, 400);
                view.fit(); assertEquals(1, view.viewport.zoom, .001);
            } finally { view.close(); }
        }
    }
    @Test public void externalKeysAndHeldModifiersReleaseWithoutSticking() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            List<String> commands = new ArrayList<>();
            NativeDesktopView view = new NativeDesktopView(new NativeUi(controller.get(), false), commands::add, reason -> fail(reason), () -> {}, () -> {});
            try {
                ready(view, false);
                view.onKeyDown(KeyEvent.KEYCODE_DPAD_LEFT, new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_LEFT));
                view.onKeyUp(KeyEvent.KEYCODE_DPAD_LEFT, new KeyEvent(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DPAD_LEFT));
                view.modifier(0xffe3); assertTrue(view.held(0xffe3)); view.releaseAll(); assertFalse(view.held(0xffe3));
                assertEquals(List.of("k,65361,1", "k,65361,0", "k,65507,1", "r"), commands);
            } finally { view.close(); }
        }
    }
    @Test public void dialogDisconnectClosesViewAndCancelsPendingConnection() {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            Fixture f = new Fixture(); int[] dismissed = {0};
            NativeDesktopDialog dialog = new NativeDesktopDialog(new NativeUi(controller.get(), false, "zh-CN"), f, f.io, () -> dismissed[0]++);
            dialog.show(); dialog.dismiss(); idle(); f.io.drain();
            assertEquals(1, dismissed[0]); assertFalse(dialog.isShowing()); assertTrue(f.requests.isEmpty()); assertEquals(0, f.opened);
        }
    }
    @Test public void dialogRoutesClipboardMessagesAndToolbarPasteThroughTheLiveView() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            Fixture f = new Fixture();
            NativeDesktopDialog dialog = new NativeDesktopDialog(new NativeUi(controller.get(), false, "zh-CN"), f, f.io, () -> {});
            try {
                dialog.show(); f.io.next(); idle();
                View root = dialog.getWindow().getDecorView();
                View copy = labelled(root, "从远程桌面复制"), paste = labelled(root, "粘贴到远程桌面");
                assertNotNull(copy); assertNotNull(paste); assertFalse(paste.isEnabled());
                ready(dialog.desktop, false); dialog.desktop.panMode(false); dialog.clipboard.focus(true);
                assertTrue(copy.isEnabled()); assertTrue(paste.isEnabled());
                String remote = "中文\n" + "remote".repeat(15000); // Larger than the old video-only JSON limit.
                f.listener.message(new JSONObject().put("t", "clip").put("d", remote).toString().getBytes(java.nio.charset.StandardCharsets.UTF_8), false);
                idle();
                var system = (android.content.ClipboardManager) controller.get().getSystemService(android.content.Context.CLIPBOARD_SERVICE);
                assertEquals(remote, system.getPrimaryClip().getItemAt(0).getText());
                system.setPrimaryClip(android.content.ClipData.newPlainText("local", "local\n😀"));
                assertTrue(paste.performClick());
                assertTrue(f.commands.indexOf("c,local\n😀") < f.commands.indexOf("k,118,1"));
                ready(dialog.desktop, true); dialog.desktop.panMode(false);
                assertFalse(copy.isEnabled()); assertFalse(paste.isEnabled());
                int count = f.commands.size(); paste.performClick(); assertEquals(count, f.commands.size());
            } finally { dialog.dismiss(); idle(); f.io.drain(); }
        }
    }
    @Test public void oversizedClipboardReportsErrorWithoutEndingTheDesktopConnection() throws Exception {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            Fixture f = new Fixture();
            NativeDesktopDialog dialog = new NativeDesktopDialog(new NativeUi(controller.get(), false, "zh-CN"), f, f.io, () -> {});
            try {
                dialog.show(); f.io.next(); idle();
                ready(dialog.desktop, false); dialog.clipboard.focus(true);
                var system = (android.content.ClipboardManager) controller.get().getSystemService(android.content.Context.CLIPBOARD_SERVICE);
                system.setPrimaryClip(android.content.ClipData.newPlainText("local", "sentinel"));
                // JSON escaping makes this larger than six times the new local
                // limit; it must still reach the clipboard size validation.
                String oversized = "\u0001".repeat(300 * 1024);
                byte[] packet = new JSONObject().put("t", "clip").put("d", oversized).toString().getBytes(java.nio.charset.StandardCharsets.UTF_8);
                assertTrue(packet.length > 6 * NativeDesktopClipboard.MAX_TEXT + 128);
                f.listener.message(packet, false); idle();
                assertTrue(dialog.desktop.controllable()); assertEquals(0, f.closed);
                assertEquals("sentinel", system.getPrimaryClip().getItemAt(0).getText());
                assertEquals(dialog.label(R.string.desktop_clipboard_error), org.robolectric.shadows.ShadowToast.getTextOfLatestToast());
                f.listener.message(new JSONObject().put("t", "clip").put("d", "valid next copy").toString().getBytes(java.nio.charset.StandardCharsets.UTF_8), false);
                idle(); assertEquals("valid next copy", system.getPrimaryClip().getItemAt(0).getText());
            } finally { dialog.dismiss(); idle(); f.io.drain(); }
        }
    }
    private static View labelled(View view, String label) {
        if (label.equals(view.getContentDescription())) return view;
        if (view instanceof android.view.ViewGroup group) {
            for (int i = 0; i < group.getChildCount(); i++) {
                View found = labelled(group.getChildAt(i), label);
                if (found != null) return found;
            }
        }
        return null;
    }
    @Test public void iconToolbarTogglesFullscreenWithoutDisconnecting() {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            Fixture f = new Fixture();
            NativeDesktopDialog dialog = new NativeDesktopDialog(new NativeUi(controller.get(), false, "zh-CN"), f, f.io, () -> {});
            try {
                dialog.show(); f.io.next(); idle();
                View root = dialog.getWindow().getDecorView();
                View enter = labelled(root, "进入全屏"), disconnect = labelled(root, "断开桌面");
                assertTrue(enter instanceof NativeUi.IconButton); assertTrue(disconnect instanceof NativeUi.IconButton);
                assertEquals("进入全屏", enter.getTooltipText().toString());
                assertTrue(enter.performClick());
                assertTrue(dialog.fullscreen()); assertTrue(dialog.isShowing());
                assertNotNull(labelled(root, "退出全屏")); assertTrue(enter.isSelected());
                if (android.os.Build.VERSION.SDK_INT < 30)
                    assertNotEquals(0, dialog.getWindow().getAttributes().flags & android.view.WindowManager.LayoutParams.FLAG_FULLSCREEN);
                root.measure(View.MeasureSpec.makeMeasureSpec(400, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
                root.layout(0, 0, 400, 800);
                View collapse = labelled(root, "收起工具栏"), dock = (View) collapse.getParent();
                int expandedWidth = dock.getWidth(), desktopHeight = dialog.desktop.getHeight();
                dialog.desktop.zoom(2);
                assertTrue(collapse.performClick());
                root.measure(View.MeasureSpec.makeMeasureSpec(400, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
                root.layout(0, 0, 400, 800);
                assertTrue(dialog.toolbarCollapsed()); assertTrue(dialog.fullscreen()); assertFalse(enter.isShown());
                assertTrue(dock.getWidth() < expandedWidth / 2);
                assertEquals(desktopHeight, dialog.desktop.getHeight()); assertEquals(2, dialog.desktop.viewport.zoom, .001);
                assertEquals("展开工具栏", collapse.getTooltipText().toString());
                int[] dockPosition = new int[2], rootPosition = new int[2];
                dock.getLocationOnScreen(dockPosition); root.getLocationOnScreen(rootPosition);
                boolean[] reachedDesktop = {false};
                dialog.desktop.setOnTouchListener((view, event) -> { reachedDesktop[0] = true; return true; });
                MotionEvent event = MotionEvent.obtain(0, 0, MotionEvent.ACTION_DOWN, 24, dockPosition[1] - rootPosition[1] + dock.getHeight() / 2f, 0);
                root.dispatchTouchEvent(event); event.recycle();
                assertTrue("Collapsing must free the old toolbar hit area", reachedDesktop[0]);
                event = MotionEvent.obtain(0, 0, MotionEvent.ACTION_CANCEL, 24, dockPosition[1] - rootPosition[1] + dock.getHeight() / 2f, 0);
                root.dispatchTouchEvent(event); event.recycle();
                assertTrue(collapse.performClick());
                root.measure(View.MeasureSpec.makeMeasureSpec(400, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
                root.layout(0, 0, 400, 800);
                assertFalse(dialog.toolbarCollapsed()); assertTrue(enter.isShown()); assertEquals(expandedWidth, dock.getWidth());
                assertTrue(enter.performClick());
                assertFalse(dialog.fullscreen()); assertTrue(dialog.isShowing()); assertFalse(enter.isSelected());
                if (android.os.Build.VERSION.SDK_INT < 30)
                    assertEquals(0, dialog.getWindow().getAttributes().flags & android.view.WindowManager.LayoutParams.FLAG_FULLSCREEN);
                assertEquals(1, f.opened); assertEquals(0, f.closed);
                assertNotNull(labelled(root, "进入全屏"));
                assertTrue(disconnect.performClick()); idle(); f.io.drain();
                assertFalse(dialog.isShowing()); assertEquals(1, f.closed);
            } finally { dialog.dismiss(); f.io.drain(); }
        }
    }
}
