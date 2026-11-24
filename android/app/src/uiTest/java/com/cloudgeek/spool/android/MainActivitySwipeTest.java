package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import android.widget.FrameLayout;
import java.time.Duration;
import org.json.JSONArray;
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

    @Test public void bothSidebarsFollowTheFingerBeforeReleaseAndSettleFromTheirDraggedPosition() throws Exception {
        for (boolean recycler : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, recycler)) {
            FrameLayout root = h.get("root"); layout(root);
            NativeUi.Composer editor = h.composer(); editor.input.setText("Keep my draft");
            Object transcript = recycler ? h.get("markwonHistory") : h.get("history");
            for (int direction : new int[] {1, -1}) {
                long down = SystemClock.uptimeMillis();
                touch(root, down, 0, MotionEvent.ACTION_DOWN, 180, 300);
                touch(root, down, 100, MotionEvent.ACTION_MOVE, 180 + direction * 80, 300);
                NativeDrawer drawer = h.get("drawer");
                assertNotNull("The sidebar must appear while the finger is still down", drawer);
                layout(root);
                assertEquals(-direction * (drawer.sheet.getWidth() - 80), drawer.sheet.getTranslationX(), .01f);
                touch(root, down, 200, MotionEvent.ACTION_MOVE, 180 + direction * 120, 300);
                assertSame(drawer, h.get("drawer"));
                assertEquals(-direction * (drawer.sheet.getWidth() - 120), drawer.sheet.getTranslationX(), .01f);
                touch(root, down, 300, MotionEvent.ACTION_UP, 180 + direction * 120, 300);
                assertEquals("Release must not restart the entrance animation", -direction * (drawer.sheet.getWidth() - 120), drawer.sheet.getTranslationX(), .01f);
                settle(root); assertEquals(0, drawer.sheet.getTranslationX(), .01f);

                down = SystemClock.uptimeMillis();
                touch(root, down, 0, MotionEvent.ACTION_DOWN, 180, 300);
                touch(root, down, 100, MotionEvent.ACTION_MOVE, 180 - direction * 80, 300);
                assertSame("Closing keeps the drawer mounted until release", drawer, h.get("drawer"));
                assertEquals(-direction * 80, drawer.sheet.getTranslationX(), .01f);
                touch(root, down, 200, MotionEvent.ACTION_UP, 180 - direction * 80, 300);
                settle(root);
                assertNull(h.get("drawer")); assertNull(drawer.getParent());
            }
            assertSame(editor, h.composer()); assertEquals("Keep my draft", editor.input.getText().toString());
            assertSame(transcript, recycler ? h.get("markwonHistory") : h.get("history"));
        }
    }

    @Test public void shortReversedAndCancelledDragsRestoreBothSidebarsAndAllowTheNextGesture() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            FrameLayout root = h.get("root"); layout(root);
            for (int direction : new int[] {1, -1}) {
                for (int action : new int[] {MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL}) {
                    long down = SystemClock.uptimeMillis();
                    touch(root, down, 0, MotionEvent.ACTION_DOWN, 180, 300);
                    touch(root, down, 100, MotionEvent.ACTION_MOVE, 180 + direction * 100, 300);
                    NativeDrawer preview = h.get("drawer"); assertNotNull(preview); layout(root);
                    touch(root, down, 300, MotionEvent.ACTION_MOVE, 180 + direction * 30, 300);
                    assertEquals(-direction * (preview.sheet.getWidth() - 30), preview.sheet.getTranslationX(), .01f);
                    touch(root, down, 500, action, 180 + direction * 30, 300);
                    settle(root); assertEquals("", h.<String>get("panel")); assertNull(h.get("drawer"));
                    assertNull(preview.getParent());
                }
                swipe(root, 180, 300, direction * 140, 0); settle(root);
                NativeDrawer drawer = h.get("drawer"); assertNotNull(drawer);
                for (int action : new int[] {MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL}) {
                    long down = SystemClock.uptimeMillis();
                    touch(root, down, 0, MotionEvent.ACTION_DOWN, 180, 300);
                    touch(root, down, 100, MotionEvent.ACTION_MOVE, 180 - direction * 30, 300);
                    assertEquals(-direction * 30, drawer.sheet.getTranslationX(), .01f);
                    touch(root, down, 300, action, 180 - direction * 30, 300);
                    settle(root); assertSame(drawer, h.get("drawer"));
                    assertEquals(0, drawer.sheet.getTranslationX(), .01f);
                }
                swipe(root, 180, 300, -direction * 140, 0); settle(root); assertNull(h.get("drawer"));
            }
        }
    }

    @Test public void aReleaseBeforeTheFirstLayoutKeepsThePartialEntrancePosition() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            FrameLayout root = h.get("root"); layout(root);
            for (int direction : new int[] {1, -1}) {
                swipe(root, 180, 300, direction * 120, 0);
                NativeDrawer drawer = h.get("drawer"); assertNotNull(drawer);
                layout(root);
                assertEquals(-direction * (drawer.sheet.getWidth() - 120), drawer.sheet.getTranslationX(), .01f);
                settle(root); assertEquals(0, drawer.sheet.getTranslationX(), .01f);
                swipe(root, 180, 300, -direction * 140, 0); settle(root);
            }
        }
    }

    @Test public void leftEntranceUsesNavigationAlreadyBuiltAndLaidOutBeforeTheGesture() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("chats", new JSONArray().put(h.chat("chat-one").put("project_id", "project-one").put("title", "Existing chat")));
            FrameLayout root = h.get("root"); layout(root); settle(root);
            for (int entrance = 0; entrance < 2; entrance++) {
                NativeSidebar prepared = h.get("preparedSidebar");
                assertNotNull(prepared); assertNull(prepared.getParent());
                assertTrue(prepared.getWidth() > 0); assertFalse(prepared.chatRows.isEmpty());
                NativeChatLink row = prepared.chatRows.get(0);
                long textSets = NativeTextView.textSets;
                long down = SystemClock.uptimeMillis();
                touch(root, down, 0, MotionEvent.ACTION_DOWN, 100, 300);
                touch(root, down, 100, MotionEvent.ACTION_MOVE, 140, 300);
                assertSame(prepared, h.get("sidebar")); assertNull(h.get("preparedSidebar"));
                assertSame("The first drag must not recreate navigation rows", row, prepared.chatRows.get(0));
                assertEquals("The first drag must not typeset the navigation again", textSets, NativeTextView.textSets);
                layout(root);
                NativeDrawer drawer = h.get("drawer");
                assertEquals(-drawer.sheet.getWidth() + 40, drawer.sheet.getTranslationX(), .01f);
                touch(root, down, 300, MotionEvent.ACTION_UP, 140, 300);
                settle(root); assertNull(h.get("drawer")); assertNull(drawer.getParent());
            }
        }
    }

    @Test public void preparedNavigationRefreshesWhileClosedAndIsReplacedWhenTheWorkspaceRenders() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("chats", new JSONArray().put(h.chat("chat-one").put("project_id", "project-one").put("title", "Existing chat")));
            FrameLayout root = h.get("root"); layout(root); settle(root);
            NativeSidebar prepared = h.get("preparedSidebar"); assertNotNull(prepared);
            JSONArray chats = h.get("chats");
            chats.getJSONObject(0).put("title", "Updated before opening");
            h.call("renderNavigation"); settle(root);
            assertSame(prepared, h.get("preparedSidebar"));
            assertEquals("Updated before opening", prepared.chatRows.get(0).title.getText().toString());
            h.set("theme", "dark"); h.call("render");
            assertNull(h.get("preparedSidebar"));
            FrameLayout replacement = h.get("root"); layout(replacement); settle(replacement);
            assertNotNull(h.get("preparedSidebar")); assertNotSame(prepared, h.get("preparedSidebar"));
            swipe(replacement, 100, 300, 140, 0); settle(replacement);
            NativeSidebar opened = h.get("sidebar");
            assertEquals("Updated before opening", opened.chatRows.get(0).title.getText().toString());
        }
    }
}
