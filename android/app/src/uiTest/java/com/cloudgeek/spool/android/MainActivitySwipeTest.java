package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import android.widget.FrameLayout;
import java.time.Duration;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** Touch dispatch through the production workspace and both full-screen sidebars. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivitySwipeTest {
    static void layout(View view) {
        view.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, 360, 800);
    }

    static void touch(View view, long down, int elapsed, int action, float x, float y) {
        MotionEvent event = MotionEvent.obtain(down, down + elapsed, action, x, y, 0);
        try { view.dispatchTouchEvent(event); } finally { event.recycle(); }
    }

    static void swipe(View view, float x, float y, float dx, float dy) {
        long down = SystemClock.uptimeMillis();
        touch(view, down, 0, MotionEvent.ACTION_DOWN, x, y);
        for (int step = 1; step <= 4; step++)
            touch(view, down, step * 30, MotionEvent.ACTION_MOVE, x + dx * step / 4, y + dy * step / 4);
        touch(view, down, 150, MotionEvent.ACTION_UP, x + dx, y + dy);
    }

    private void settle(View root) {
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(250));
        layout(root);
    }

    @Test public void chatSwipesOpenBothSidesAndOutwardSwipesCloseWithoutRebuildingTheChat() throws Exception {
        for (boolean recycler : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, recycler)) {
            FrameLayout root = h.get("root"); layout(root);
            NativeUi.Composer editor = h.composer(); editor.input.setText("Keep my draft");
            Object transcript = recycler ? h.get("markwonHistory") : h.get("history");

            swipe(root, 180, 300, 140, 4); settle(root);
            assertEquals("sidebar", h.<String>get("panel"));
            assertNotNull(h.get("sidebar"));
            NativeDrawer left = h.get("drawer"); assertSame(root, left.getParent());
            swipe(root, 180, 300, 140, 0);
            assertSame("Only an outward swipe closes the left sidebar", left, h.get("drawer"));
            swipe(root, 180, 300, -140, 4); settle(root);
            assertEquals("", h.<String>get("panel")); assertNull(h.get("drawer")); assertNull(h.get("sidebar"));
            assertNull(left.getParent());

            swipe(root, 180, 300, -140, 4); settle(root);
            assertEquals("files", h.<String>get("panel")); assertNotNull(h.get("fileBrowser"));
            NativeDrawer right = h.get("drawer");
            swipe(root, 180, 300, -140, 0);
            assertSame("Only an outward swipe closes the right sidebar", right, h.get("drawer"));
            swipe(root, 180, 300, 140, 4); settle(root);
            assertEquals("", h.<String>get("panel")); assertNull(h.get("drawer")); assertNull(h.get("fileBrowser"));
            assertNull(right.getParent());
            assertSame(editor, h.composer()); assertEquals("Keep my draft", editor.input.getText().toString());
            assertSame(transcript, recycler ? h.get("markwonHistory") : h.get("history"));
            assertEquals("chat-one", h.<String>get("chatID"));
        }
    }

    @Test public void loginDoesNotOfferChatSwipeNavigation() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            h.set("user", null); h.call("render");
            FrameLayout root = h.get("root"); layout(root);
            swipe(root, 180, 300, 140, 0); swipe(root, 180, 300, -140, 0);
            assertNull(h.get("drawer")); assertEquals("", h.<String>get("panel"));
        }
    }
}
