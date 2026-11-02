package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.ScrollView;
import android.widget.TextView;
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
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeCssTextOriginTest {
    private TextView text(View view) {
        if (view instanceof TextView) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            TextView found = text(((ViewGroup) view).getChildAt(i)); if (found != null) return found;
        }
        return null;
    }

    private void layout(View view, int width, int height, int top) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, top, width, top + height);
    }

    private void settle(View view, int width, int height, int top) {
        for (int pass = 0; pass < 6; pass++) {
            if (!view.getViewTreeObserver().dispatchOnPreDraw()) return;
            layout(view, width, height, top);
        }
        fail("CSS origin changes must converge without an endless pre-draw/layout loop");
    }

    private int baseline(TextView text, int line) {
        int[] location = new int[2]; text.getLocationInWindow(location);
        return location[1] + text.getTotalPaddingTop() + text.getLayout().getLineBaseline(line);
    }

    @Test public void actualWebBaselineOriginsSurviveNestedFractionalInsetsAndAncestorMovesWithoutReplacingTheDocument() throws Exception {
        JSONObject reference;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-text-origins.json")) {
            assertNotNull(input); reference = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        JSONArray samples = new JSONArray(), cases = reference.getJSONArray("cases"); assertEquals(45, cases.length());
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h240dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false); float scale = ui.context.getResources().getDisplayMetrics().density;
                FrameLayout parent = new FrameLayout(ui.context);
                NativeMarkdownFlow flow = new NativeMarkdownFlow(ui); flow.collapseTop = flow.collapseBottom = false;
                java.util.List<String> opened = new java.util.ArrayList<>();
                View markdown = NativeMarkdown.view(ui, reference.getString("source"), false, opened::add); flow.add(markdown, 0, 0);
                parent.addView(flow, new FrameLayout.LayoutParams(-1, -2)); controller.get().setContentView(parent);
                TextView text = text(markdown); assertNotNull(text); Object document = text.getText();
                Selection.setSelection((Spannable) document, 1, 5);
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject sample = cases.getJSONObject(i); if (sample.getDouble("density") != scale) continue;
                    float padding = (float) sample.getDouble("padding"); int offset = sample.getInt("offsetPhysical");
                    flow.inset(18 * scale, padding * scale, 18 * scale, 0);
                    layout(parent, ui.dp(360), ui.dp(240), offset); settle(parent, ui.dp(360), ui.dp(240), offset);
                    assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
                    assertEquals(sample.getString("text"), text.getText().toString()); assertEquals(2, text.getLineCount());
                    JSONArray baselines = new JSONArray();
                    for (int line = 0; line < 2; line++) {
                        int actual = baseline(text, line); baselines.put(actual);
                        assertEquals(density + "/" + padding + "/" + offset + "/" + line, sample.getJSONArray("baselines").getDouble(line) * scale, actual, 1);
                    }
                    int before = opened.size();
                    float x = 5 * scale, y = (text.getLayout().getLineTop(0) + text.getLayout().getLineBottom(0)) / 2f;
                    for (int action : new int[] {android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_UP}) {
                        android.view.MotionEvent event = android.view.MotionEvent.obtain(0, 0, action, x, y, 0);
                        try { assertTrue(text.getMovementMethod().onTouchEvent(text, (Spannable) document, event)); }
                        finally { event.recycle(); }
                    }
                    assertEquals(before + 1, opened.size()); assertEquals("https://example.com", opened.get(before));
                    Selection.setSelection((Spannable) document, 1, 5);
                    samples.put(new JSONObject(sample.toString()).put("nativeBaselines", baselines));
                }
            }
        }
        assertEquals(45, samples.length());
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-text-origins-api" + android.os.Build.VERSION.SDK_INT + ".json"), samples.toString(2));
    }

    private RectF ink(View root, int color) {
        java.util.List<RectF> rects = new java.util.ArrayList<>();
        Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
        try {
            root.draw(new Canvas(image) {
                @Override public void drawRect(float left, float top, float right, float bottom, Paint paint) {
                    if (paint.getColor() == color) { RectF rect = new RectF(left, top, right, bottom); getMatrix().mapRect(rect); rects.add(rect); }
                    super.drawRect(left, top, right, bottom, paint);
                }
            });
        } finally { image.recycle(); }
        assertEquals(1, rects.size()); return rects.get(0);
    }

    @Test public void pixelScrollTranslatesExistingNativeGlyphAndUnderlineTogetherWithoutResnappingOrReflow() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h240dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false); float scale = ui.context.getResources().getDisplayMetrics().density;
                FrameLayout host = new FrameLayout(ui.context);
                ScrollView scroll = new ScrollView(ui.context); scroll.setVerticalScrollBarEnabled(false);
                NativeMarkdownFlow flow = new NativeMarkdownFlow(ui); flow.collapseTop = flow.collapseBottom = false;
                flow.inset(18 * scale, 25 * scale, 18 * scale, 0);
                View markdown = NativeMarkdown.view(ui, "[agjpqy](https://example.com)", false, ignored -> fail("Scrolling must not open links"));
                flow.add(markdown, 0, 0); View spacer = new View(ui.context); spacer.setMinimumHeight(ui.dp(200)); flow.add(spacer, 0, 0);
                scroll.addView(flow); host.addView(scroll, new FrameLayout.LayoutParams(-1, -1)); controller.get().setContentView(host);
                layout(host, ui.dp(360), ui.dp(60), 0); settle(host, ui.dp(360), ui.dp(60), 0);
                TextView text = text(markdown); assertNotNull(text); Object document = text.getText(), nativeLayout = text.getLayout();
                Selection.setSelection((Spannable) document, 1, 5);
                RectF initial = ink(host, ui.primary); int initialBaseline = baseline(text, 0);
                for (int offset : new int[] {1, 2, 3}) {
                    scroll.scrollTo(0, offset); assertEquals(offset, scroll.getScrollY());
                    assertFalse("A compositor-only scroll must not request a fresh CSS baseline layout", host.getViewTreeObserver().dispatchOnPreDraw());
                    assertSame(document, text.getText()); assertSame(nativeLayout, text.getLayout());
                    assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
                    assertEquals(initialBaseline - offset, baseline(text, 0));
                    RectF actual = ink(host, ui.primary); assertEquals(initial.top - offset, actual.top, .0001);
                    assertEquals(initial.left, actual.left, .0001); assertEquals(initial.right, actual.right, .0001);
                }
            }
        }
    }

    @Test public void unchangedFramesSkipEditorPaddingQueriesButLayoutTextAndAncestorMovesStillRecheck() {
        RuntimeEnvironment.setQualifiers("w360dp-h240dp-420dpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            class CountedText extends NativeTextView {
                int paddingQueries;
                CountedText() { super(ui.context); }
                @Override public int getTotalPaddingTop() { paddingQueries++; return super.getTotalPaddingTop(); }
            }
            FrameLayout host = new FrameLayout(ui.context), parent = new FrameLayout(ui.context);
            CountedText text = new CountedText(); ui.typography(text, 14, ui.text, 400);
            ui.cssLineHeight(text, 1.85f); text.setText("Stable selectable text\nSecond line"); text.setTextIsSelectable(true);
            NativeUi.cssTextRounding(text, .375f);
            parent.addView(text, new FrameLayout.LayoutParams(-1, -2)); host.addView(parent, new FrameLayout.LayoutParams(-1, -1)); controller.get().setContentView(host);
            layout(host, ui.dp(360), ui.dp(240), 0); settle(host, ui.dp(360), ui.dp(240), 0);
            Object document = text.getText(), nativeLayout = text.getLayout(); Selection.setSelection((Spannable) document, 1, 5);
            text.paddingQueries = 0;
            for (int frame = 0; frame < 120; frame++) assertTrue(text.onOriginPreDraw());
            assertEquals("Steady frames must not enter TextView's editor/padding path", 0, text.paddingQueries);
            assertSame(nativeLayout, text.getLayout()); assertSame(document, text.getText());
            parent.offsetTopAndBottom(1); text.onOriginPreDraw(); assertTrue("Even a direct ancestor offset must be observed", text.paddingQueries > 0);
            assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
            text.paddingQueries = 0; text.setPadding(0, ui.dp(3), 0, 0);
            layout(host, ui.dp(360), ui.dp(240), 0); settle(host, ui.dp(360), ui.dp(240), 0);
            assertTrue("A new layout must recompute its padding", text.paddingQueries > 0);
            NativeLineHeight before = text.lineMetrics; text.setText("A changed document"); assertNotSame(before, text.lineMetrics);
            text.paddingQueries = 0; text.onOriginPreDraw(); assertTrue("A changed metric span invalidates the fast path", text.paddingQueries > 0);
        }
    }

    @Test public void parentRelayoutRetainsTheOriginCacheButTextPaddingAndGravityChangesInvalidateIt() {
        RuntimeEnvironment.setQualifiers("w360dp-h240dp-420dpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            class CountedText extends NativeTextView {
                int paddingQueries;
                CountedText() { super(ui.context); }
                @Override public int getTotalPaddingTop() { paddingQueries++; return super.getTotalPaddingTop(); }
            }
            FrameLayout host = new FrameLayout(ui.context);
            CountedText text = new CountedText(); ui.typography(text, 14, ui.text, 400);
            ui.cssLineHeight(text, 1.85f); text.setText("Unchanged selectable document\nSecond line");
            text.setTextIsSelectable(true); NativeUi.cssTextRounding(text, .375f);
            host.addView(text, new FrameLayout.LayoutParams(-1, ui.dp(150)));
            controller.get().setContentView(host);
            layout(host, ui.dp(360), ui.dp(240), 0); settle(host, ui.dp(360), ui.dp(240), 0);
            Object document = text.getText(), nativeLayout = text.getLayout();
            Selection.setSelection((Spannable) document, 1, 5);
            text.paddingQueries = 0;
            for (int frame = 0; frame < 120; frame++) {
                // Other transcript rows can require a parent layout while this document is unchanged.
                text.forceLayout(); host.requestLayout();
                layout(host, ui.dp(360), ui.dp(240), 0);
                assertTrue(text.onOriginPreDraw());
            }
            assertEquals("Unchanged parent layout must not re-enter TextView's editor padding path", 0, text.paddingQueries);
            assertSame(document, text.getText()); assertSame(nativeLayout, text.getLayout());
            assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
            int changeIndex = 0;
            for (Runnable change : new Runnable[] {
                    () -> text.setPadding(0, ui.dp(7), 0, ui.dp(3)),
                    () -> text.setGravity(android.view.Gravity.BOTTOM),
                    () -> text.setMaxLines(1),
                    () -> text.setText("Changed document\nChanged line")}) {
                text.paddingQueries = 0; change.run();
                layout(host, ui.dp(360), ui.dp(240), 0); settle(host, ui.dp(360), ui.dp(240), 0);
                text.onOriginPreDraw();
                assertTrue("A real TextView input change must recheck its origin: " + changeIndex++, text.paddingQueries > 0);
            }
        }
    }

    @Test public void cssOriginObserverDoesNotReplaceThePlatformsOneShotTextPreDrawCallback() {
        RuntimeEnvironment.setQualifiers("w360dp-h240dp-420dpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup().visible()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            class CountedText extends NativeTextView {
                int checks;
                CountedText() { super(ui.context); }
                @Override boolean alignOrigin() { checks++; return super.alignOrigin(); }
            }
            FrameLayout host = new FrameLayout(ui.context);
            CountedText text = new CountedText(); ui.typography(text, 14, ui.text, 400);
            ui.cssLineHeight(text, 1.85f); text.setText("Selectable document\nSecond line");
            text.setTextIsSelectable(true); NativeUi.cssTextRounding(text, .375f);
            host.addView(text, new FrameLayout.LayoutParams(-1, -2)); controller.get().setContentView(host);
            layout(host, ui.dp(360), ui.dp(240), 0);
            assertTrue("This test must exercise TextView's registered platform callback",
                    org.robolectric.util.ReflectionHelpers.<Boolean>getField(text, "mPreDrawRegistered"));
            for (int pass = 0; pass < 4; pass++) text.getViewTreeObserver().dispatchOnPreDraw();
            assertFalse("The platform callback must complete its normal one-shot lifecycle",
                    org.robolectric.util.ReflectionHelpers.<Boolean>getField(text, "mPreDrawRegistered"));
            assertTrue("CSS baseline observation has a separate lifetime", text.observingOrigin);
            Object document = text.getText(); Selection.setSelection((Spannable) document, 1, 5);
            for (int pass = 0; pass < 4; pass++) text.getViewTreeObserver().dispatchOnPreDraw();
            text.checks = 0;
            for (int frame = 0; frame < 120; frame++) text.getViewTreeObserver().dispatchOnPreDraw();
            assertEquals("Exactly one CSS-origin callback per frame, not one per platform registration", 120, text.checks);
            assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
            android.view.ViewTreeObserver observer = text.getViewTreeObserver();
            host.removeView(text); assertFalse(text.observingOrigin); text.checks = 0;
            observer.dispatchOnPreDraw(); assertEquals("Detachment removes the owned observer", 0, text.checks);
            host.addView(text, new FrameLayout.LayoutParams(-1, -2)); layout(host, ui.dp(360), ui.dp(240), 0);
            for (int pass = 0; pass < 4; pass++) text.getViewTreeObserver().dispatchOnPreDraw();
            text.checks = 0; text.getViewTreeObserver().dispatchOnPreDraw();
            assertEquals("A warm-document reattach must not duplicate the observer", 1, text.checks);
        }
    }
}
