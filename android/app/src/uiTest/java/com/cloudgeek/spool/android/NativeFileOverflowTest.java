package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.view.ViewGroup;
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

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeFileOverflowTest {
    private JSONObject reference() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-file-overflow.json")) {
            assertNotNull(input);
            return new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
        }
    }

    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void productionRowsPreserveTheNameInsteadOfPlatformEndEllipsizing() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); NativeFileModel state = new NativeFileModel("overflow");
            String name = "NativeWorkspaceConfiguration.android.test.ts";
            state.accept(".", new JSONObject().put("entries", new JSONArray().put(new JSONObject().put("name", name).put("path", name).put("kind", "text"))));
            NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null,
                    (path, done) -> fail("Filename layout must not read files"), file -> {}, url -> {}, () -> {}, value -> {});
            controller.get().setContentView(browser);
            TextView row = (TextView) browser.list.getAdapter().getView(0, null, browser.list);
            layout(row, 100, 30);
            Bitmap image = Bitmap.createBitmap(100, 30, Bitmap.Config.ARGB_8888); row.draw(new Canvas(image));
            Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
            String stem = "native-file-overflow-api" + android.os.Build.VERSION.SDK_INT;
            try (java.io.OutputStream stream = Files.newOutputStream(output.resolve(stem + ".png"))) {
                assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
            } finally { image.recycle(); }
            Files.writeString(output.resolve(stem + ".json"), new JSONObject().put("name", row.getText().toString())
                    .put("platformEllipsize", String.valueOf(row.getEllipsize())).put("nativeEllipsisCount", row.getLayout().getEllipsisCount(0)).toString(2));
            assertEquals(name, row.getText().toString()); assertEquals(name, row.getContentDescription());
            assertNull("Actual Web splits/clips whole segments; Android END/MIDDLE cannot reproduce it", row.getEllipsize());
            browser.deactivate();
        }
    }

    @Test public void segmentSplitsWidthsAndMarkersFollowTheActualWebAtAllDensities() throws Exception {
        JSONArray cases = reference().getJSONArray("cases");
        JSONArray failures = new JSONArray(); int samples = 0; double maximum = 0;
        for (int c = 0; c < cases.length(); c++) {
            JSONObject scene = cases.getJSONObject(c); float scale = (float) scene.getDouble("density");
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + (scale == 1 ? "mdpi" : scale == 3 ? "xxhdpi" : "420dpi"));
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), scene.getString("theme").equals("dark"), scene.getString("locale"));
                ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeFileModel state = new NativeFileModel("overflow");
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Files", state, null,
                        (path, done) -> fail("Filename layout must not read files"), file -> {}, url -> {}, () -> {}, value -> {});
                controller.get().setContentView(browser);
                View recycled = null;
                for (int r = 0; r < scene.getJSONArray("rows").length(); r++) {
                    JSONObject expected = scene.getJSONArray("rows").getJSONObject(r); String name = expected.getString("name");
                    state.accept(".", new JSONObject().put("entries", new JSONArray().put(new JSONObject().put("name", name).put("path", name).put("kind", "text"))));
                    browser.renderTree();
                    View next = browser.list.getAdapter().getView(0, recycled, browser.list);
                    if (recycled != null) assertSame(recycled, next);
                    recycled = next; assertTrue(next instanceof NativeMiddleTruncate);
                    NativeMiddleTruncate row = (NativeMiddleTruncate) next;
                    layout(row, Math.round((float) expected.getDouble("available") * scale) + row.getCompoundPaddingLeft() + row.getCompoundPaddingRight(), ui.dp(30));
                    assertEquals(name, row.getText().toString()); assertEquals(name, row.getContentDescription());
                    assertNull(row.getEllipsize()); assertEquals(0, row.getLayout().getEllipsisCount(0));
                    java.util.List<NativeMiddleTruncate.Segment> segments = row.overflowSegments();
                    JSONArray reference = expected.getJSONArray("segments"); assertEquals(name, reference.length(), segments.size());
                    for (int s = 0; s < reference.length(); s++) {
                        JSONObject web = reference.getJSONObject(s); NativeMiddleTruncate.Segment nativeSegment = segments.get(s);
                        assertEquals(name, web.getString("text"), nativeSegment.text.toString());
                        assertEquals(name, web.getString("mode").equals("fruncate"), nativeSegment.fromEnd);
                        double x = (web.getJSONObject("box").getDouble("x") - expected.getJSONObject("content").getDouble("x")) * scale;
                        double ink = (web.getJSONObject("ink").getDouble("x") - web.getJSONObject("box").getDouble("x")) * scale;
                        double delta = Math.max(Math.max(Math.abs(nativeSegment.x - x), Math.abs(nativeSegment.width - web.getJSONObject("box").getDouble("width") * scale)),
                                Math.max(Math.abs(nativeSegment.natural - web.getJSONObject("ink").getDouble("width") * scale),
                                        Math.max(Math.abs((nativeSegment.fromEnd ? nativeSegment.width - nativeSegment.natural : 0) - ink),
                                                Math.abs(nativeSegment.markerWidth - web.getJSONObject("marker").getDouble("width") * scale))));
                        maximum = Math.max(maximum, delta);
                        boolean marker = web.getJSONObject("marker").getDouble("opacity") == 1;
                        if (delta > 1 || marker != nativeSegment.markerVisible)
                            failures.put(new JSONObject().put("scene", scene.getString("id")).put("density", scale).put("locale", scene.getString("locale"))
                                    .put("name", name).put("segment", s).put("delta", delta).put("marker", nativeSegment.markerVisible).put("expectedMarker", marker));
                    }
                    assertEquals(1, row.getScaleX(), 0); assertEquals(1, row.getScaleY(), 0);
                    samples++;
                }
                browser.deactivate();
            }
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-file-overflow-geometry-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject()
                .put("purpose", "Actual adapter segment geometry/text/steady marker visibility versus Web, not full glyph/fade raster or device parity")
                .put("samples", samples).put("maximum", maximum).put("failures", failures).toString(2));
        assertEquals("Actual Web filename splits, segment clips and marker visibility must match at <=1 physical pixel", 0, failures.length());
    }

    private void finishMarkers(View view) {
        if (view instanceof NativeMiddleTruncate) for (Object marker : (Object[]) ReflectionHelpers.getField(view, "markers")) {
            android.animation.ValueAnimator animation = ReflectionHelpers.getField(marker, "animation");
            if (animation != null) animation.end();
        }
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) finishMarkers(((ViewGroup) view).getChildAt(i));
    }

    @Test public void fullProductionBrowserHasIndependentNarrowViewportImages() throws Exception {
        RuntimeEnvironment.setQualifiers("w220dp-h800dp-420dpi");
        JSONArray cases = reference().getJSONArray("cases");
        for (boolean dark : new boolean[] {false, true}) {
            JSONObject scene = null;
            for (int i = 0; i < cases.length(); i++) {
                JSONObject candidate = cases.getJSONObject(i);
                if (candidate.getDouble("density") == 2.625 && candidate.getString("locale").equals("zh-CN")
                        && candidate.getString("theme").equals(dark ? "dark" : "light") && candidate.getString("id").equals("narrow")) scene = candidate;
            }
            assertNotNull(scene);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, "zh-CN"); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                JSONArray entries = new JSONArray();
                for (int i = 0; i < scene.getJSONArray("rows").length(); i++) {
                    String name = scene.getJSONArray("rows").getJSONObject(i).getString("name");
                    entries.put(new JSONObject().put("name", name).put("path", name).put("kind", "text"));
                }
                NativeFileModel state = new NativeFileModel("overflow"); state.accept(".", new JSONObject().put("entries", entries));
                NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", state, null,
                        (path, done) -> fail("Fixture already contains all files"), file -> {}, url -> {}, () -> {}, value -> {});
                FrameLayout root = new FrameLayout(ui.context); root.setBackgroundColor(ui.surface); root.setFocusableInTouchMode(true);
                root.addView(browser, new FrameLayout.LayoutParams(Math.min(ui.dp(360), Math.round(ui.dp(220) * .92f)), -1, android.view.Gravity.RIGHT));
                controller.get().setContentView(root); root.requestFocus(); layout(root, ui.dp(220), ui.dp(800));
                Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
                root.draw(new Canvas(image)); finishMarkers(root); root.draw(new Canvas(image));
                Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
                try (java.io.OutputStream stream = Files.newOutputStream(output.resolve("native-file-overflow-browser-api" + android.os.Build.VERSION.SDK_INT + (dark ? "-dark" : "-light") + ".png"))) {
                    assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                } finally { image.recycle(); }
                browser.deactivate();
            }
        }
    }

    @Test public void markerFadeUsesActualWebTimingAndCancelsAfterReuseOrDetach() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        JSONObject timing = reference().getJSONObject("markerTiming");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); NativeTreeRow row = new NativeTreeRow(ui);
            ui.typography(row, 13, ui.text, 400); row.setSingleLine(true); row.setText("NativeWorkspaceConfiguration.android.test.ts");
            controller.get().setContentView(row); layout(row, 100, 30);
            Bitmap image = Bitmap.createBitmap(100, 30, Bitmap.Config.ARGB_8888); row.draw(new Canvas(image)); image.recycle();
            Object marker = ((Object[]) ReflectionHelpers.getField(row, "markers"))[0];
            android.animation.ValueAnimator animation = ReflectionHelpers.getField(marker, "animation"); assertNotNull(animation);
            assertEquals(timing.getJSONObject("timing").getLong("duration"), animation.getDuration());
            JSONArray samples = new JSONArray(); double maximum = 0;
            for (int i = 0; i < timing.getJSONArray("samples").length(); i++) {
                JSONObject sample = timing.getJSONArray("samples").getJSONObject(i); animation.setCurrentPlayTime(sample.getLong("time"));
                float alpha = ReflectionHelpers.getField(marker, "alpha"); maximum = Math.max(maximum, Math.abs(sample.getDouble("opacity") - alpha));
                samples.put(new JSONObject().put("time", sample.getLong("time")).put("alpha", alpha).put("expected", sample.getDouble("opacity"))
                        .put("directInterpolator", animation.getInterpolator().getInterpolation(sample.getLong("time") / 100f)));
                if (sample.getLong("time") == 50) {
                    Bitmap frame = Bitmap.createBitmap(100, 30, Bitmap.Config.ARGB_8888);
                    int[] gradients = {0};
                    row.draw(new Canvas(frame) {
                        @Override public void drawRect(float left, float top, float right, float bottom, android.graphics.Paint paint) {
                            if (paint.getShader() != null) {
                                assertEquals("Cached background shaders receive marker opacity exactly once", 128, paint.getAlpha()); gradients[0]++;
                            }
                            super.drawRect(left, top, right, bottom, paint);
                        }
                    });
                    frame.recycle(); assertEquals(2, gradients[0]);
                }
            }
            layout(row, 1000, 30); row.overflowSegments();
            assertEquals(0, (float) ReflectionHelpers.getField(marker, "alpha"), 0); assertNull(ReflectionHelpers.getField(marker, "animation"));
            layout(row, 100, 30); row.overflowSegments(); assertNotNull(ReflectionHelpers.getField(marker, "animation"));
            controller.get().setContentView(new View(ui.context));
            assertEquals(0, (float) ReflectionHelpers.getField(marker, "alpha"), 0); assertNull(ReflectionHelpers.getField(marker, "animation"));
            Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
            Files.writeString(output.resolve("native-file-overflow-motion-api" + android.os.Build.VERSION.SDK_INT + ".json"),
                    new JSONObject().put("samples", samples).put("maximum", maximum).toString(2));
            assertTrue("CSS marker opacity timing must match within .001 without changing duration", maximum <= .001);
        }
    }
}
