package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import static com.cloudgeek.spool.android.MainActivitySwipeTest.*;

import android.app.Activity;
import android.os.Looper;
import android.os.SystemClock;
import android.view.InputDevice;
import android.view.MotionEvent;
import android.view.View;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.ScrollView;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeSwipeLayoutTest {
    private static final class Fixture implements AutoCloseable {
        final ActivityController<Activity> controller;
        final NativeSwipeLayout page;
        final List<Integer> swipes = new ArrayList<>();
        final AtomicInteger clicks = new AtomicInteger(), cancels = new AtomicInteger();
        final View content;
        Fixture() {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
            controller = Robolectric.buildActivity(Activity.class).setup();
            page = new NativeSwipeLayout(new NativeUi(controller.get(), false));
            page.swipes(direction -> true, (direction, commit) -> { if (commit) swipes.add(direction); });
            content = new View(controller.get()) {
                @Override public boolean onTouchEvent(MotionEvent event) {
                    if (event.getActionMasked() == MotionEvent.ACTION_CANCEL) cancels.incrementAndGet();
                    return super.onTouchEvent(event);
                }
            };
            content.setOnClickListener(view -> clicks.incrementAndGet());
            controller.get().setContentView(page); child(content);
        }
        void child(View view) {
            page.removeAllViews(); page.addView(view, new FrameLayout.LayoutParams(-1, -1)); layout(page);
        }
        @Override public void close() { controller.pause().stop().destroy(); }
    }

    @Test public void swipesCommitOnReleaseAndCancelChildClicksWhileTapsAndShortDragsDoNotNavigate() {
        try (Fixture f = new Fixture()) {
            long down = SystemClock.uptimeMillis();
            touch(f.page, down, 0, MotionEvent.ACTION_DOWN, 100, 300);
            touch(f.page, down, 30, MotionEvent.ACTION_MOVE, 240, 304);
            assertTrue(f.swipes.isEmpty()); assertEquals(1, f.cancels.get());
            touch(f.page, down, 60, MotionEvent.ACTION_UP, 240, 304);
            assertEquals(List.of(1), f.swipes); assertEquals(0, f.clicks.get());
            swipe(f.page, 240, 300, -140, 0); assertEquals(List.of(1, -1), f.swipes);
            swipe(f.page, 180, 300, 30, 0); assertEquals(List.of(1, -1), f.swipes);
            touch(f.page, down, 100, MotionEvent.ACTION_DOWN, 180, 300);
            touch(f.page, down, 120, MotionEvent.ACTION_UP, 180, 300);
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, f.clicks.get()); assertEquals(List.of(1, -1), f.swipes);
        }
    }

    @Test public void verticalScrollAndDiagonalGesturesDoNotBecomeNavigation() {
        try (Fixture f = new Fixture()) {
            ScrollView scroll = new ScrollView(f.controller.get());
            View content = new View(f.controller.get()); content.setClickable(true); content.setMinimumHeight(2000);
            scroll.addView(content, new ScrollView.LayoutParams(-1, 2000)); f.child(scroll);
            swipe(f.page, 180, 500, 8, -240);
            assertTrue("The child must still scroll vertically", scroll.getScrollY() > 0);
            assertTrue(f.swipes.isEmpty());
            f.child(f.content);
            swipe(f.page, 180, 300, 100, 120); assertTrue(f.swipes.isEmpty());
            long down = SystemClock.uptimeMillis();
            touch(f.page, down, 0, MotionEvent.ACTION_DOWN, 100, 300);
            touch(f.page, down, 30, MotionEvent.ACTION_MOVE, 104, 340);
            touch(f.page, down, 60, MotionEvent.ACTION_MOVE, 240, 340);
            touch(f.page, down, 90, MotionEvent.ACTION_UP, 240, 340);
            assertTrue("A vertical start keeps ownership even after horizontal movement", f.swipes.isEmpty());
        }
    }

    @Test public void horizontalContentKeepsItsGesturesAtBothEdgesAndInTheMiddle() {
        try (Fixture f = new Fixture()) {
            HorizontalScrollView scroll = new HorizontalScrollView(f.controller.get());
            View content = new View(f.controller.get()); content.setClickable(true); content.setMinimumWidth(1200);
            scroll.addView(content, new HorizontalScrollView.LayoutParams(1200, -1)); f.child(scroll);
            for (int offset : new int[] {0, 400, 840}) for (int direction : new int[] {-1, 1}) {
                scroll.scrollTo(offset, 0); swipe(f.page, 180, 300, direction * 140, 0);
                assertTrue(f.swipes.isEmpty());
            }
            scroll.scrollTo(400, 0); swipe(f.page, 180, 300, -140, 0);
            assertTrue("The child must still scroll horizontally", scroll.getScrollX() > 400);
        }
    }

    @Test public void textEditingAndChildRequestedGesturesDoNotNavigate() {
        try (Fixture f = new Fixture()) {
            EditText editor = new EditText(f.controller.get()); editor.setText("Editable draft");
            f.child(editor);
            swipe(f.page, 180, 300, 140, 0); swipe(f.page, 180, 300, -140, 0);
            assertEquals("Editable draft", editor.getText().toString()); assertTrue(f.swipes.isEmpty());
            View exclusive = new View(f.controller.get()) {
                @Override public boolean onTouchEvent(MotionEvent event) {
                    if (event.getActionMasked() == MotionEvent.ACTION_DOWN) getParent().requestDisallowInterceptTouchEvent(true);
                    return true;
                }
            };
            f.child(exclusive); swipe(f.page, 180, 300, 140, 0); assertTrue(f.swipes.isEmpty());
        }
    }

    @Test public void cancelledMultitouchAndReversedGesturesCannotCommitAndTheNextSwipeStillWorks() {
        try (Fixture f = new Fixture()) {
            for (int interrupted : new int[] {MotionEvent.ACTION_CANCEL, MotionEvent.ACTION_POINTER_DOWN}) {
                long down = SystemClock.uptimeMillis();
                touch(f.page, down, 0, MotionEvent.ACTION_DOWN, 100, 300);
                touch(f.page, down, 30, MotionEvent.ACTION_MOVE, 240, 300);
                if (interrupted == MotionEvent.ACTION_CANCEL) touch(f.page, down, 60, interrupted, 240, 300);
                else {
                    pointers(f.page, down, 60, MotionEvent.ACTION_POINTER_DOWN);
                    pointers(f.page, down, 90, MotionEvent.ACTION_POINTER_UP);
                    touch(f.page, down, 120, MotionEvent.ACTION_UP, 240, 300);
                }
                assertTrue(f.swipes.isEmpty());
            }
            long down = SystemClock.uptimeMillis();
            touch(f.page, down, 0, MotionEvent.ACTION_DOWN, 100, 300);
            touch(f.page, down, 30, MotionEvent.ACTION_MOVE, 240, 300);
            touch(f.page, down, 60, MotionEvent.ACTION_UP, 110, 300);
            assertTrue(f.swipes.isEmpty());
            swipe(f.page, 100, 300, 140, 0); assertEquals(List.of(1), f.swipes);
        }
    }

    @Test public void dragUpdatesStayInTheirInitialDirectionAndMultitouchReleasesOnlyOnce() {
        try (Fixture f = new Fixture()) {
            List<Float> positions = new ArrayList<>();
            List<Boolean> releases = new ArrayList<>();
            f.page.swipes(direction -> true, new NativeSwipeLayout.SwipeListener() {
                @Override public void drag(int direction, float distance) { positions.add(direction * distance); }
                @Override public void release(int direction, boolean commit) { releases.add(commit); }
            });
            long down = SystemClock.uptimeMillis();
            touch(f.page, down, 0, MotionEvent.ACTION_DOWN, 180, 300);
            touch(f.page, down, 100, MotionEvent.ACTION_MOVE, 260, 300);
            touch(f.page, down, 200, MotionEvent.ACTION_MOVE, 230, 300);
            touch(f.page, down, 300, MotionEvent.ACTION_MOVE, 160, 300);
            assertEquals(List.of(80f, 50f, 0f), positions); assertTrue(releases.isEmpty());
            pointers(f.page, down, 400, MotionEvent.ACTION_POINTER_DOWN);
            pointers(f.page, down, 500, MotionEvent.ACTION_POINTER_UP);
            touch(f.page, down, 600, MotionEvent.ACTION_UP, 300, 300);
            assertEquals(List.of(false), releases);
            assertEquals(List.of(80f, 50f, 0f), positions);
        }
    }

    @Test public void aFastFlingCanCommitBelowTheDistanceThresholdButASlowDragCannot() {
        try (Fixture f = new Fixture()) {
            long down = SystemClock.uptimeMillis();
            touch(f.page, down, 0, MotionEvent.ACTION_DOWN, 180, 300);
            touch(f.page, down, 10, MotionEvent.ACTION_MOVE, 200, 300);
            touch(f.page, down, 20, MotionEvent.ACTION_MOVE, 220, 300);
            touch(f.page, down, 30, MotionEvent.ACTION_UP, 230, 300);
            assertEquals(List.of(1), f.swipes);
            swipe(f.page, 180, 300, 50, 0);
            assertEquals(List.of(1), f.swipes);
        }
    }

    private void pointers(View view, long down, int elapsed, int action) {
        MotionEvent.PointerProperties[] properties = new MotionEvent.PointerProperties[2];
        MotionEvent.PointerCoords[] coordinates = new MotionEvent.PointerCoords[2];
        for (int i = 0; i < 2; i++) {
            properties[i] = new MotionEvent.PointerProperties(); properties[i].id = i;
            properties[i].toolType = MotionEvent.TOOL_TYPE_FINGER;
            coordinates[i] = new MotionEvent.PointerCoords(); coordinates[i].x = 240 + i * 40;
            coordinates[i].y = 300; coordinates[i].pressure = coordinates[i].size = 1;
        }
        MotionEvent event = MotionEvent.obtain(down, down + elapsed, action | (1 << MotionEvent.ACTION_POINTER_INDEX_SHIFT),
                2, properties, coordinates, 0, 0, 1, 1, 0, 0, InputDevice.SOURCE_TOUCHSCREEN, 0);
        try { view.dispatchTouchEvent(event); } finally { event.recycle(); }
    }
}
