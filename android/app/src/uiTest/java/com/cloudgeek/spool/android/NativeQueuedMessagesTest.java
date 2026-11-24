package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import org.json.JSONArray;
import org.json.JSONObject;
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
public class NativeQueuedMessagesTest {
    private void layout(View page, int width, int height) {
        page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        page.layout(0, 0, width, height);
        page.getViewTreeObserver().dispatchOnPreDraw();
    }

    private JSONArray messages(int count) throws Exception {
        JSONArray messages = new JSONArray();
        for (int i = 0; i < count; i++) messages.put(new JSONObject().put("id", "queued-" + i)
                .put("message", i == 0 ? "Is there only a TypeScript SDK?" : "Check whether only the TypeScript SDK is published. ".repeat(5)));
        return messages;
    }

    private void screenshot(View view, String name) throws Exception {
        // Screenshots are retained only by the Gradle task that names the artifact directory.
        String artifacts = System.getProperty("spool.ui.artifacts");
        if (android.os.Build.VERSION.SDK_INT != 35 || artifacts == null) return;
        File directory = new File(artifacts, "../queued-messages");
        assertTrue(directory.isDirectory() || directory.mkdirs());
        Bitmap image = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(image));
        try (FileOutputStream out = new FileOutputStream(new File(directory, "android-" + name + ".png"))) {
            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, out));
        } finally { image.recycle(); }
    }

    private static View described(ViewGroup row, String label) {
        for (int i = 0; i < row.getChildCount(); i++)
            if (label.contentEquals(String.valueOf(row.getChildAt(i).getContentDescription()))) return row.getChildAt(i);
        throw new AssertionError("No " + label + " in the row");
    }

    /** The flexible message text is the only weighted child of a row. */
    private static TextView body(ViewGroup row) {
        for (int i = 0; i < row.getChildCount(); i++)
            if (((LinearLayout.LayoutParams) row.getChildAt(i).getLayoutParams()).weight > 0) return (TextView) row.getChildAt(i);
        throw new AssertionError("No message text in the row");
    }

    private static View after(ViewGroup row, View child) { return row.getChildAt(row.indexOfChild(child) + 1); }

    private void advance(long millis) {
        Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(millis));
    }

    private final class DragFixture implements AutoCloseable {
        final ActivityController<Activity> controller;
        final NativeUi ui;
        final NativeQueuedMessages queue;
        final LinearLayout page;
        final java.util.List<String> moves = new java.util.ArrayList<>();
        float x, y;
        long down;

        DragFixture(int count, boolean dark) throws Exception {
            RuntimeEnvironment.setQualifiers("w390dp-h420dp-mdpi");
            controller = Robolectric.buildActivity(Activity.class).setup();
            ui = new NativeUi(controller.get(), dark);
            page = ui.column(); page.setBackgroundColor(ui.surface);
            page.addView(new View(ui.context), new LinearLayout.LayoutParams(-1, ui.dp(40)));
            queue = new NativeQueuedMessages(ui, id -> {}, id -> {}, id -> {}, (id, before) -> moves.add(id + ":" + before));
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2);
            params.leftMargin = params.rightMargin = ui.dp(28);
            page.addView(queue, params); controller.get().setContentView(page);
            queue.bind(messages(count), true, false);
            relayout(); advance(240);
        }

        void relayout() { layout(page, ui.dp(390), ui.dp(420)); }
        ViewGroup rows() { return (ViewGroup) queue.getChildAt(0); }
        View row(int at) { return rows().getChildAt(at); }
        void press(int at) { press(at, .5f); }
        void press(int at, float fraction) {
            View grip = described((ViewGroup) row(at), ui.t("Drag to reorder"));
            x = queue.getLeft() + rows().getLeft() + row(at).getX() + grip.getLeft() + grip.getWidth() / 2f;
            y = queue.getTop() + rows().getTop() + row(at).getY() + grip.getTop() + grip.getHeight() * fraction - queue.getScrollY();
            down = android.os.SystemClock.uptimeMillis();
            touch(MotionEvent.ACTION_DOWN, x, y);
        }
        void touch(int action, float x, float y) {
            MotionEvent event = MotionEvent.obtain(down, android.os.SystemClock.uptimeMillis(), action, x, y, 0);
            // Dispatch through the parent as a device would, including moves outside the queue.
            try { assertTrue(page.dispatchTouchEvent(event)); } finally { event.recycle(); }
        }
        void inside(View row) {
            float top = rows().getTop() + row.getY() - queue.getScrollY();
            assertTrue("The moving row stays below the queue's top edge", top >= queue.getPaddingTop() - .1f);
            assertTrue("The moving row stays above the queue's bottom edge", top + row.getHeight() <= queue.getHeight() - queue.getPaddingBottom() + .1f);
            assertEquals("Dragging stays vertical", 0, row.getTranslationX(), .01f);
            assertEquals("The moving row stays opaque", 1, row.getAlpha(), .01f);
        }
        void settled() {
            relayout(); advance(240);
            for (int i = 0; i < rows().getChildCount(); i++) {
                assertEquals(0, row(i).getTranslationY(), .01f);
                assertEquals(0, row(i).getElevation(), .01f);
                assertFalse(described((ViewGroup) row(i), ui.t("Drag to reorder")).isPressed());
            }
        }
        @Override public void close() { controller.close(); }
    }

    @Test public void aHandleStartsAfterSmallVerticalMovementAndSwapsAtStableMidpoints() throws Exception {
        try (DragFixture f = new DragFixture(3, false)) {
            f.press(1, .2f);
            View moving = f.row(1), neighbor = f.row(2);
            f.touch(MotionEvent.ACTION_MOVE, f.x + 80, f.y + f.ui.dp(4));
            assertEquals("Touch slop does not pick up the row", 0, moving.getTranslationY(), .01f);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y + moving.getHeight() - 2);
            advance(200);
            assertEquals("The grab offset does not trigger a premature swap", 0, neighbor.getTranslationY(), .01f);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y + moving.getHeight() + 2);
            advance(200);
            assertEquals(-moving.getHeight(), neighbor.getTranslationY(), .01f);
            // The neighbor's animation must not feed back into the next hit test.
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y + moving.getHeight() + 3);
            advance(200);
            assertEquals(-moving.getHeight(), neighbor.getTranslationY(), .01f);
            assertTrue("Moving only previews; releasing commits", f.moves.isEmpty());
            f.touch(MotionEvent.ACTION_UP, f.x, f.y + moving.getHeight() + 3);
            assertEquals(java.util.List.of("queued-1:"), f.moves);
            assertEquals("queued-1", f.row(2).getTag());
            f.settled();
        }
    }

    @Test public void draggingOutsideTheQueueKeepsTheActualRowInsideAndCommitsOnce() throws Exception {
        for (boolean dark : new boolean[] {false, true}) try (DragFixture f = new DragFixture(3, dark)) {
            f.press(2);
            View moving = f.row(2);
            f.touch(MotionEvent.ACTION_MOVE, f.x - 200, f.y - 300);
            advance(200); f.inside(moving);
            assertEquals(moving.getHeight(), f.row(0).getTranslationY(), .01f);
            screenshot(f.page, dark ? "drag-contained-dark" : "drag-contained-light");
            f.touch(MotionEvent.ACTION_MOVE, f.x + 500, f.y + 300);
            f.inside(moving);
            f.touch(MotionEvent.ACTION_MOVE, f.x - 200, f.y - 300);
            f.touch(MotionEvent.ACTION_UP, f.x - 200, f.y - 300);
            assertEquals(java.util.List.of("queued-2:queued-0"), f.moves);
            assertEquals("queued-2", f.row(0).getTag());
            f.settled();
        }
    }

    @Test public void aTapCancelAndReturnToTheOriginalSlotDoNotSendMoves() throws Exception {
        try (DragFixture f = new DragFixture(3, false)) {
            f.press(1);
            f.touch(MotionEvent.ACTION_UP, f.x, f.y);
            f.settled();
            f.press(1);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y - 80);
            f.touch(MotionEvent.ACTION_CANCEL, f.x, f.y - 80);
            f.settled();
            f.press(1);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y - 80);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y);
            f.touch(MotionEvent.ACTION_UP, f.x, f.y);
            f.settled();
            assertTrue(f.moves.isEmpty());
            assertEquals("queued-1", f.row(1).getTag());
        }
    }

    @Test public void holdingAtTheEdgesScrollsBothWaysAndStopsOnCancel() throws Exception {
        try (DragFixture f = new DragFixture(20, true)) {
            f.press(1);
            View moving = f.row(1);
            float bottom = f.queue.getBottom() + 80;
            f.touch(MotionEvent.ACTION_MOVE, f.x, bottom);
            advance(160);
            int first = f.queue.getScrollY();
            assertTrue("A stationary finger scrolls the long queue", first > 0);
            f.inside(moving);
            advance(160);
            assertTrue(f.queue.getScrollY() > first);
            int lower = f.queue.getScrollY();
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.queue.getTop() - 80);
            advance(160);
            assertTrue("Moving to the top reverses the scroll", f.queue.getScrollY() < lower);
            f.inside(moving);
            f.touch(MotionEvent.ACTION_CANCEL, f.x, f.queue.getTop() - 80);
            int stopped = f.queue.getScrollY();
            advance(160);
            assertEquals(stopped, f.queue.getScrollY());
            assertTrue(f.moves.isEmpty());
            f.settled();
        }
    }

    @Test public void aScrolledDragCanReachAndCommitTheVeryLastSlot() throws Exception {
        try (DragFixture f = new DragFixture(20, false)) {
            f.press(0);
            View moving = f.row(0);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.queue.getBottom() + 80);
            for (int tick = 0; tick < 40 && f.queue.canScrollVertically(1); tick++) advance(160);
            assertFalse("Scroll " + f.queue.getScrollY() + ", content " + f.rows().getHeight() + ", viewport " + f.queue.getHeight(), f.queue.canScrollVertically(1));
            f.inside(moving);
            f.touch(MotionEvent.ACTION_UP, f.x, f.queue.getBottom() + 80);
            assertEquals(java.util.List.of("queued-0:"), f.moves);
            assertEquals("queued-0", f.row(19).getTag());
            f.settled();
        }
    }

    @Test public void aBusyOrReplacedQueueCancelsTheActiveDrag() throws Exception {
        try (DragFixture f = new DragFixture(3, false)) {
            f.press(1);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y - 80);
            f.queue.bind(messages(3), true, true);
            f.touch(MotionEvent.ACTION_UP, f.x, f.y - 80);
            f.settled();
            assertFalse(described((ViewGroup) f.row(1), f.ui.t("Drag to reorder")).isEnabled());
            f.queue.bind(messages(3), true, false); f.relayout(); advance(240);
            f.press(1);
            f.touch(MotionEvent.ACTION_MOVE, f.x, f.y - 80);
            f.queue.bind(messages(1), true, false);
            f.touch(MotionEvent.ACTION_UP, f.x, f.y - 80);
            f.relayout(); advance(240);
            assertTrue(f.moves.isEmpty());
            assertEquals(1, f.queue.rows());
            assertEquals(0, f.row(0).getTranslationY(), .01f);
        }
    }

    @Test public void swipingTheMessageBodyStillScrollsWithoutReordering() throws Exception {
        try (DragFixture f = new DragFixture(20, false)) {
            float x = f.queue.getLeft() + f.queue.getWidth() / 2f;
            float y = f.queue.getTop() + f.row(2).getTop() + f.row(2).getHeight() / 2f;
            f.down = android.os.SystemClock.uptimeMillis();
            f.touch(MotionEvent.ACTION_DOWN, x, y);
            f.touch(MotionEvent.ACTION_MOVE, x, y - 40);
            f.touch(MotionEvent.ACTION_MOVE, x, y - 80);
            assertTrue("The text area retains normal queue scrolling", f.queue.getScrollY() > 0);
            f.touch(MotionEvent.ACTION_UP, x, y - 80);
            assertTrue(f.moves.isEmpty());
            for (int i = 0; i < f.rows().getChildCount(); i++) assertEquals(0, f.row(i).getElevation(), .01f);
        }
    }

    @Test public void failedAndSendingRowsKeepTheirTextAndActionsInsideANarrowQueue() throws Exception {
        RuntimeEnvironment.setQualifiers("w320dp-h420dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeQueuedMessages queue = new NativeQueuedMessages(ui, id -> {}, id -> {}, id -> {}, (id, before) -> {});
            controller.get().setContentView(queue);
            JSONArray messages = messages(3);
            messages.getJSONObject(1).put("delivery_status", "failed");
            messages.getJSONObject(2).put("delivery_status", "sending");
            queue.bind(messages, true, false);
            layout(queue, ui.dp(264), ui.dp(NativeQueuedMessages.MAX_HEIGHT_DP));
            ViewGroup rows = (ViewGroup) queue.getChildAt(0);
            assertEquals(3, rows.getChildCount());
            for (int i = 0; i < 3; i++) {
                ViewGroup row = (ViewGroup) rows.getChildAt(i);
                TextView text = body(row);
                assertTrue("Row " + i + " keeps readable text", text.getWidth() >= ui.dp(24));
                assertEquals(ui.dp(NativeQueuedMessages.ROW_HEIGHT_DP), row.getHeight());
                View last = row.getChildAt(row.getChildCount() - 1);
                assertTrue(last.getRight() <= row.getWidth() - ui.dp(8));
                for (int child = 1; child < row.getChildCount(); child++)
                    assertTrue(row.getChildAt(child - 1).getRight() <= row.getChildAt(child).getLeft());
                if (i > 0) assertTrue(after(row, text) instanceof NativeDeliveryStatus);
            }
            ViewGroup failed = (ViewGroup) rows.getChildAt(1), sending = (ViewGroup) rows.getChildAt(2);
            assertSame(failed.getChildAt(failed.getChildCount() - 1), described(failed, ui.t("Queued message actions")));
            described(failed, ui.t("Delete queued message"));
            assertSame("A sending row ends with its status", sending.getChildAt(sending.getChildCount() - 1), after(sending, body(sending)));
        }
    }

    @Test public void openQueueSidesJoinTheComposerWithoutDrawingAnotherBottomLine() throws Exception {
        for (String density : new String[] {"mdpi", "420dpi"}) for (int width : new int[] {320, 390, 900}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h420dp-" + density);
            for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark);
                LinearLayout page = ui.column(); page.setBackgroundColor(ui.surface);
                page.addView(new View(ui.context), new LinearLayout.LayoutParams(-1, 0, 1));
                NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
                NativeQueuedMessages queue = new NativeQueuedMessages(ui, id -> {}, id -> {}, id -> {}, (id, before) -> {});
                composer.addView(queue, 0, new LinearLayout.LayoutParams(-1, -2));
                page.addView(composer); controller.get().setContentView(page);
                layout(page, ui.dp(width), ui.dp(420));
                int composerTop = composer.getTop() + composer.box.getTop();
                for (int count : new int[] {1, 3, 20}) {
                    queue.bind(messages(count), true, false); layout(page, ui.dp(width), ui.dp(420));
                    assertEquals(composerTop, composer.getTop() + composer.box.getTop());
                    assertEquals("The sides overlap the rounded input border", composer.box.getTop() + ui.dp(1), queue.getBottom());
                    assertEquals(ui.dp(16), queue.getLeft() - composer.box.getLeft());
                    assertEquals(ui.dp(16), composer.box.getRight() - queue.getRight());
                    assertTrue(queue.getHeight() <= ui.dp(NativeQueuedMessages.MAX_HEIGHT_DP));
                    Bitmap frame = Bitmap.createBitmap(queue.getWidth(), queue.getHeight(), Bitmap.Config.ARGB_8888);
                    queue.draw(new Canvas(frame));
                    assertEquals("The queue has no bottom rule", ui.elevated, frame.getPixel(frame.getWidth() / 2, frame.getHeight() - 1));
                    assertEquals("Left side reaches the bottom", ui.borderStrong, frame.getPixel(0, frame.getHeight() - 1));
                    assertEquals("Right side reaches the bottom", ui.borderStrong, frame.getPixel(frame.getWidth() - 1, frame.getHeight() - 1));
                    frame.recycle();
                    ViewGroup rows = (ViewGroup) queue.getChildAt(0);
                    for (int i = 0; i < rows.getChildCount(); i++) {
                        ViewGroup row = (ViewGroup) rows.getChildAt(i);
                        View text = body(row), action = after(row, text);
                        View remove = described(row, ui.t("Delete queued message")), more = described(row, ui.t("Queued message actions"));
                        assertEquals(ui.t("Steer"), ((TextView) action).getText().toString());
                        assertTrue("The action keeps its tap height", action.getHeight() >= ui.dp(26));
                        assertEquals(ui.dp(NativeQueuedMessages.ROW_HEIGHT_DP), row.getHeight());
                        assertTrue(text.getWidth() > 0);
                        assertTrue(text.getRight() <= action.getLeft());
                        assertTrue(action.getRight() <= remove.getLeft());
                        assertTrue(remove.getRight() <= more.getLeft());
                        assertTrue(more.getRight() <= row.getWidth() - ui.dp(8));
                    }
                    if (density.equals("mdpi") && width == 390) screenshot(page, width + (dark ? "-dark-" : "-light-") + count);
                }
            }
        }
    }

    @Test public void attachmentNamesAndVectorActionsUseTheSameSingleRowLayout() throws Exception {
        RuntimeEnvironment.setQualifiers("w390dp-h420dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), true, "zh-CN");
            java.util.List<String> actions = new java.util.ArrayList<>();
            NativeQueuedMessages queue = new NativeQueuedMessages(ui, id -> actions.add("steer:" + id), id -> {}, id -> {}, (id, before) -> {});
            controller.get().setContentView(queue);
            queue.bind(new JSONArray().put(new JSONObject().put("id", "files").put("message", " ")
                    .put("attachments", new JSONArray().put("notes.txt").put("design.png"))), false, false);
            layout(queue, 334, ui.dp(NativeQueuedMessages.MAX_HEIGHT_DP));
            ViewGroup row = (ViewGroup) ((ViewGroup) queue.getChildAt(0)).getChildAt(0);
            assertEquals(View.IMPORTANT_FOR_ACCESSIBILITY_NO, row.getChildAt(0).getImportantForAccessibility());
            assertEquals("notes.txt, design.png", body(row).getText().toString());
            ViewGroup attachments = (ViewGroup) described(row, ui.t("Attachments") + ": notes.txt, design.png");
            assertEquals(2, attachments.getChildCount());
            assertTrue(attachments.getRight() <= body(row).getLeft());
            screenshot(queue, "mixed-attachments");
            TextView action = (TextView) after(row, body(row));
            assertEquals(ui.t("Send now"), action.getText().toString());
            assertNotNull(action.getCompoundDrawablesRelative()[0]);
            action.performClick(); assertEquals(java.util.List.of("steer:files"), actions);
            described(row, ui.t("Queued message actions")).performClick();
            ViewGroup option = (ViewGroup) ui.activePopover.content.getChildAt(0);
            assertEquals(ui.t("Edit message"), option.getContentDescription());
            assertTrue("The only option is a full touch target", option.getMinimumHeight() >= ui.dp(48));
            assertTrue("The option takes focus like the Web autofocus", option.isFocused());
            assertTrue(option.getChildAt(0) instanceof android.widget.ImageView);
            assertEquals(ui.dp(180), ui.activePopover.panel().getLayoutParams().width);
            ui.dismissMenus();
        }
    }

    @Test public void attachmentIndicatorsStayVisibleBeforeLongTextInEveryDeliveryState() throws Exception {
        RuntimeEnvironment.setQualifiers("w390dp-h420dp-mdpi");
        for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark);
            NativeQueuedMessages queue = new NativeQueuedMessages(ui, id -> {}, id -> {}, id -> {}, (id, before) -> {});
            controller.get().setContentView(queue);
            for (String status : new String[] {"", "sending", "failed"}) {
                for (String[] names : new String[][] {{}, {"photo.JPG", "plan.webp"}, {"说明.md"}, {"photo.jpg", "说明.md"}}) {
                    JSONObject message = new JSONObject().put("id", "files").put("message", "Long follow-up message ".repeat(10))
                            .put("delivery_status", status).put("attachments", new JSONArray(java.util.List.of(names)));
                    queue.bind(new JSONArray().put(message), true, false);
                    layout(queue, 264, ui.dp(NativeQueuedMessages.MAX_HEIGHT_DP));
                    ViewGroup row = (ViewGroup) ((ViewGroup) queue.getChildAt(0)).getChildAt(0);
                    TextView text = body(row);
                    assertTrue(text.getWidth() > 0);
                    if (names.length > 0) {
                        ViewGroup attachments = (ViewGroup) described(row, ui.t("Attachments") + ": " + String.join(", ", names));
                        int icons = names.length == 2 && names[1].endsWith(".md") ? 2 : 1;
                        assertEquals(icons, attachments.getChildCount());
                        assertTrue(attachments.getRight() <= text.getLeft());
                        for (int i = 0; i < icons; i++) assertEquals(ui.dp(15), attachments.getChildAt(i).getWidth());
                    } else assertEquals(1, row.indexOfChild(text));
                    assertTrue(text.getRight() <= after(row, text).getLeft());
                    assertTrue(row.getChildAt(row.getChildCount() - 1).getRight() <= row.getWidth() - ui.dp(8));
                    if (names.length == 2 && names[1].endsWith(".md")) screenshot(queue, "mixed-" + (dark ? "dark-" : "light-") + (status.isEmpty() ? "accepted" : status));
                }
            }
        }
    }
}
