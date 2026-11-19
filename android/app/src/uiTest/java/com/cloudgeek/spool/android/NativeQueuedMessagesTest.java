package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
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
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
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
}
