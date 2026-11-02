package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.TextView;
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
import org.robolectric.util.ReflectionHelpers;

/** Actual list children, not independently positioned expected rows. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeFileRowsTest {
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    private int top(View view, View root) {
        int y = 0;
        for (View child = view; child != root; child = (View) child.getParent()) y += child.getTop();
        return y;
    }

    @Test public void longListRecyclingScrollAndFilteringPreserveTheFractionalRowGrid() throws Exception {
        for (String qualifier : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + qualifier);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false); float density = ui.context.getResources().getDisplayMetrics().density;
                NativeFileModel state = new NativeFileModel("scroll"); JSONArray entries = new JSONArray();
                for (int i = 1999; i >= 0; i--) {
                    String name = String.format(java.util.Locale.ROOT, "file-%04d.txt", i);
                    entries.put(new JSONObject().put("name", name).put("path", name).put("kind", "text"));
                }
                state.accept(".", new JSONObject().put("entries", entries));
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null,
                        (path, done) -> fail("All scroll fixtures are already loaded"), file -> {}, address -> {}, () -> {}, value -> {});
                FrameLayout root = new FrameLayout(ui.context); root.addView(browser); controller.get().setContentView(root);
                layout(root, ui.dp(360), ui.dp(800));
                // Supplemental native invariants; the independent DOM/CDP test checks absolute placement.
                View recycled = null; int extent = 0;
                for (int i = 0; i < 2000; i++) {
                    View row = browser.list.getAdapter().getView(i, recycled, browser.list);
                    if (recycled != null) assertSame(recycled, row);
                    recycled = row; extent += row.getLayoutParams().height;
                }
                assertEquals(2000 * 30 * density, extent, .5);
                for (int position : new int[] {0, 37, 118, 1999, 0}) {
                    browser.list.setSelectionFromTop(position, -ui.dp(3));
                    layout(root, ui.dp(360), ui.dp(position == 118 ? 500 : 800));
                    assertTrue(browser.list.getChildCount() > 0); int first = browser.list.getFirstVisiblePosition();
                    int count = browser.list.getChildCount(), firstTop = browser.list.getChildAt(0).getTop();
                    for (int i = 0; i < count; i++) {
                        TextView row = (TextView) browser.list.getChildAt(i);
                        assertEquals(String.format(java.util.Locale.ROOT, "file-%04d.txt", first + i), row.getText().toString());
                        assertEquals(i * 30 * density, row.getTop() - firstTop, 1);
                    }
                    assertEquals(count * 30 * density, browser.list.getChildAt(count - 1).getBottom() - firstTop, 1);
                }
                state.search("file-000"); browser.renderTree(); layout(root, ui.dp(360), ui.dp(800));
                assertEquals(10, browser.list.getAdapter().getCount());
                assertEquals(10, browser.list.getChildCount());
                assertEquals(10 * 30 * density,
                        browser.list.getChildAt(9).getBottom() - browser.list.getChildAt(0).getTop(), 1);
                browser.deactivate();
            }
        }
    }

    @Test public void realFileListRowsAndBaselinesDoNotAccumulateDensityRounding() throws Exception {
        JSONArray scenes;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-overflow.json")) {
            assertNotNull(input);
            scenes = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("cases");
        }
        JSONArray samples = new JSONArray(); int failures = 0, orderFailures = 0;
        double positionMaximum = 0, baselineMaximum = 0, iconMaximum = 0;
        for (int c = 0; c < scenes.length(); c++) {
            JSONObject scene = scenes.getJSONObject(c);
            if (scene.has("limit")) continue;
            float density = (float) scene.getDouble("density"); int width = scene.getInt("width");
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + (density == 1 ? "mdpi" : density == 3 ? "xxhdpi" : "420dpi"));
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), scene.getString("theme").equals("dark"), scene.getString("locale"));
                ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                JSONArray expected = scene.getJSONArray("rows"), entries = new JSONArray();
                for (int i = 0; i < expected.length(); i++) {
                    String name = expected.getJSONObject(i).getString("name");
                    entries.put(new JSONObject().put("name", name).put("path", name).put("kind", "text"));
                }
                NativeFileModel state = new NativeFileModel("rows"); state.accept(".", new JSONObject().put("entries", entries));
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", state, null,
                        (path, done) -> fail("All synthetic files are already loaded"), file -> {}, address -> {}, () -> {}, value -> {});
                FrameLayout root = new FrameLayout(ui.context); root.setFocusableInTouchMode(true);
                root.addView(browser, new FrameLayout.LayoutParams(Math.min(ui.dp(360), Math.round(ui.dp(width) * .92f)), -1, android.view.Gravity.RIGHT));
                controller.get().setContentView(root); root.requestFocus(); layout(root, ui.dp(width), ui.dp(800));
                assertEquals(expected.length(), browser.list.getChildCount());
                float[] iconTops = new float[expected.length()]; java.util.Arrays.fill(iconTops, Float.NaN);
                for (int i = 0; i < expected.length(); i++) {
                    View row = browser.list.getChildAt(i); final int index = i;
                    android.graphics.drawable.Drawable original = ReflectionHelpers.getField(row, "icon");
                    ReflectionHelpers.setField(row, "icon", new android.graphics.drawable.Drawable() {
                        final android.graphics.Matrix matrix = new android.graphics.Matrix();
                        final float[] point = new float[2];
                        @Override public void draw(Canvas canvas) {
                            canvas.getMatrix(matrix); point[0] = point[1] = 0; matrix.mapPoints(point); iconTops[index] = point[1]; original.draw(canvas);
                        }
                        @Override public void setAlpha(int alpha) { original.setAlpha(alpha); }
                        @Override public void setColorFilter(android.graphics.ColorFilter filter) { original.setColorFilter(filter); }
                        @Override public int getOpacity() { return android.graphics.PixelFormat.TRANSLUCENT; }
                    });
                }
                java.util.Set<Float> textBaselines = new java.util.TreeSet<>();
                Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
                root.draw(new Canvas(image) {
                    final android.graphics.Matrix matrix = new android.graphics.Matrix();
                    final float[] point = new float[2];
                    private void baseline(float x, float y) {
                        getMatrix(matrix); point[0] = x; point[1] = y; matrix.mapPoints(point); textBaselines.add(point[1]);
                    }
                    @Override public void drawTextRun(char[] text, int index, int count, int contextIndex, int contextCount, float x, float y, boolean rtl, Paint paint) {
                        baseline(x, y); super.drawTextRun(text, index, count, contextIndex, contextCount, x, y, rtl, paint);
                    }
                    @Override public void drawTextRun(CharSequence text, int start, int end, int contextStart, int contextEnd, float x, float y, boolean rtl, Paint paint) {
                        baseline(x, y); super.drawTextRun(text, start, end, contextStart, contextEnd, x, y, rtl, paint);
                    }
                    @Override public void drawText(String text, float x, float y, Paint paint) {
                        baseline(x, y); super.drawText(text, x, y, paint);
                    }
                    @Override public void drawText(CharSequence text, int start, int end, float x, float y, Paint paint) {
                        baseline(x, y); super.drawText(text, start, end, x, y, paint);
                    }
                }); image.recycle();
                for (int i = 0; i < expected.length(); i++) {
                    JSONObject web = expected.getJSONObject(i); TextView row = (TextView) browser.list.getChildAt(i);
                    boolean orderMatches = web.getString("name").equals(row.getText().toString());
                    if (!orderMatches) orderFailures++;
                    int y = top(row, root), baseline = y + row.getBaseline();
                    java.util.List<Float> painted = new java.util.ArrayList<>();
                    for (float at : textBaselines) if (at >= y && at < y + row.getHeight()) painted.add(at);
                    assertEquals("Exactly one actual native paint baseline per row: " + row.getText(), 1, painted.size());
                    double webTop = web.getJSONObject("row").getDouble("y") * density;
                    double webBottom = webTop + web.getJSONObject("row").getDouble("height") * density;
                    double webBaseline = web.getJSONArray("baselines").getDouble(0) * density;
                    double positionDelta = Math.max(Math.abs(y - webTop), Math.abs(y + row.getHeight() - webBottom));
                    double baselineDelta = Math.max(Math.abs(baseline - webBaseline), Math.abs(painted.get(0) - webBaseline));
                    assertTrue("The production leading drawable must be painted", Float.isFinite(iconTops[i]));
                    double iconDelta = Math.abs(iconTops[i] - web.getJSONObject("icon").getDouble("y") * density);
                    iconMaximum = Math.max(iconMaximum, iconDelta);
                    positionMaximum = Math.max(positionMaximum, positionDelta); baselineMaximum = Math.max(baselineMaximum, baselineDelta);
                    if (positionDelta > 1 || baselineDelta > 1 || iconDelta > 1 || !orderMatches) failures++;
                    samples.put(new JSONObject().put("density", density).put("locale", scene.getString("locale")).put("theme", scene.getString("theme"))
                            .put("width", width).put("name", row.getText()).put("y", y).put("height", row.getHeight()).put("baseline", baseline).put("paintBaseline", painted.get(0))
                            .put("webTop", webTop).put("webBottom", webBottom).put("webBaseline", webBaseline)
                            .put("positionDelta", positionDelta).put("baselineDelta", baselineDelta).put("iconDelta", iconDelta).put("iconY", iconTops[i])
                            .put("expectedName", web.getString("name")).put("orderMatches", orderMatches));
                }
                browser.deactivate();
            }
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-file-rows-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject()
                .put("purpose", "Actual ListView row frames and native text baseline versus actual DOM/CDP paints; not full raster or device parity")
                .put("samples", samples).put("failures", failures).put("orderFailures", orderFailures).put("positionMaximum", positionMaximum).put("baselineMaximum", baselineMaximum).put("iconMaximum", iconMaximum).toString(2));
        assertEquals("Actual file row frames and absolute filename baselines must remain within one physical pixel without cumulative drift", 0, failures);
    }
}
