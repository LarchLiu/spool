package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
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
public class NativeHeaderTest {
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    private int[] pixels(View view) {
        Bitmap image = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888);
        try {
            view.draw(new Canvas(image)); int[] result = new int[image.getWidth() * image.getHeight()];
            image.getPixels(result, 0, image.getWidth(), 0, 0, image.getWidth(), image.getHeight()); return result;
        } finally { image.recycle(); }
    }

    @Test public void titleUsesTheAvailableHeaderWidthAndFocusDoesNotChangeItsAppearance() throws Exception {
        for (int width : new int[]{320, 360, 412}) for (boolean dark : new boolean[]{false, true}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(activity.get(), dark, "zh-CN");
                NativeUi.Header header = ui.new Header("生成提交说明并检查 Android 右侧栏的显示效果", "spool", () -> {}, () -> {}, () -> {});
                header.bindPeople(java.util.List.of(new JSONObject().put("id", "synthetic")));
                header.chatOptions(true, true, () -> {}); activity.get().setContentView(header); layout(header, width, 49);
                assertTrue("A long title must fill the space before the people button", header.title.getWidth() >= width - 170);
                assertTrue(header.title.getLayout().getEllipsisStart(0) >= 8);
                assertEquals("The chevron follows the title without leaving unused space", header.titleAction.getWidth(), header.titleChevron.getRight());
                int[] before = pixels(header.titleAction);
                assertTrue(header.titleAction.requestFocusFromTouch());
                assertArrayEquals("Focus must not paint a border or selected background", before, pixels(header.titleAction));
                header.title.setText("短标题"); layout(header, width, 49);
                assertTrue("A short title keeps its dropdown immediately after the text", header.titleChevron.getRight() < 80);
                header.chatOptions(false, false, () -> {}); layout(header, width, 49);
                assertEquals(View.GONE, header.titleChevron.getVisibility());
            }
        }
    }

    @Test public void panelTabsAndHistoryRowsKeepTheirAppearanceWhenFocused() throws Exception {
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), dark);
            NativePanelAction tab = new NativePanelAction(ui, "History", () -> {}); tab.tab = true; tab.setSelected(true);
            NativeGitHistoryRow row = new NativeGitHistoryRow(ui, new JSONObject().put("id", "a".repeat(40)).put("subject", "Synthetic commit").put("author", "Developer"), () -> {});
            for (View view : new View[]{tab, row}) {
                activity.get().setContentView(view); layout(view, 320, 60); view.clearFocus(); int[] before = pixels(view);
                assertTrue(view.requestFocusFromTouch()); assertArrayEquals(before, pixels(view));
                assertTrue(view.performClick());
            }
        }
    }

    @Test public void segmentedTabsPaintSelectionAndKeepItWhenFocused() {
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), dark);
            NativePanelTabs tabs = new NativePanelTabs(ui, "Git views", false);
            tabs.add("changes", "Changes", "", () -> tabs.select("changes"));
            tabs.add("history", "History", "", () -> tabs.select("history"));
            activity.get().setContentView(tabs); tabs.select("changes"); layout(tabs, 320, 42);
            int[] selected = pixels(tabs.action("changes"));
            tabs.action("history").performClick();
            int sample = tabs.action("changes").getWidth() * 6 + 6;
            assertNotEquals("Switching tabs must change the fill away from the label", selected[sample], pixels(tabs.action("changes"))[sample]);
            int[] history = pixels(tabs.action("history"));
            assertTrue(tabs.action("history").requestFocusFromTouch());
            assertArrayEquals("Keyboard focus keeps the selected fill without adding an outline", history, pixels(tabs.action("history")));
        }
    }
}
