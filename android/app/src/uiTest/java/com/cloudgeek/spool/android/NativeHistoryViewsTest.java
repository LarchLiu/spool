package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.Looper;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeHistoryViewsTest {
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height); view.getViewTreeObserver().dispatchOnPreDraw();
    }
    private JSONArray reference() throws Exception {
        try (InputStream input = getClass().getResourceAsStream("/web-history-notices.json")) {
            assertNotNull(input); return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("cases");
        }
    }
    private void close(List<String> failures, String label, double expected, double actual) {
        if (Math.abs(expected - actual) > 1) failures.add(label + ": " + actual + " vs " + expected);
    }

    @Test public void originalWebHistoryNoticesMatchAllLocalesThemesWidthsAndDensities() throws Exception {
        JSONArray cases = reference(), records = new JSONArray(); List<String> failures = new ArrayList<>(); assertEquals(150, cases.length());
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i); float density = (float) item.getDouble("density");
            int width = item.getInt("viewport"); String locale = item.getString("locale"), id = item.getString("id");
            boolean dark = item.getString("theme").equals("dark");
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + (density == 1 ? "mdpi" : density == 3 ? "xxhdpi" : "420dpi"));
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, locale); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeHistoryNotice notice = new NativeHistoryNotice(ui, id.startsWith("unavailable"), id.contains("loading") || id.contains("retry"), id.contains("retry"), () -> {});
                FrameLayout root = new FrameLayout(controller.get()); root.setBackgroundColor(ui.surface);
                FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(ui.dp(width - 36), -2); params.leftMargin = ui.dp(18); params.topMargin = ui.dp(25); root.addView(notice, params);
                controller.get().setContentView(root); layout(root, ui.dp(width), ui.dp(800));
                close(failures, item + " height", item.getDouble("height") * density, notice.getHeight());
                JSONArray expected = item.getJSONArray("children"), actual = new JSONArray(); assertEquals(id, expected.length(), notice.getChildCount());
                for (int j = 0; j < expected.length(); j++) {
                    JSONObject web = expected.getJSONObject(j); TextView nativeText = (TextView) notice.getChildAt(j);
                    JSONObject box = new JSONObject().put("x", nativeText.getLeft()).put("y", nativeText.getTop()).put("width", nativeText.getWidth()).put("height", nativeText.getHeight());
                    close(failures, id + "/" + locale + "/" + density + " x", web.getDouble("x") * density, nativeText.getLeft());
                    close(failures, id + "/" + locale + "/" + density + " y", web.getDouble("y") * density, nativeText.getTop());
                    close(failures, id + "/" + locale + "/" + density + " width", web.getDouble("width") * density, nativeText.getWidth());
                    close(failures, id + "/" + locale + "/" + density + " height", web.getDouble("height") * density, nativeText.getHeight());
                    assertEquals(web.getString("text"), nativeText.getText().toString()); assertEquals((float) web.getDouble("fontSize") * density, nativeText.getTextSize(), .001f);
                    assertEquals(ui.muted, nativeText.getCurrentTextColor());
                    assertEquals(Math.round(web.getDouble("paddingLeft") * density), nativeText.getPaddingLeft());
                    assertEquals(Math.round(web.getDouble("paddingTop") * density), nativeText.getPaddingTop());
                    actual.put(box);
                }
                records.put(new JSONObject().put("case", item).put("height", notice.getHeight()).put("children", actual));
            }
        }
        File out = new File(System.getProperty("spool.ui.artifacts")); assertTrue(out.isDirectory() || out.mkdirs());
        Files.writeString(new File(out, "native-history-notices-api" + android.os.Build.VERSION.SDK_INT + ".json").toPath(), records.toString(2));
        assertTrue(String.join("\n", failures.subList(0, Math.min(30, failures.size()))) + "\n" + failures.size() + " geometry differences", failures.isEmpty());
    }

    @Test public void afterPaintCanBeRevokedBeforeDrawAndAfterDrawButNeverRunsInsideTheDraw() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            View view = new View(controller.get()); controller.get().setContentView(view); layout(view, 360, 800); idle(); AtomicInteger callbacks = new AtomicInteger();
            Runnable cancel = NativeAfterPaint.schedule(view, callbacks::incrementAndGet); idle(); assertEquals(0, callbacks.get());
            cancel.run(); view.getViewTreeObserver().dispatchOnDraw(); idle(); assertEquals(0, callbacks.get());
            cancel = NativeAfterPaint.schedule(view, callbacks::incrementAndGet); view.getViewTreeObserver().dispatchOnDraw(); assertEquals(0, callbacks.get());
            cancel.run(); idle(); assertEquals(0, callbacks.get());
            NativeAfterPaint.schedule(view, callbacks::incrementAndGet); view.getViewTreeObserver().dispatchOnDraw(); view.getViewTreeObserver().dispatchOnDraw();
            assertEquals(0, callbacks.get()); idle(); assertEquals(1, callbacks.get());
            view.getViewTreeObserver().dispatchOnDraw(); idle(); assertEquals(1, callbacks.get());
        }
    }

    @Test public void contentAndViewportResizeFollowOnlyWhenTheReaderIsNearTheBottom() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            FrameLayout root = new FrameLayout(controller.get()); ScrollView scroll = new ScrollView(controller.get()); scroll.setFillViewport(true);
            View content = new View(controller.get()); content.setMinimumHeight(1200); content.setLayoutParams(new ScrollView.LayoutParams(-1, 1200)); scroll.addView(content); root.addView(scroll, new FrameLayout.LayoutParams(-1,-1));
            NativeHistoryScroll.State state = new NativeHistoryScroll.State(); state.select("first"); NativeHistoryScroll behavior = new NativeHistoryScroll(scroll, content, state, 90);
            controller.get().setContentView(root); idle(); behavior.changing(null); behavior.changed(); layout(root, 360, 800);
            assertEquals(1200, content.getHeight()); assertEquals(800, scroll.getHeight()); assertEquals(400, scroll.getScrollY());
            content.getLayoutParams().height = 1600; content.setMinimumHeight(1600); content.requestLayout(); layout(root, 360, 800); assertEquals(800, scroll.getScrollY());
            layout(root, 360, 600); assertEquals(1000, scroll.getScrollY());
            scroll.scrollTo(0, 910); assertFalse("Exactly90px is not following on Web", state.following);
            content.getLayoutParams().height = 1900; content.setMinimumHeight(1900); content.requestLayout(); layout(root, 360, 600); assertEquals(910, scroll.getScrollY());
            scroll.scrollTo(0, 1211); assertTrue("Less than90px follows", state.following);
            content.getLayoutParams().height = 2100; content.setMinimumHeight(2100); content.requestLayout(); layout(root, 360, 600); assertEquals(1500, scroll.getScrollY());
            behavior.dispose(); content.getLayoutParams().height = 2400; content.setMinimumHeight(2400); content.requestLayout(); layout(root, 360, 600); assertEquals(1500, scroll.getScrollY());
        }
    }

    @Test public void renderHistoryNoticeContactSheet() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[] {false,true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark, "en"); LinearLayout root = ui.column(); root.setBackgroundColor(ui.surface); root.setPadding(18,25,18,0);
            for (String id : new String[] {"unavailable","loading","retry","unavailable-loading","unavailable-retry"}) {
                NativeHistoryNotice notice = new NativeHistoryNotice(ui, id.startsWith("unavailable"), id.contains("loading") || id.contains("retry"), id.contains("retry"), () -> {});
                root.addView(notice, new LinearLayout.LayoutParams(-1,-2)); root.addView(ui.space(21));
            }
            controller.get().setContentView(root); layout(root, 360, 800); Bitmap image = Bitmap.createBitmap(360,800,Bitmap.Config.ARGB_8888); root.draw(new Canvas(image));
            File out = new File(System.getProperty("spool.ui.artifacts")); assertTrue(out.isDirectory() || out.mkdirs());
            try (FileOutputStream stream = new FileOutputStream(new File(out,"native-history-notices-api"+android.os.Build.VERSION.SDK_INT+"-"+(dark?"dark":"light")+".png"))) { image.compress(Bitmap.CompressFormat.PNG,100,stream); }
            image.recycle();
        }
    }

    @Test public void fractionalDensityFollowThresholdDoesNotRoundAwayTheLastPhysicalPixel() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-420dpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ScrollView scroll = new ScrollView(controller.get()); View content = new View(controller.get()); content.setMinimumHeight(3000); scroll.addView(content);
            NativeHistoryScroll.State state = new NativeHistoryScroll.State(); state.select("first");
            NativeHistoryScroll behavior = new NativeHistoryScroll(scroll, content, state, 90 * controller.get().getResources().getDisplayMetrics().density);
            controller.get().setContentView(scroll); idle(); behavior.changing(null); behavior.changed(); layout(scroll, 945, 2100);
            scroll.scrollTo(0, 663); assertFalse("237px is outside90CSSpx at420dpi", state.following);
            scroll.scrollTo(0, 664); assertTrue("236px is inside90CSSpx at420dpi", state.following);
            behavior.dispose();
        }
    }
}
