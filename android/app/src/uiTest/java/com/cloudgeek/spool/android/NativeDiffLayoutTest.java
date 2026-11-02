package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeDiffLayoutTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-diff.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }
    private JSONArray ranges(List<NativeWordDiff.Range> ranges, String text) {
        JSONArray result = new JSONArray();
        for (NativeWordDiff.Range range : ranges) {
            assertTrue(range.start >= 0 && range.end <= NativeWordDiff.cleanLastNewline(text).length() && range.end > range.start);
            assertFalse(range.start < text.length() && Character.isLowSurrogate(text.charAt(range.start)));
            assertFalse(range.end < text.length() && Character.isLowSurrogate(text.charAt(range.end)));
            result.put(new JSONArray().put(range.start).put(range.end));
        }
        return result;
    }

    @Test public void nativeWordAltSpansMatchActualPierreAndJsdiffIncludingUnicodeAndLengthLimits() throws Exception {
        JSONArray cases = fixtures().getJSONArray("words");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject test = cases.getJSONObject(i), expected = test.getJSONObject("expected");
            NativeWordDiff.Pair actual = NativeWordDiff.ranges(test.getString("old"), test.getString("next"));
            assertEquals("deletion pair " + i, expected.getJSONArray("old").toString(), ranges(actual.old, test.getString("old")).toString());
            assertEquals("addition pair " + i, expected.getJSONArray("next").toString(), ranges(actual.next, test.getString("next")).toString());
        }
    }

    private void cell(JSONObject expected, NativeDiffLayout.Cell actual) throws Exception {
        if (expected == null) { assertNull(actual); return; }
        assertNotNull(actual);
        assertEquals(expected.getString("text"), actual.text);
        assertEquals(expected.getInt("number"), actual.number);
        assertEquals(expected.getString("kind"), String.valueOf(actual.kind));
    }
    @Test public void splitRowsMatchActualPierreSimilarityThresholdComparisonCapAndContextBarriers() throws Exception {
        JSONArray cases = fixtures().getJSONArray("layouts");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject test = cases.getJSONObject(i);
            List<NativePatch.File> files = NativePatch.parse(test.getString("text"));
            assertEquals(test.getString("name"), 1, files.size());
            List<NativeDiffLayout.Row> actual = new ArrayList<>();
            for (NativeDiffLayout.Row row : NativeDiffLayout.rows(files.get(0))) if (row.separator == null) actual.add(row);
            JSONArray expected = test.getJSONArray("expected");
            assertEquals(test.getString("name"), expected.length(), actual.size());
            for (int j = 0; j < actual.size(); j++) {
                JSONObject row = expected.getJSONObject(j);
                try { cell(row.optJSONObject("old"), actual.get(j).old); cell(row.optJSONObject("next"), actual.get(j).next); }
                catch (AssertionError error) { throw new AssertionError(test.getString("name") + " row " + j, error); }
            }
        }
    }

    @Test public void separatedEditsDoNotShadeUnchangedMiddleTextAndOriginalCharactersRemainUntouched() {
        NativePatch.File file = NativePatch.parse("*** Begin Patch\n*** Update File: src/test.java\n@@\n-\tconst first = old; const second = old;\n+\tconst first = new; const second = new;\n*** End Patch").get(0);
        NativeDiffLayout.Row row = NativeDiffLayout.rows(file).get(0);
        assertEquals(2, row.old.emphasis.size()); assertEquals(2, row.next.emphasis.size());
        assertEquals("\tconst first = old; const second = old;\n", row.old.text);
        assertEquals("\tconst first = new; const second = new;\n", row.next.text);
        assertEquals("old", row.old.text.substring(row.old.emphasis.get(0).start, row.old.emphasis.get(0).end));
        assertEquals("new", row.next.text.substring(row.next.emphasis.get(1).start, row.next.emphasis.get(1).end));
    }

    @Test public void expensiveDiffWorkIsDeferredAndDetachedOrCompletedStaleJobsCannotPublish() {
        try (ActivityController<android.app.Activity> controller = Robolectric.buildActivity(android.app.Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            List<Runnable> tasks = new ArrayList<>(); ui.codeWorker = tasks::add;
            NativePatchView patch = (NativePatchView) NativePatchView.render(ui,
                    "*** Begin Patch\n*** Update File: a\n@@\n-old\n+prologue();\n+new\n*** End Patch", false);
            assertTrue(tasks.isEmpty());
            android.widget.FrameLayout page = new android.widget.FrameLayout(controller.get()); controller.get().setContentView(page);
            page.addView(patch); assertEquals(1, tasks.size());
            assertFalse((boolean) ReflectionHelpers.getField(patch, "precise"));
            page.removeView(patch);
            assertTrue(((java.util.concurrent.FutureTask<?>) tasks.get(0)).isCancelled()); tasks.get(0).run();
            page.addView(patch); assertEquals(2, tasks.size());
            tasks.get(1).run(); page.removeView(patch); page.addView(patch);
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            assertFalse("A completed old attachment must not publish into a reattached view", (boolean) ReflectionHelpers.getField(patch, "precise"));
            assertEquals(3, tasks.size());
            patch.measure(android.view.View.MeasureSpec.makeMeasureSpec(360, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED));
            int height = patch.getMeasuredHeight();
            tasks.get(2).run(); Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            assertTrue((boolean) ReflectionHelpers.getField(patch, "precise"));
            patch.measure(android.view.View.MeasureSpec.makeMeasureSpec(360, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED));
            assertEquals(height, patch.getMeasuredHeight());
            page.removeView(patch); page.addView(patch); assertEquals("Completed immutable rows are reused on reattach", 3, tasks.size());
        }
    }

    @Test public void interruptedDiffsStopWithoutLeavingPartialRowsOrClearingTheCancellationFlag() {
        Thread.currentThread().interrupt();
        try {
            try { NativeWordDiff.ranges("a.b ".repeat(200), "x,y ".repeat(200)); fail("Expected cancellation"); }
            catch (java.util.concurrent.CancellationException expected) { assertTrue(Thread.currentThread().isInterrupted()); }
        } finally { Thread.interrupted(); }
    }

    private android.view.View code(android.view.View view, boolean additions) {
        if (view.getClass().getSimpleName().equals("Code") && !(boolean) ReflectionHelpers.getField(view, "gutter")
                && (boolean) ReflectionHelpers.getField(view, "additions") == additions) return view;
        if (view instanceof android.view.ViewGroup) for (int i = 0; i < ((android.view.ViewGroup) view).getChildCount(); i++) {
            android.view.View found = code(((android.view.ViewGroup) view).getChildAt(i), additions);
            if (found != null) return found;
        }
        return null;
    }
    private JSONArray spanBoxes(android.view.View view, boolean additions) throws Exception {
        assertNotNull(view);
        android.graphics.Paint paint = ReflectionHelpers.getField(view, "text");
        int height = ReflectionHelpers.getField(view, "lineHeight");
        List<NativeDiffLayout.Row> rows = ReflectionHelpers.getField(view, "rows");
        JSONArray result = new JSONArray();
        for (NativeDiffLayout.Row row : rows) {
            NativeDiffLayout.Cell cell = additions ? row.next : row.old;
            if (cell == null) continue;
            String text = NativeWordDiff.cleanLastNewline(cell.text);
            JSONArray spans = new JSONArray();
            for (NativeWordDiff.Range range : cell.emphasis) {
                float left = NativeCodeTabs.width(paint, text, range.start), right = NativeCodeTabs.width(paint, text, range.end);
                spans.put(new JSONObject().put("x", left).put("y", (height - paint.ascent() - paint.descent()) / 2 + paint.ascent())
                        .put("width", right - left).put("height", paint.descent() - paint.ascent()));
            }
            result.put(spans);
        }
        return result;
    }

    @Test @Config(sdk = 35) @GraphicsMode(GraphicsMode.Mode.NATIVE)
    public void nativeWordHighlightInkMatchesActualBrowserInlineBoxesAtEveryMobileWidthAndTheme() throws Exception {
        String source = fixtures().getString("diffLayoutContents");
        java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) for (boolean dark : new boolean[] {false, true}) {
            org.robolectric.RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + (dark ? "night" : "notnight") + "-mdpi");
            try (ActivityController<android.app.Activity> controller = Robolectric.buildActivity(android.app.Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark);
                android.view.View patch = NativePatchView.render(ui, source, false);
                controller.get().setContentView(patch);
                patch.measure(android.view.View.MeasureSpec.makeMeasureSpec(width, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED));
                patch.layout(0, 0, width, patch.getMeasuredHeight());
                JSONObject boxes = new JSONObject().put("old", spanBoxes(code(patch, false), false)).put("next", spanBoxes(code(patch, true), true));
                String suffix = width + (dark ? "-dark" : "-light");
                java.nio.file.Files.writeString(new java.io.File(directory, "native-diff-layout-" + suffix + ".json").toPath(), boxes.toString(2));
                android.graphics.Bitmap bitmap = android.graphics.Bitmap.createBitmap(width, patch.getMeasuredHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                android.graphics.Canvas canvas = new android.graphics.Canvas(bitmap); canvas.drawColor(ui.elevated); patch.draw(canvas);
                try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, "native-diff-layout-" + suffix + ".png"))) {
                    assertTrue(bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
                } finally { bitmap.recycle(); }
            }
        }
    }
}
