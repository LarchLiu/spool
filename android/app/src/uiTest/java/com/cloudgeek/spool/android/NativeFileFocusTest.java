package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.KeyEvent;
import android.view.View;
import android.widget.FrameLayout;
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
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeFileFocusTest {
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void realSearchAndKeyboardFocusFollowTheActualWebTransitions() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-focus.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("cases");
        }
        JSONArray failures = new JSONArray(); int snapshots = 0;
        for (int c = 0; c < cases.length(); c++) {
            JSONObject sample = cases.getJSONObject(c); float scale = (float) sample.getDouble("density");
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + (scale == 1 ? "mdpi" : scale == 3 ? "xxhdpi" : "420dpi"));
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), sample.getString("theme").equals("dark"), sample.getString("locale"));
                NativeFileModel state = new NativeFileModel("fonts"); String name = sample.getString("name");
                state.accept(".", new JSONObject().put("entries", new JSONArray()
                        .put(new JSONObject().put("name", name).put("path", name).put("kind", "text"))
                        .put(new JSONObject().put("name", "plain.txt").put("path", "plain.txt").put("kind", "text"))));
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null,
                        (path, done) -> fail("Focus/search must not read files"), file -> {}, url -> {}, () -> {}, value -> {});
                FrameLayout root = new FrameLayout(ui.context); root.setFocusableInTouchMode(true);
                root.addView(browser); controller.get().setContentView(root); root.requestFocus();
                layout(root, ui.dp(331), ui.dp(800));
                JSONArray records = sample.getJSONArray("records"); View recycled = null;
                for (int i = 0; i < records.length(); i++) {
                    JSONObject expected = records.getJSONObject(i); String id = expected.getString("id");
                    switch (id) {
                        case "open-empty": case "reopen": browser.toggleSearch(true); break;
                        case "unmatched": browser.search.setText("__spool_font_probe_no_match__"); break;
                        case "match-cjk": case "rematch": browser.search.setText(name); break;
                        case "match-latin": browser.search.setText("plain"); break;
                        case "clear": browser.search.setText(""); break;
                        case "escape":
                            assertTrue(browser.search.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ESCAPE))); break;
                        case "close": browser.toggleSearch(false); break;
                        case "tree-keyboard":
                            assertTrue(browser.list.requestFocusFromTouch());
                            assertTrue(browser.list.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_DOWN))); break;
                        case "tree-blur": assertTrue(browser.searchToggle.requestFocusFromTouch()); break;
                        case "search-blur": assertTrue(browser.header.getChildAt(3).requestFocusFromTouch()); break;
                        default: assertEquals("closed", id);
                    }
                    layout(root, ui.dp(331), ui.dp(800));
                    JSONObject cssRing = expected.optJSONObject("ring");
                    if (cssRing != null) {
                        assertEquals("rgb(0, 159, 255)", cssRing.getString("color"));
                        assertEquals(-1, cssRing.getDouble("offset"), 0);
                    }
                    if (id.equals("match-cjk") && scale == 2.625f) {
                        Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
                        root.draw(new Canvas(image));
                        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
                        try (java.io.OutputStream stream = Files.newOutputStream(output.resolve("native-file-focus-api" + android.os.Build.VERSION.SDK_INT
                                + "-" + sample.getString("locale") + "-" + sample.getString("theme") + ".png"))) {
                            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                        } finally { image.recycle(); }
                    }
                    JSONArray paths = new JSONArray(), rings = new JSONArray();
                    for (int r = 0; r < browser.list.getAdapter().getCount(); r++) {
                        NativeFileModel.Row item = (NativeFileModel.Row) browser.list.getAdapter().getItem(r);
                        paths.put(item.entry.path);
                        View row = browser.list.getAdapter().getView(r, recycled, browser.list);
                        if (recycled != null) assertSame("Focus ink must reset on the real recycled adapter row", recycled, row);
                        recycled = row;
                        layout(row, ui.dp(220), ui.dp(30));
                        Bitmap image = Bitmap.createBitmap(row.getWidth(), row.getHeight(), Bitmap.Config.ARGB_8888);
                        row.draw(new Canvas(image) {
                            @Override public void drawRoundRect(float left, float top, float right, float bottom, float rx, float ry, Paint paint) {
                                if (paint.getStyle() == Paint.Style.STROKE && paint.getColor() == 0xff009fff) {
                                    rings.put(item.entry.path);
                                    JSONObject ring = expected.optJSONObject("ring");
                                    if (ring != null) {
                                        float stroke = (float) ring.optDouble("width") * scale;
                                        if (Math.abs(paint.getStrokeWidth() - stroke) > .0001
                                                || Math.abs(left - (2 * scale + stroke / 2)) > .0001
                                                || Math.abs(top - stroke / 2) > .0001
                                                || Math.abs(right - (row.getWidth() - 2 * scale - stroke / 2)) > .0001
                                                || Math.abs(bottom - (row.getHeight() - stroke / 2)) > .0001
                                                || Math.abs(rx - ((float) ring.optDouble("radius") * scale - stroke / 2)) > .0001 || rx != ry)
                                            failures.put(id + "/" + scale + ": wrong physical outline geometry");
                                    }
                                }
                                super.drawRoundRect(left, top, right, bottom, rx, ry, paint);
                            }
                        });
                        image.recycle();
                    }
                    String ring = rings.length() == 1 ? rings.getString(0) : rings.length() == 0 ? "" : rings.toString();
                    if (!paths.toString().equals(expected.getJSONArray("paths").toString()) || !ring.equals(expected.getString("focus"))
                            || !state.focused.equals(expected.getString("logicalFocus")) || state.searchOpen != expected.getBoolean("search")
                            || browser.search.hasFocus() != expected.getBoolean("searchFocused"))
                        failures.put(new JSONObject().put("id", id).put("density", scale).put("locale", sample.getString("locale"))
                                .put("theme", sample.getString("theme")).put("paths", paths).put("ring", ring).put("logicalFocus", state.focused)
                                .put("searchFocused", browser.search.hasFocus()).put("expected", expected));
                    snapshots++;
                }
                browser.deactivate();
            }
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-file-focus-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject()
                .put("purpose", "Actual production rows/search/keyboard versus real Web focus states and CSS outline geometry, not full raster/device parity")
                .put("snapshots", snapshots).put("failures", failures).toString(2));
        assertEquals("File focus must follow actual Web search/blur and physical CSS outlines", 0, failures.length());
    }

    @Test public void lazyListingsAndCollapsedDescendantsUseActualWebFocusCandidates() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-tree-focus.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("cases");
        }
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeFileModel state = new NativeFileModel("tree");
            NativeFileBrowser browser = new NativeFileBrowser(new NativeUi(controller.get(), false), "Files", state, null,
                    (path, done) -> fail("Model replay must not read files"), file -> {}, url -> {}, () -> {}, value -> {});
            controller.get().setContentView(browser);
            for (int i = 0; i < cases.length(); i++) {
                JSONObject expected = cases.getJSONObject(i), operation = expected.getJSONObject("operation");
                switch (operation.getString("type")) {
                    case "load": state.accept(operation.getString("path"), new JSONObject().put("entries", operation.getJSONArray("entries"))); break;
                    case "focus": state.focused = operation.getString("path"); break;
                    case "open": state.beginSearch(); break;
                    case "close": state.closeSearch(); break;
                    case "query": state.search(operation.getString("value")); break;
                    case "expand": state.expanded.add(operation.getString("path")); break;
                    case "collapse": state.expanded.remove(operation.getString("path")); break;
                    default: fail("Unknown operation");
                }
                browser.renderTree();
                assertEquals(operation.toString(), expected.getString("focus"), state.focused);
                JSONArray rows = expected.getJSONArray("rows");
                assertEquals(rows.length(), browser.list.getAdapter().getCount());
                for (int r = 0; r < rows.length(); r++) {
                    NativeFileModel.Row row = (NativeFileModel.Row) browser.list.getAdapter().getItem(r);
                    assertEquals(rows.getJSONObject(r).getString("path"), row.entry.path);
                    assertEquals(rows.getJSONObject(r).getBoolean("expanded"), row.expanded);
                }
            }
            browser.deactivate();
        }
    }
}
