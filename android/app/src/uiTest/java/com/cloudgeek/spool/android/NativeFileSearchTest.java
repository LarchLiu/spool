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
public class NativeFileSearchTest {
    private void layout(View root, int width, int height) {
        root.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, width, height);
    }

    private JSONObject box(View view, View root) throws Exception {
        int x = 0, y = 0;
        for (View child = view; child != root; child = (View) child.getParent()) { x += child.getLeft(); y += child.getTop(); }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }

    private double compare(JSONObject actual, JSONObject web, float scale) throws Exception {
        double maximum = 0;
        for (String key : new String[] {"x", "y", "width", "height"}) maximum = Math.max(maximum, Math.abs(actual.getDouble(key) - web.getDouble(key) * scale));
        return maximum;
    }

    private int color(JSONArray rgba) throws Exception { return android.graphics.Color.argb(rgba.getInt(3), rgba.getInt(0), rgba.getInt(1), rgba.getInt(2)); }

    @Test public void nativeEditingCompositionSelectionAndClearSurviveSearchFieldResize() {
        for (String qualifier : new String[] {"mdpi", "420dpi", "xxhdpi"}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + qualifier);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                NativeSearchField field = new NativeSearchField(ui, ui.t("Search files"), ui.t("Clear file search"));
                FrameLayout root = new FrameLayout(ui.context); root.setFocusableInTouchMode(true);
                FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(ui.dp(200), ui.dp(32)); root.addView(field, params);
                controller.get().setContentView(root); layout(root, ui.dp(360), ui.dp(800));
                assertTrue(field.input.requestFocusFromTouch()); field.input.setText("abcdef");
                assertEquals(View.VISIBLE, field.clear.getVisibility());
                assertTrue(root.requestFocusFromTouch()); assertEquals(View.INVISIBLE, field.clear.getVisibility());
                assertTrue(field.input.requestFocusFromTouch()); assertEquals(View.VISIBLE, field.clear.getVisibility());
                field.input.setSelection(2, 4);
                android.view.inputmethod.InputConnection connection = field.input.onCreateInputConnection(new android.view.inputmethod.EditorInfo());
                assertNotNull(connection); assertTrue(connection.setComposingText("共有", 1));
                android.text.Editable document = field.input.getText(); String value = document.toString();
                int start = field.input.getSelectionStart(), end = field.input.getSelectionEnd();
                for (int width : new int[] {200, 80, 340, 200}) {
                    params.width = ui.dp(width); field.setLayoutParams(params); layout(root, ui.dp(360), ui.dp(800));
                    assertSame(document, field.input.getText()); assertEquals(value, document.toString());
                    assertEquals(start, field.input.getSelectionStart()); assertEquals(end, field.input.getSelectionEnd());
                    assertEquals(2, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(document));
                    assertEquals(4, android.view.inputmethod.BaseInputConnection.getComposingSpanEnd(document));
                }
                int[] changes = {0};
                field.input.addTextChangedListener(new android.text.TextWatcher() {
                    public void beforeTextChanged(CharSequence text, int start, int count, int after) {}
                    public void onTextChanged(CharSequence text, int start, int before, int count) { changes[0]++; }
                    public void afterTextChanged(android.text.Editable text) {}
                });
                assertTrue(field.clear.performClick()); assertEquals(1, changes[0]);
                assertTrue(field.input.hasFocus()); assertEquals("", field.input.getText().toString());
                assertEquals(-1, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(field.input.getText()));
                assertEquals(View.INVISIBLE, field.clear.getVisibility());
                assertEquals(ui.t("Clear file search"), field.clear.getContentDescription());
                assertEquals("android.widget.Button", field.clear.getAccessibilityClassName());
                field.input.setText("another"); field.setEnabled(false);
                assertFalse(field.input.isEnabled()); assertFalse(field.clear.isEnabled()); assertEquals(View.INVISIBLE, field.clear.getVisibility());
                field.setEnabled(true); assertTrue(field.input.requestFocusFromTouch()); assertEquals(View.VISIBLE, field.clear.getVisibility());
            }
        }
    }

    @Test public void originalCancelImagesKeepTheirDensityAndPressedThemeState() {
        for (String qualifier : new String[] {"mdpi", "420dpi", "xxhdpi"}) for (boolean dark : new boolean[] {false, true}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + qualifier);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, "en");
                NativeSearchCancel cancel = new NativeSearchCancel(ui);
                assertEquals(android.widget.ImageView.ScaleType.FIT_XY, cancel.getScaleType());
                assertTrue(cancel.isClickable()); assertFalse(cancel.isFocusable());
                int normal = dark ? R.drawable.web_search_cancel_dark : R.drawable.web_search_cancel_light;
                int pressed = dark ? R.drawable.web_search_cancel_dark_pressed : R.drawable.web_search_cancel_light_pressed;
                for (boolean active : new boolean[] {false, true, false}) {
                    cancel.setPressed(active);
                    android.graphics.drawable.BitmapDrawable actual = (android.graphics.drawable.BitmapDrawable) cancel.getDrawable().getCurrent();
                    Bitmap expected = android.graphics.BitmapFactory.decodeResource(ui.context.getResources(), active ? pressed : normal);
                    assertEquals(28, actual.getBitmap().getWidth()); assertEquals(28, actual.getBitmap().getHeight());
                    assertEquals(28, actual.getIntrinsicWidth()); assertEquals(28, actual.getIntrinsicHeight());
                    assertTrue(actual.isFilterBitmap()); assertNull(actual.getColorFilter());
                    assertTrue("Select the original theme/state resource without tint or density resampling", expected.sameAs(actual.getBitmap()));
                    expected.recycle();
                }
            }
        }
    }

    @Test public void productionHeaderFlexResizePreservesTheLiveComposingDocument() throws Exception {
        for (String qualifier : new String[] {"mdpi", "420dpi", "xxhdpi"}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
            RuntimeEnvironment.setQualifiers("w900dp-h800dp-" + qualifier);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                NativeFileModel state = new NativeFileModel("search");
                state.accept(".", new JSONObject().put("entries", new JSONArray()));
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", state, null,
                        (path, done) -> fail("Resize must not request a loaded directory"), file -> {}, address -> {}, () -> {}, value -> {});
                FrameLayout root = new FrameLayout(ui.context); root.addView(browser);
                controller.get().setContentView(root); browser.toggleSearch(true); layout(root, ui.dp(360), ui.dp(800));
                browser.search.setText("abcdef"); browser.search.setSelection(2, 4);
                android.view.inputmethod.InputConnection connection = browser.search.onCreateInputConnection(new android.view.inputmethod.EditorInfo());
                assertNotNull(connection); assertTrue(connection.setComposingText("共有", 1));
                android.text.Editable document = browser.search.getText(); String value = document.toString();
                int start = browser.search.getSelectionStart(), end = browser.search.getSelectionEnd();
                for (int width : new int[] {220, 360, 412, 900, 220}) {
                    layout(root, ui.dp(width), ui.dp(800));
                    assertSame(document, browser.search.getText()); assertEquals(value, document.toString());
                    assertEquals(value, state.query); assertTrue(browser.search.hasFocus());
                    assertEquals(start, browser.search.getSelectionStart()); assertEquals(end, browser.search.getSelectionEnd());
                    assertEquals(2, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(document));
                    assertEquals(4, android.view.inputmethod.BaseInputConnection.getComposingSpanEnd(document));
                    assertEquals(browser.header.getWidth() - ui.dp(136), browser.searchField.getWidth());
                    assertEquals(View.VISIBLE, browser.searchField.clear.getVisibility());
                }
                browser.deactivate();
            }
        }
    }

    @Test public void realHeaderSearchBoundsClearAndFocusMatchActualWebTransitions() throws Exception {
        JSONArray scenes;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-search.json")) {
            assertNotNull(input); scenes = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("cases");
        }
        JSONArray samples = new JSONArray(); int failures = 0; double maximum = 0;
        for (int c = 0; c < scenes.length(); c++) {
            JSONObject scene = scenes.getJSONObject(c); float density = (float) scene.getDouble("density"); int width = scene.getInt("width");
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + (density == 1 ? "mdpi" : density == 3 ? "xxhdpi" : "420dpi"));
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), scene.getString("theme").equals("dark"), scene.getString("locale"));
                ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeFileModel state = new NativeFileModel("search"); JSONArray entries = new JSONArray(), names = scene.getJSONArray("names");
                for (int i = 0; i < names.length(); i++) entries.put(new JSONObject().put("name", names.getString(i)).put("path", names.getString(i)).put("kind", "text"));
                state.accept(".", new JSONObject().put("entries", entries));
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", state, null,
                        (path, done) -> fail("Search fixtures are already loaded"), file -> {}, address -> {}, () -> {}, value -> {});
                FrameLayout root = new FrameLayout(ui.context); root.setBackgroundColor(ui.surface); root.setFocusableInTouchMode(true);
                root.addView(browser, new FrameLayout.LayoutParams(Math.min(ui.dp(360), Math.round(ui.dp(width) * .92f)), -1, android.view.Gravity.RIGHT));
                controller.get().setContentView(root); root.requestFocus();
                JSONArray records = scene.getJSONArray("records");
                for (int r = 0; r < records.length(); r++) {
                    JSONObject web = records.getJSONObject(r); String id = web.getString("id");
                    View field = browser.header.getChildAt(1), clear = field.findViewWithTag("search-clear");
                    switch (id) {
                        case "open-empty": case "reopened": browser.toggleSearch(true); break;
                        case "typed": browser.search.setText(web.getString("value")); break;
                        case "blurred": assertTrue(browser.header.getChildAt(3).requestFocusFromTouch()); break;
                        case "refocused": assertTrue(browser.search.requestFocusFromTouch()); break;
                        case "cleared": if (clear != null) clear.performClick(); break;
                        case "escaped": browser.search.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ESCAPE)); break;
                        case "closed-again": browser.toggleSearch(false); break;
                        default: assertEquals("closed", id);
                    }
                    layout(root, ui.dp(width), ui.dp(800)); JSONArray issues = new JSONArray();
                    double delta;
                    // Native header content excludes the separately painted one-CSS-pixel divider.
                    delta = Math.max(Math.abs(box(browser.header, root).getDouble("height") + ui.dp(50) - ui.dp(49) - web.getJSONObject("header").getDouble("height") * density),
                            Math.max(Math.abs(box(browser.header, root).getDouble("x") - web.getJSONObject("header").getDouble("x") * density),
                            Math.abs(box(browser.header, root).getDouble("width") - web.getJSONObject("header").getDouble("width") * density)));
                    boolean open = !web.isNull("search");
                    if (browser.search.isShown() != open || browser.search.hasFocus() != web.getBoolean("focused") || !browser.search.getText().toString().equals(web.getString("value"))) issues.put("visibility/focus/value");
                    JSONArray actions = web.getJSONArray("actions");
                    for (int a = 0; a < actions.length(); a++) {
                        View action = browser.header.getChildAt(a + 2); JSONObject expected = actions.getJSONObject(a);
                        delta = Math.max(delta, compare(box(action, root), expected.getJSONObject("box"), density));
                        if (!action.getContentDescription().toString().equals(expected.getString("label"))) issues.put("action label");
                    }
                    if (open) {
                        delta = Math.max(delta, compare(box(field, root), web.getJSONObject("search"), density));
                        delta = Math.max(delta, compare(box(browser.search, root), web.getJSONObject("input"), density));
                        JSONObject style = web.getJSONObject("style");
                        if (browser.search.getCurrentTextColor() != color(style.getJSONArray("textColor")) || browser.search.getCurrentHintTextColor() != color(style.getJSONArray("hintColor"))) issues.put("text/hint colors");
                        if (!browser.search.getHint().toString().equals(web.getString("hint"))) issues.put("hint translation");
                        JSONObject cancel = null; JSONArray ua = web.getJSONArray("ua");
                        for (int i = 0; i < ua.length(); i++) if (ua.getJSONObject(i).optJSONArray("attributes") != null && ua.getJSONObject(i).getJSONArray("attributes").toString().contains("-webkit-search-cancel-button")) cancel = ua.getJSONObject(i);
                        assertNotNull("Actual UA cancel geometry remains explicit", cancel);
                        if (clear == null) issues.put("missing clear control");
                        else {
                            delta = Math.max(delta, compare(box(clear, root), cancel, density));
                            if (clear.isShown() != (cancel.getDouble("opacity") > 0)) issues.put("clear visibility");
                        }
                        Bitmap surface = Bitmap.createBitmap(Math.max(1, field.getWidth() + ui.dp(8)), Math.max(1, field.getHeight() + ui.dp(8)), Bitmap.Config.ARGB_8888);
                        int[] borders = {0}, outlines = {0}; final int borderColor = color(style.getJSONArray("borderColor")), outlineColor = color(style.getJSONArray("outlineColor"));
                        field.draw(new Canvas(surface) {
                            @Override public void drawRoundRect(android.graphics.RectF rect, float rx, float ry, Paint paint) {
                                if (paint.getStyle() == Paint.Style.STROKE && Math.abs(paint.getStrokeWidth() - density) < .001) {
                                    if (paint.getColor() == borderColor) borders[0]++;
                                    if (paint.getColor() == outlineColor) outlines[0]++;
                                }
                                super.drawRoundRect(rect, rx, ry, paint);
                            }
                        }); surface.recycle();
                        if (borders[0] != 1 || outlines[0] != (web.getBoolean("focused") ? 1 : 0)) issues.put("border/focus outline");
                    }
                    if (delta > 1) issues.put("geometry"); maximum = Math.max(maximum, delta); if (issues.length() > 0) failures++;
                    samples.put(new JSONObject().put("scene", c).put("id", id).put("density", density).put("width", width).put("locale", scene.getString("locale"))
                            .put("theme", scene.getString("theme")).put("maximum", delta).put("issues", issues)
                            .put("field", open ? box(field, root) : JSONObject.NULL).put("input", open ? box(browser.search, root) : JSONObject.NULL)
                            .put("clear", open && clear != null ? box(clear, root) : JSONObject.NULL));
                    if (id.equals("typed") && density == 2.625 && width == 220 && scene.getString("locale").equals("zh-CN")) {
                        Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888); root.draw(new Canvas(image));
                        Path directory = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(directory);
                        try (java.io.OutputStream stream = Files.newOutputStream(directory.resolve("native-file-search-api" + android.os.Build.VERSION.SDK_INT + "-" + scene.getString("theme") + ".png"))) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream)); }
                        image.recycle();
                    }
                }
                browser.deactivate();
            }
        }
        Path directory = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(directory);
        Files.writeString(directory.resolve("native-file-search-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject().put("samples", samples).put("maximum", maximum).put("failures", failures).toString(2));
        assertEquals("Actual search geometry/clear/focus transitions must match Web without weakening the one-physical-pixel limit", 0, failures);
    }
}
