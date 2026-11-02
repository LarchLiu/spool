package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.view.ViewGroup;
import android.view.Gravity;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.text.Selection;
import android.text.Spannable;
import android.view.accessibility.AccessibilityNodeInfo;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
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
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeMarkdownTest {
    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        view.layout(0, 0, width, view.getMeasuredHeight());
    }

    private View find(View view, String tag) {
        if (("markdown:" + tag).equals(view.getTag())) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            View found = find(((ViewGroup) view).getChildAt(i), tag); if (found != null) return found;
        }
        return null;
    }

    private boolean text(View view, String value) {
        if (view instanceof TextView && ((TextView) view).getText().toString().equals(value)) return true;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++)
            if (text(((ViewGroup) view).getChildAt(i), value)) return true;
        return false;
    }

    private void markerLines(View view) {
        if (view instanceof TextView && ((TextView) view).getText().toString().matches("[0-9]+\\.")) {
            assertEquals("An outside list marker must never wrap", 1, ((TextView) view).getLineCount());
            TextView marker = (TextView) view;
            assertTrue("A wide marker must not clip its final period", marker.getLayout().getLineWidth(0)
                    <= marker.getWidth() - marker.getCompoundPaddingLeft() - marker.getCompoundPaddingRight() + 1);
        }
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) markerLines(((ViewGroup) view).getChildAt(i));
    }

    @Test @Config(sdk = {26, 35}) public void rulesListsAndTaskStatesKeepTheirNativeSemanticsAtBothDensities() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                LinearLayout markdown = NativeMarkdown.view(ui, "Before.\n\n---\n\n0. First\n1. This item\n   continues\n\nParagraph\ncontinues\n\n- [x] Done\n- [ ] Pending\n\n1000. A wide marker\n1001. Another item", false, ignored -> fail());
                controller.get().setContentView(markdown); layout(markdown, ui.dp(324));
                assertEquals(ui.dp(1), find(markdown, "hr").getHeight());
                assertTrue(markdown.getHeight() < ui.dp(500));
                assertTrue(text(markdown, "0."));
                assertTrue(text(markdown, "This item continues"));
                assertTrue(text(markdown, "Paragraph\ncontinues"));
                assertTrue(text(markdown, "1000.")); markerLines(markdown);
                View marker = find(markdown, "input"); assertNotNull(marker); assertFalse(marker.isEnabled());
                ViewGroup item = (ViewGroup) marker.getParent(), content = (ViewGroup) item.getChildAt(0);
                TextView taskText = (TextView) content.getChildAt(0);
                assertEquals("CSS checkboxes sit on the text baseline", taskText.getBaseline(), marker.getBottom());
                AccessibilityNodeInfo info = marker.createAccessibilityNodeInfo();
                assertEquals("android.widget.CheckBox", info.getClassName());
                assertTrue(info.isCheckable()); assertTrue(info.isChecked()); assertFalse(info.isEnabled()); info.recycle();
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void longFlowsRoundCumulativePositionsWithoutLosingSelection() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeMarkdownFlow flow = new NativeMarkdownFlow(ui); flow.zeroFirst = flow.zeroLast = true;
            for (int i = 0; i < 100; i++) {
                TextView text = ui.text("Sample " + i, 14, ui.text); ui.fixedLineHeight(text, 1.75f); text.setTextIsSelectable(true);
                flow.add(text, 3.5f, 4.2f);
            }
            controller.get().setContentView(flow); layout(flow, 320);
            assertEquals(Math.round(100 * 24.5f + 99 * 4.2f), flow.getHeight());
            for (int i = 0; i < 100; i++) assertEquals(i * 28.7f, flow.getChildAt(i).getTop(), .51);
            TextView first = (TextView) flow.getChildAt(0); Selection.setSelection((Spannable) first.getText(), 2, 6);
            layout(flow, 260); assertEquals(2, first.getSelectionStart()); assertEquals(6, first.getSelectionEnd());
        }
    }

    @Test @Config(sdk = {26, 35}) public void longTablesShareColumnsAndRetainFractionalRowHeights() throws Exception {
        float cssRowHeight;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-line-metrics.json")) {
            assertNotNull(input);
            cssRowHeight = (float) new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONObject("table").getDouble("rowHeight");
        }
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                StringBuilder source = new StringBuilder("| Name | Value |\n| --- | ---: |\n");
                for (int i = 0; i < 20; i++) source.append("| Row ").append(i).append(" | ").append(i).append(" |\n");
                LinearLayout markdown = NativeMarkdown.view(ui, source.toString(), false, ignored -> fail());
                controller.get().setContentView(markdown); layout(markdown, ui.dp(324));
                NativeMarkdownTable table = (NativeMarkdownTable) find(markdown, "table");
                float scale = controller.get().getResources().getDisplayMetrics().density;
                float rowHeight = cssRowHeight * scale;
                assertEquals(Math.round(scale + 21 * rowHeight), table.getHeight());
                assertEquals(ui.dp(181), table.getWidth());
                for (int i = 0; i < 21; i++) {
                    View row = table.getChildAt(i); assertEquals(scale / 2 + i * rowHeight, row.getTop(), .51);
                    assertEquals(ui.dp(180), row.getWidth());
                }
            }
        }
    }

    private void collect(View view, View root, JSONArray nodes) throws Exception {
        String tag = view == root ? "markdown:root" : String.valueOf(view.getTag());
        if (tag.startsWith("markdown:")) {
            int x = 0, y = 0;
            for (View child = view; child != root; child = (View) child.getParent()) { x += child.getLeft(); y += child.getTop(); }
            JSONObject box = new JSONObject().put("type", tag.substring(9)).put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
            if (view instanceof NativeMarkdownListItem) box.put("marker", ((NativeMarkdownListItem) view).markerType);
            if (tag.equals("markdown:th") || tag.equals("markdown:td")) {
                int gravity = ((TextView) view).getGravity() & Gravity.HORIZONTAL_GRAVITY_MASK;
                box.put("align", gravity == Gravity.RIGHT ? "right" : gravity == Gravity.CENTER_HORIZONTAL ? "center" : "left");
            }
            nodes.put(box);
        }
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) collect(((ViewGroup) view).getChildAt(i), root, nodes);
    }

    @Test @Config(sdk = {26, 35}) public void actualWebMarkdownBlockFixturesRenderThroughProductionComponents() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("markdownCases");
        }
        Path directory = Path.of(System.getProperty("spool.ui.artifacts"));
        Files.createDirectories(directory);
        for (int width : new int[] {320, 360, 412}) for (boolean dark : new boolean[] {false, true}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark);
                LinearLayout page = ui.padded(18, 25); page.setBackgroundColor(ui.elevated);
                for (int i = 0; i < cases.length(); i++) {
                    LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.bottomMargin = 22;
                    JSONObject item = cases.getJSONObject(i);
                    NativeUi localeUi = new NativeUi(controller.get(), dark, item.optString("locale", "en"));
                    page.addView(NativeMarkdown.view(localeUi, item.getString("text"), false, ignored -> fail("Rendering must not load links")), params);
                }
                controller.get().setContentView(page);
                page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(100_000, View.MeasureSpec.AT_MOST));
                page.layout(0, 0, width, page.getMeasuredHeight());
                markerLines(page);
                JSONArray geometry = new JSONArray();
                for (int i = 0; i < page.getChildCount(); i++) { JSONArray nodes = new JSONArray(); collect(page.getChildAt(i), page.getChildAt(i), nodes); geometry.put(nodes); }
                String suffix = width + (dark ? "-dark" : "-light");
                String prefix = android.os.Build.VERSION.SDK_INT == 26 ? "native-api26-" : "native-";
                Files.writeString(directory.resolve(prefix + "markdown-blocks-" + suffix + ".json"), new JSONObject().put("cases", geometry).toString(2));
                Bitmap bitmap = Bitmap.createBitmap(width, page.getHeight(), Bitmap.Config.ARGB_8888); page.draw(new Canvas(bitmap));
                String imagePrefix = android.os.Build.VERSION.SDK_INT == 26 ? "native-api26-" : "";
                try (FileOutputStream output = new FileOutputStream(directory.resolve(imagePrefix + "markdown-blocks-" + suffix + ".png").toFile())) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                bitmap.recycle();
            }
        }
    }
}
