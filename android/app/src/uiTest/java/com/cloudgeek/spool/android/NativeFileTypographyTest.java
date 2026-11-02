package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.text.Spanned;
import android.text.TextPaint;
import android.view.View;
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

/** Production file rows must share CSS font synthesis rather than invent CJK bold. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeFileTypographyTest {
    private JSONArray reference() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-fonts.json")) {
            assertNotNull(input);
            return new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("cases");
        }
    }

    private JSONObject fixture(JSONArray cases, String locale, float density, boolean dark) throws Exception {
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i);
            if (item.getString("locale").equals(locale) && item.getDouble("density") == density && item.getString("theme").equals(dark ? "dark" : "light")) return item;
        }
        throw new AssertionError("Missing actual Web file-font fixture");
    }

    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void actualFileRowsMatchRealConfiguredCjkFacesDuringSearchAndReuse() throws Exception {
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        JSONArray samples = new JSONArray(), cases = reference(); int outside = 0, weights = 0;
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            for (String locale : new String[] {"zh-CN", "ja"}) for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark, locale); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                    float scale = ui.context.getResources().getDisplayMetrics().density;
                    JSONObject fixture = fixture(cases, locale, scale, dark);
                    String name = fixture.getString("name");
                    NativeFileModel state = new NativeFileModel("fonts");
                    state.accept(".", new JSONObject().put("entries", new JSONArray().put(new JSONObject().put("name", name).put("path", name).put("kind", "text"))));
                    NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null, (path, done) -> fail("No file reads in font controls"), file -> {}, address -> {}, () -> {}, value -> {});
                    controller.get().setContentView(browser);
                    TextView row = null; int phase = 0;
                    for (boolean match : new boolean[] {false, true, false}) {
                        state.search(match ? name : ""); browser.renderTree();
                        TextView next = (TextView) browser.list.getAdapter().getView(0, row, browser.list);
                        if (row != null) assertSame("The actual ListView row must be reused", row, next);
                        row = next; assertEquals(name, row.getText().toString()); assertEquals(name, row.getContentDescription());
                        int width = ui.dp(220), height = ui.dp(30); layout(row, width, height);
                        JSONObject style = fixture.getJSONArray("records").getJSONObject(phase++);
                        int expectedWeight = Integer.parseInt(style.getString("weight"));
                        if (((NativeTextView) row).fontWeight != expectedWeight) weights++;
                        assertEquals(style.getDouble("size") * scale, row.getTextSize(), .0001);
                        assertEquals(style.getJSONObject("row").getDouble("height") * scale, row.getHeight(), 1);
                        Bitmap actual = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                        Bitmap expected = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                        row.draw(new Canvas(actual));
                        int baseline = row.getBaseline(), left = row.getTotalPaddingLeft();
                        Paint real = new Paint(row.getPaint()); real.setTypeface(ui.cjkFonts.face(locale, 400)); real.setFakeBoldText(false); real.setTextSkewX(0);
                        // Keep production icon/background paint; replace only the glyph control.
                        row.setText(""); row.draw(new Canvas(expected));
                        new Canvas(expected).drawTextRun(name, 0, name.length(), 0, name.length(), left, baseline, false, real);
                        int maximum = 0, count = 0;
                        for (int y = 0; y < height; y++) for (int x = 0; x < width; x++) {
                            int a = actual.getPixel(x, y), b = expected.getPixel(x, y), delta = 0;
                            for (int shift : new int[] {0, 8, 16, 24}) delta = Math.max(delta, Math.abs((a >>> shift & 255) - (b >>> shift & 255)));
                            maximum = Math.max(maximum, delta); if (delta > 1) count++;
                        }
                        outside += count;
                        samples.put(new JSONObject().put("density", density).put("locale", locale).put("dark", dark).put("match", match)
                                .put("weight", ((NativeTextView) row).fontWeight).put("expectedWeight", expectedWeight).put("maximum", maximum).put("outsideTolerance", count));
                        if (match && density.equals("420dpi")) for (boolean control : new boolean[] {false, true}) {
                            try (java.io.OutputStream stream = Files.newOutputStream(output.resolve("native-file-font-api" + android.os.Build.VERSION.SDK_INT + "-" + locale + (dark ? "-dark" : "-light") + (control ? "-real-face" : "") + ".png"))) {
                                assertTrue((control ? expected : actual).compress(Bitmap.CompressFormat.PNG, 100, stream));
                            }
                        }
                        actual.recycle(); expected.recycle();
                    }
                    browser.deactivate();
                }
            }
        }
        Files.writeString(output.resolve("native-file-font-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject()
                .put("purpose", "Actual Web filename style plus independent native real-font glyph control, not Web/device raster parity")
                .put("cases", samples).put("outsideTolerance", outside).put("weightFailures", weights).toString(2));
        assertEquals("File search rows must not synthesize an unavailable CJK weight", 0, outside);
        assertEquals("Use actual Web requested weights, not unused search-match CSS", 0, weights);
    }

    @Test public void recycledFileRowsKeepLatinWeightsAndDiscardOldFallbackSpans() throws Exception {
        JSONArray cases = reference();
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            for (String locale : new String[] {"zh-CN", "ja"}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), false, locale); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                    JSONObject fixture = fixture(cases, locale, ui.context.getResources().getDisplayMetrics().density, false);
                    NativeFileModel state = new NativeFileModel("fonts");
                    NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null, (path, done) -> fail("No file reads in row reuse checks"), file -> {}, address -> {}, () -> {}, value -> {});
                    controller.get().setContentView(browser);
                    TextView row = null;
                    for (String name : new String[] {"共享项目", "readme.md", "共有項目", "Latin 共享.md", "plain.txt"}) for (boolean match : new boolean[] {true, false}) {
                        state.accept(".", new JSONObject().put("entries", new JSONArray().put(new JSONObject().put("name", name).put("path", name).put("kind", "text"))));
                        state.search(match ? name : ""); browser.renderTree();
                        TextView next = (TextView) browser.list.getAdapter().getView(0, row, browser.list);
                        if (row != null) assertSame(row, next);
                        row = next; assertTrue(row instanceof NativeTextView);
                        layout(row, ui.dp(120), ui.dp(30));
                        assertEquals(name, row.getText().toString()); assertEquals(name, row.getContentDescription());
                        int weight = Integer.parseInt(fixture.getJSONArray("records").getJSONObject(match ? 1 : 0).getString("weight"));
                        assertEquals(weight, ((NativeTextView) row).fontWeight);
                        assertEquals(ui.dp(30), row.getHeight()); assertEquals(NativeUi.staticFontWeight(weight) >= 700, row.getTypeface().isBold());
                        assertEquals(1f, row.getScaleX(), 0); assertEquals(1f, row.getScaleY(), 0);
                        Spanned document = row.getText() instanceof Spanned ? (Spanned) row.getText() : null;
                        NativeCjkFallback.FaceSpan[] faces = document == null ? new NativeCjkFallback.FaceSpan[0]
                                : document.getSpans(0, document.length(), NativeCjkFallback.FaceSpan.class);
                        assertEquals(NativeCjkFallback.containsCjk(name), faces.length > 0);
                        for (NativeCjkFallback.FaceSpan face : faces) {
                            assertTrue(NativeCjkFallback.containsCjk(document.subSequence(document.getSpanStart(face), document.getSpanEnd(face))));
                            TextPaint paint = new TextPaint(row.getPaint()); face.updateDrawState(paint);
                            assertFalse(paint.isFakeBoldText()); assertFalse(paint.getTypeface().isBold()); assertEquals(0, paint.getTextSkewX(), 0);
                        }
                        if (name.startsWith("Latin")) assertEquals(0, document.getSpans(0, 5, NativeCjkFallback.FaceSpan.class).length);
                    }
                    browser.deactivate();
                }
            }
        }
    }

    @Test public void actualWebSearchCountsIncludeItsNoMatchAndCloseBehavior() throws Exception {
        JSONArray cases = reference();
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            for (String locale : new String[] {"zh-CN", "ja"}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), false, locale); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                    JSONObject fixture = fixture(cases, locale, ui.context.getResources().getDisplayMetrics().density, false);
                    String name = fixture.getString("name");
                    NativeFileModel state = new NativeFileModel("fonts");
                    state.accept(".", new JSONObject().put("entries", new JSONArray()
                            .put(new JSONObject().put("name", name).put("path", name).put("kind", "text"))
                            .put(new JSONObject().put("name", "plain.txt").put("path", "plain.txt").put("kind", "text"))));
                    NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null, (path, done) -> fail("Search must not fetch files"), file -> {}, address -> {}, () -> {}, value -> {});
                    controller.get().setContentView(browser); layout(browser, ui.dp(331), ui.dp(800));
                    assertEquals(fixture.getJSONArray("records").getJSONObject(0).getInt("rowCount"), browser.list.getAdapter().getCount());
                    browser.toggleSearch(true); browser.search.setText("__spool_font_probe_no_match__");
                    JSONArray noMatch = fixture.getJSONArray("noMatchPaths");
                    assertEquals(noMatch.length(), browser.list.getAdapter().getCount());
                    for (int i = 0; i < noMatch.length(); i++) assertEquals(noMatch.getString(i), ((NativeFileModel.Row) browser.list.getAdapter().getItem(i)).entry.path);
                    browser.search.setText(name);
                    assertEquals(fixture.getJSONArray("records").getJSONObject(1).getInt("rowCount"), browser.list.getAdapter().getCount());
                    assertEquals(name, ((NativeFileModel.Row) browser.list.getAdapter().getItem(0)).entry.path);
                    browser.toggleSearch(false);
                    assertEquals(fixture.getJSONArray("records").getJSONObject(2).getInt("rowCount"), browser.list.getAdapter().getCount());
                    assertEquals("", state.query); assertEquals(View.GONE, browser.search.getVisibility()); browser.deactivate();
                }
            }
        }
    }
}
