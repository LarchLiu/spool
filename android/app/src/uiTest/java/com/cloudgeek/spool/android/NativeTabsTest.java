package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.animation.ValueAnimator;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.widget.FrameLayout;
import java.io.File;
import java.nio.file.Files;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeTabsTest {
    private static void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(120, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, 120);
    }

    private static NativeTabs tabs(NativeUi ui, FrameLayout page, NativeTabs.Size size) {
        NativeTabs tabs = new NativeTabs(ui, "Git views", size);
        tabs.addTab("changes", "Changes", () -> tabs.select("changes"));
        tabs.addTab("history", "History", () -> tabs.select("history"));
        tabs.select("changes"); page.addView(tabs, new FrameLayout.LayoutParams(-1, -2)); layout(page, 360);
        return tabs;
    }

    private static Bitmap image(View view) {
        Bitmap image = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(image)); return image;
    }

    // Sample above the label so the bounds describe the painted selection, including during motion.
    private static int[] markerBounds(NativeTabs tabs, NativeUi ui) {
        Bitmap image = image(tabs.tabs);
        try {
            int color = ui.dark ? ui.sidebar : ui.elevated;
            int y = tabs.button("changes").getTop() + ui.dp(6);
            int left = -1, right = -1;
            for (int x = 0; x < image.getWidth(); x++) if (image.getPixel(x, y) == color) { if (left < 0) left = x; right = x + 1; }
            assertTrue("The selected tab must paint a background", left >= 0); return new int[]{left, right};
        } finally { image.recycle(); }
    }

    private static int markerLeft(NativeTabs tabs, NativeUi ui) { return markerBounds(tabs, ui)[0]; }

    private static void assertAligned(NativeTabs tabs, NativeUi ui, String key) {
        int[] bounds = markerBounds(tabs, ui);
        assertEquals(tabs.button(key).getLeft(), bounds[0], ui.dp(2));
        assertEquals(tabs.button(key).getRight(), bounds[1], ui.dp(2));
        assertTrue(tabs.button(key).isSelected());
    }

    private static void screenshot(Bitmap start, Bitmap middle, Bitmap end, String name) throws Exception {
        Bitmap strip = Bitmap.createBitmap(start.getWidth(), start.getHeight() * 3, Bitmap.Config.ARGB_8888);
        try {
            Canvas canvas = new Canvas(strip); canvas.drawBitmap(start, 0, 0, null);
            canvas.drawBitmap(middle, 0, start.getHeight(), null); canvas.drawBitmap(end, 0, start.getHeight() * 2, null);
            File output = new File(System.getProperty("spool.ui.artifacts"), "tabs"); assertTrue(output.isDirectory() || output.mkdirs());
            try (java.io.OutputStream stream = Files.newOutputStream(new File(output, name + "-api" + android.os.Build.VERSION.SDK_INT + ".png").toPath())) {
                strip.compress(Bitmap.CompressFormat.PNG, 100, stream);
            }
        } finally { strip.recycle(); }
    }

    @Test public void selectionSlidesAndTextColorsTransitionInBothSharedSizes() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[]{false, true}) for (NativeTabs.Size size : NativeTabs.Size.values())
            try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(activity.get(), dark, "en"); FrameLayout page = new FrameLayout(activity.get()); activity.get().setContentView(page);
                NativeTabs tabs = tabs(ui, page, size); assertAligned(tabs, ui, "changes");
                int first = markerLeft(tabs, ui), color = tabs.button("history").getCurrentTextColor();
                Bitmap start = image(tabs);
                tabs.button("history").performClick(); layout(page, 360);
                assertTrue(tabs.button("history").isSelected()); assertFalse(tabs.button("changes").isSelected());
                assertEquals("The marker starts at its previous position", first, markerLeft(tabs, ui));
                ValueAnimator animation = ReflectionHelpers.getField(tabs, "animation"); assertNotNull(animation);
                animation.setCurrentPlayTime(80);
                int middleLeft = markerLeft(tabs, ui), target = tabs.button("history").getLeft();
                assertTrue("The marker moves between tabs", middleLeft > first && middleLeft < target);
                if (dark) { assertNotEquals(color, tabs.button("history").getCurrentTextColor()); assertNotEquals(ui.thumb, tabs.button("history").getCurrentTextColor()); }
                Bitmap middle = image(tabs); animation.end(); assertAligned(tabs, ui, "history");
                assertEquals(dark ? ui.thumb : ui.text, tabs.button("history").getCurrentTextColor());
                Bitmap end = image(tabs);
                try { screenshot(start, middle, end, size.name().toLowerCase(java.util.Locale.ROOT) + "-" + (dark ? "dark" : "light")); }
                finally { start.recycle(); middle.recycle(); end.recycle(); }
            }
    }

    @Test public void rapidReversalContinuesFromThePaintedPositionAndResizeAndDetachSettleSelection() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), true, "en"); FrameLayout page = new FrameLayout(activity.get()); activity.get().setContentView(page);
            NativeTabs tabs = tabs(ui, page, NativeTabs.Size.COMPACT);
            tabs.button("history").performClick(); layout(page, 360);
            ValueAnimator outgoing = ReflectionHelpers.getField(tabs, "animation"); outgoing.setCurrentPlayTime(65);
            int middle = markerLeft(tabs, ui);
            tabs.button("changes").performClick(); layout(page, 360);
            assertFalse(outgoing.isRunning()); assertEquals(middle, markerLeft(tabs, ui));
            ValueAnimator returning = ReflectionHelpers.getField(tabs, "animation"); returning.setCurrentPlayTime(80);
            assertTrue(markerLeft(tabs, ui) < middle); returning.end(); assertAligned(tabs, ui, "changes");
            tabs.button("history").performClick(); layout(page, 360);
            layout(page, 240); assertAligned(tabs, ui, "history");
            tabs.button("changes").performClick(); layout(page, 240);
            ValueAnimator detached = ReflectionHelpers.getField(tabs, "animation"); assertNotNull(detached);
            page.removeView(tabs); assertFalse(detached.isRunning());
            page.addView(tabs); layout(page, 240); assertAligned(tabs, ui, "changes");
        }
    }

    @Test public void disabledSystemAnimationsSelectImmediately() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        float previous = ReflectionHelpers.callStaticMethod(ValueAnimator.class, "getDurationScale");
        ReflectionHelpers.callStaticMethod(ValueAnimator.class, "setDurationScale", ReflectionHelpers.ClassParameter.from(float.class, 0f));
        try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), true, "en"); FrameLayout page = new FrameLayout(activity.get()); activity.get().setContentView(page);
            NativeTabs tabs = tabs(ui, page, NativeTabs.Size.COMPACT);
            tabs.button("history").performClick(); layout(page, 360); assertAligned(tabs, ui, "history");
            assertNull(ReflectionHelpers.getField(tabs, "animation")); assertEquals(ui.thumb, tabs.button("history").getCurrentTextColor());
        } finally { ReflectionHelpers.callStaticMethod(ValueAnimator.class, "setDurationScale", ReflectionHelpers.ClassParameter.from(float.class, previous)); }
    }
}
