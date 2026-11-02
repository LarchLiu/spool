package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.os.Looper;
import android.text.Selection;
import android.text.Spannable;
import android.text.Spanned;
import android.text.style.ForegroundColorSpan;
import android.text.style.StrikethroughSpan;
import android.text.style.StyleSpan;
import android.text.style.TabStopSpan;
import android.text.style.UnderlineSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.CancellationException;
import java.util.concurrent.FutureTask;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeSyntaxTest {
    private static List<NativeSyntaxEngine.Line> tokens(String text) {
        List<NativeSyntaxEngine.Line> lines = new ArrayList<>();
        for (int from = 0; from <= text.length();) {
            int end = text.indexOf('\n', from); if (end < 0) end = text.length();
            lines.add(new NativeSyntaxEngine.Line(from, end - from, end == from ? Collections.emptyList()
                    : List.of(new NativeSyntaxEngine.Run(0, end - from, 0xff20649e, 15))));
            if (end == text.length()) break; from = end + 1;
        }
        return lines;
    }
    private static void measure(View view) {
        view.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        view.layout(0, 0, 360, view.getMeasuredHeight());
    }
    private static <T> T child(View view, Class<T> type) {
        if (type.isInstance(view)) return type.cast(view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            T result = child(((ViewGroup) view).getChildAt(i), type); if (result != null) return result;
        }
        return null;
    }

    @Test public void sourceStylesKeepExactSelectionTabsLineHeightsAndDimensions() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); List<Runnable> tasks = new ArrayList<>();
            ui.syntax = new NativeSyntax(tasks::add, (path, source, dark) -> {
                assertEquals("src/example.ts", path); assertFalse(dark); return tokens(source);
            }, () -> {});
            String text = "\tconst rocket = '\ud83d\ude80';\n\treturn rocket;\n";
            NativeFileSource source = new NativeFileSource(ui, "src/example.ts", text, 2);
            assertTrue(tasks.isEmpty()); FrameLayout page = new FrameLayout(controller.get()); controller.get().setContentView(page); page.addView(source);
            assertEquals(1, tasks.size()); measure(source);
            int height = source.getMeasuredHeight(), lineHeight = source.getLineHeight(), lines = source.getLineCount();
            Spannable selectable = (Spannable) source.getText(); Selection.setSelection(selectable, 1, text.length() - 1);
            TabStopSpan[] stops = selectable.getSpans(0, text.length(), TabStopSpan.class); assertTrue(stops.length > 0);
            tasks.get(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); measure(source);
            assertSame(selectable, source.getText()); assertEquals(text, source.getText().toString()); assertTrue(source.isTextSelectable());
            assertEquals(1, source.getSelectionStart()); assertEquals(text.length() - 1, source.getSelectionEnd());
            assertArrayEquals(stops, selectable.getSpans(0, text.length(), TabStopSpan.class));
            assertEquals(height, source.getMeasuredHeight()); assertEquals(lineHeight, source.getLineHeight()); assertEquals(lines, source.getLineCount());
            assertEquals(2, selectable.getSpans(0, text.length(), ForegroundColorSpan.class).length);
            assertEquals(Typeface.BOLD_ITALIC, selectable.getSpans(0, text.length(), StyleSpan.class)[0].getStyle());
            assertEquals(2, selectable.getSpans(0, text.length(), UnderlineSpan.class).length);
            assertEquals(2, selectable.getSpans(0, text.length(), StrikethroughSpan.class).length);
        }
    }

    @Test public void detachCancelsAndCompletedOldAttachmentsCannotPublishIntoAReattachedView() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            List<Runnable> tasks = new ArrayList<>(); AtomicInteger applied = new AtomicInteger();
            NativeSyntax syntax = new NativeSyntax(tasks::add, (path, source, dark) -> tokens(source), () -> {});
            View view = new View(controller.get()); syntax.bind(view, "test.go", "value", false, result -> applied.incrementAndGet());
            FrameLayout page = new FrameLayout(controller.get()); controller.get().setContentView(page); page.addView(view);
            page.removeView(view); assertTrue(((FutureTask<?>) tasks.get(0)).isCancelled()); tasks.get(0).run();
            page.addView(view); tasks.get(1).run(); page.removeView(view); page.addView(view);
            Shadows.shadowOf(Looper.getMainLooper()).idle(); assertEquals(0, applied.get()); assertEquals(3, tasks.size());
            tasks.get(2).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); assertEquals(1, applied.get());
            page.removeView(view); page.addView(view); assertEquals("Retain completed spans without repeating tokenization", 3, tasks.size());
            syntax.close(); tasks.get(3).run();
        }
    }

    @Test public void sessionClearCloseAndCancellationRejectStaleWorkBeforeAndAfterComputation() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            List<Runnable> tasks = new ArrayList<>(); AtomicInteger applied = new AtomicInteger(), cleaned = new AtomicInteger(), computed = new AtomicInteger();
            NativeSyntax syntax = new NativeSyntax(tasks::add, (path, source, dark) -> { computed.incrementAndGet(); return tokens(source); }, cleaned::incrementAndGet);
            View view = new View(controller.get()); controller.get().setContentView(view);
            syntax.bind(view, "one.ts", "one", false, result -> applied.incrementAndGet());
            tasks.get(0).run(); syntax.clear(); Shadows.shadowOf(Looper.getMainLooper()).idle(); assertEquals(0, applied.get());
            tasks.get(1).run(); assertEquals(1, cleaned.get());
            int stale = syntax.epoch(); syntax.bind(view, "two.ts", "two", false, result -> applied.incrementAndGet()); syntax.close();
            tasks.get(2).run(); tasks.get(3).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, computed.get()); assertEquals(2, cleaned.get()); assertEquals(0, applied.get());
            assertThrows(CancellationException.class, () -> syntax.compute("three.ts", "three", false, stale));
            syntax.close(); syntax.clear(); assertEquals(4, tasks.size());
            NativeSyntax[] inFlight = new NativeSyntax[1];
            inFlight[0] = new NativeSyntax(tasks::add, (path, source, dark) -> { inFlight[0].clear(); return tokens(source); }, () -> {});
            assertThrows(CancellationException.class, () -> inFlight[0].compute("test.go", "code", false, inFlight[0].epoch()));
            Thread.currentThread().interrupt();
            try { assertThrows(CancellationException.class, () -> inFlight[0].compute("test.go", "code", false, inFlight[0].epoch())); }
            finally { Thread.interrupted(); }
        }
    }

    @Test public void unavailableSyntaxKeepsPlainSourceAndPreviewUsesTheServerReturnedFilename() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), true); List<Runnable> tasks = new ArrayList<>();
            List<String> paths = new ArrayList<>();
            ui.syntax = new NativeSyntax(tasks::add, (path, source, dark) -> { paths.add(path); assertTrue(dark); throw new java.io.IOException("Unavailable asset"); }, () -> {});
            NativeFilePreview preview = new NativeFilePreview(ui, "project", null, target -> {}, path -> {}, file -> {}, () -> {});
            controller.get().setContentView(preview);
            preview.bind(new JSONObject().put("kind", "text").put("path", "resolved/name.ts").put("content", "\tconst value = 1;"), new NativeFileTarget("requested.ts", 0, 0), "");
            assertEquals(1, tasks.size()); tasks.get(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(List.of("resolved/name.ts"), paths);
            NativeFileSource source = child(preview, NativeFileSource.class); assertNotNull(source);
            assertEquals("\tconst value = 1;", source.getText().toString()); assertTrue(source.isTextSelectable());
            assertEquals(0, ((Spanned) source.getText()).getSpans(0, source.length(), ForegroundColorSpan.class).length);
        }
    }

    @Test public void diffTokenizesEachHunkSideIndependentlyWithRenamedFilenamesAndAtomicRows() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); List<Runnable> tasks = new ArrayList<>(); List<String> requests = new ArrayList<>();
            ui.codeWorker = tasks::add;
            ui.syntax = new NativeSyntax(tasks::add, (path, source, dark) -> { requests.add(path + ":" + source); return tokens(source); }, () -> {});
            String patchText = "*** Begin Patch\n*** Update File: old.ts\n*** Move to: new.js\n@@\n /*\n-before\n+after\n@@ next\n-old\n+new\n*** End Patch";
            NativePatch.File file = NativePatch.parse(patchText).get(0);
            List<NativeDiffLayout.Row> rows = NativeDiffLayout.rows(file);
            ui.syntax.decorateDiff(file, rows, false, ui.syntax.epoch());
            assertEquals(List.of("old.ts:/*\nbefore\n", "new.js:/*\nafter\n", "old.ts:old\n", "new.js:new\n"), requests);
            for (NativeDiffLayout.Row row : rows) {
                if (row.old != null) assertFalse(row.old.syntax.isEmpty()); if (row.next != null) assertFalse(row.next.syntax.isEmpty());
            }
            requests.clear(); NativePatchView patch = (NativePatchView) NativePatchView.render(ui, patchText, false);
            controller.get().setContentView(patch); measure(patch); int height = patch.getMeasuredHeight();
            assertFalse((boolean) ReflectionHelpers.getField(patch, "precise")); assertEquals(1, tasks.size());
            tasks.get(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); measure(patch);
            assertTrue((boolean) ReflectionHelpers.getField(patch, "precise")); assertEquals(height, patch.getMeasuredHeight()); assertEquals(4, requests.size());
            View code = findCode(patch); assertNotNull(code);
            List<NativeDiffLayout.Row> displayed = ReflectionHelpers.getField(code, "rows");
            assertFalse(displayed.get(0).old.syntax.isEmpty());
        }
    }
    private View findCode(View view) {
        if (view.getClass().getSimpleName().equals("Code")) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) { View code = findCode(((ViewGroup) view).getChildAt(i)); if (code != null) return code; }
        return null;
    }

    @Test public void whitespaceStylesFollowPierreWithoutErasingVisibleUnderlineOrStrikeInk() {
        String text = " \tx \ty "; int base = 0xff0a0a0a;
        List<NativeSyntaxEngine.Run> runs = NativeSyntax.renderRuns(text, 0, List.of(
                new NativeSyntaxEngine.Run(0, 1, 0xff111111, 1), new NativeSyntaxEngine.Run(1, 2, 0xff222222, 0),
                new NativeSyntaxEngine.Run(2, 3, 0xff333333, 2), new NativeSyntaxEngine.Run(3, 5, 0xff444444, 0),
                new NativeSyntaxEngine.Run(5, 6, 0xff555555, 4), new NativeSyntaxEngine.Run(6, 7, 0xff666666, 8)), base);
        assertEquals(4, runs.size()); assertEquals(0, runs.get(0).start); assertEquals(3, runs.get(0).end); assertEquals(0xff333333, runs.get(0).color);
        assertEquals(3, runs.get(1).start); assertEquals(5, runs.get(1).end); assertEquals(base, runs.get(1).color); assertEquals(0, runs.get(1).style);
        assertEquals(4, runs.get(2).style); assertEquals(8, runs.get(3).style); assertEquals(0xff666666, runs.get(3).color);
    }

    @Test @Config(sdk = 35) @GraphicsMode(GraphicsMode.Mode.NATIVE)
    public void canvasColorsKeepOriginalTabGridShapingContextAndRestorePaint() {
        Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG); Typeface face = Typeface.MONOSPACE;
        paint.setTypeface(face); paint.setTextSize(13); paint.setColor(0xff123456); paint.setLinearText(true); paint.setSubpixelText(true);
        Bitmap actual = Bitmap.createBitmap(360, 60, Bitmap.Config.ARGB_8888), expected = Bitmap.createBitmap(360, 60, Bitmap.Config.ARGB_8888);
        try {
            String text = "AB\tXY\tZ";
            NativeSyntax.draw(new Canvas(actual), paint, text, List.of(new NativeSyntaxEngine.Run(0, 3, 0xff20649e, 0),
                    new NativeSyntaxEngine.Run(3, 6, 0xffa04d00, 0), new NativeSyntaxEngine.Run(6, 7, 0xff18a46c, 0)), 7, 30);
            assertSame(face, paint.getTypeface()); assertEquals(0xff123456, paint.getColor()); assertFalse(paint.isUnderlineText()); assertFalse(paint.isStrikeThruText());
            Canvas reference = new Canvas(expected); paint.setColor(0xff20649e); reference.drawText("AB", 7, 30, paint);
            paint.setColor(0xffa04d00); reference.drawText("XY", 7 + NativeCodeTabs.width(paint, text, 3), 30, paint);
            paint.setColor(0xff18a46c); reference.drawText("Z", 7 + NativeCodeTabs.width(paint, text, 6), 30, paint);
            assertTrue("Each token uses the full line's CSS two-character tab grid", actual.sameAs(expected));
            NativeSyntax.draw(new Canvas(actual), paint, text, List.of(new NativeSyntaxEngine.Run(0, text.length(), 0xff20649e, 15)), 7, 30);
            assertSame(face, paint.getTypeface()); assertEquals(0xff18a46c, paint.getColor()); assertFalse(paint.isUnderlineText()); assertFalse(paint.isStrikeThruText());
        } finally { actual.recycle(); expected.recycle(); }
    }
}
