package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.RectF;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import java.io.File;
import java.io.FileOutputStream;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeSurfaceTest {
    private Bitmap render(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
        Bitmap image = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(image));
        return image;
    }

    @Test @Config(sdk = {26, 35}) public void originalBlinkPaintReplayHasAnIndependentProductionSurfaceFixture() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            for (boolean dark : new boolean[] {false, true}) for (boolean shadow : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    FrameLayout page = new FrameLayout(ui.context); page.setBackgroundColor(ui.elevated); page.setClipChildren(false);
                    NativeSurface surface = new NativeSurface(ui, ui.elevated, ui.borderStrong, 19, 0, shadow ? 8 : 0, shadow ? 32 : 0);
                    FrameLayout.LayoutParams at = new FrameLayout.LayoutParams(ui.dp(336), ui.dp(99));
                    at.leftMargin = ui.dp(12); at.topMargin = ui.dp(64); page.addView(surface, at);
                    controller.get().setContentView(page);
                    Bitmap image = render(page, ui.dp(360), ui.dp(237));
                    try (FileOutputStream output = new FileOutputStream(new File(directory, "native-api" + android.os.Build.VERSION.SDK_INT
                            + "-css-paint-dpr" + (density.equals("mdpi") ? 1 : 3) + (dark ? "-dark-" : "-light-") + (shadow ? "shadow" : "border") + ".png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                    } finally { image.recycle(); }
                }
            }
        }
    }

    @Test public void productionComposerCornersAndShadowMatchWebSurfaceFixture() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    LinearLayout page = ui.workspace(); page.setBackgroundColor(ui.elevated);
                    page.addView(new View(ui.context), new LinearLayout.LayoutParams(1, 64));
                    for (int height : new int[] {99, 227}) {
                        NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
                        LinearLayout surface = (LinearLayout) composer.getChildAt(0);
                        assertTrue(surface instanceof NativeSurface);
                        assertEquals(0f, surface.getElevation(), 0f);
                        surface.getLayoutParams().height = height;
                        for (int i = 0; i < surface.getChildCount(); i++) surface.getChildAt(i).setVisibility(View.INVISIBLE);
                        page.addView(composer);
                        page.addView(new View(ui.context), new LinearLayout.LayoutParams(1, 64));
                    }
                    controller.get().setContentView(page);
                    Bitmap image = render(page, width, 538);
                    try (FileOutputStream output = new FileOutputStream(new File(directory, "native-composer-surface-" + width + (dark ? "-dark" : "-light") + ".png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                    }
                    // The former elevation shadow ended abruptly at y=163 and left dark corner wedges.
                    assertNotEquals(ui.elevated, image.getPixel(12, 175));
                    assertNotEquals(ui.elevated, image.getPixel(width / 2, 60));
                    for (int y = 164; y < 190; y++) for (int x = 0; x < 60; x++) {
                        int left = image.getPixel(x, y), right = image.getPixel(width - x - 1, y);
                        for (int shift : new int[] {0, 8, 16})
                            assertTrue("Asymmetric shadow at " + x + "," + y, Math.abs((left >> shift & 255) - (right >> shift & 255)) <= 1);
                    }
                    image.recycle();
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void productionComposerHasIndependentFullViewportDensityFixtures() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    LinearLayout page = ui.workspace(); page.setBackgroundColor(ui.elevated);
                    page.addView(new View(ui.context), new LinearLayout.LayoutParams(-1, ui.dp(64)));
                    NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
                    composer.box.getLayoutParams().height = ui.dp(99);
                    for (int i = 0; i < composer.box.getChildCount(); i++) composer.box.getChildAt(i).setVisibility(View.INVISIBLE);
                    page.addView(composer);
                    controller.get().setContentView(page);
                    float scale = ui.context.getResources().getDisplayMetrics().density;
                    String name = "native-api" + android.os.Build.VERSION.SDK_INT + "-composer-css-paint-dpr"
                            + (density.equals("mdpi") ? "1" : density.equals("420dpi") ? "2.625" : "3")
                            + (dark ? "-dark-shadow" : "-light-shadow");
                    Bitmap image = render(page, ui.dp(360), ui.dp(density.equals("420dpi") ? 240 : 237));
                    try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                    } finally { image.recycle(); }
                    RectF bounds = ReflectionHelpers.getField(composer.box, "outer");
                    org.json.JSONObject geometry = new org.json.JSONObject()
                            .put("left", composer.getLeft() + composer.box.getLeft() + bounds.left)
                            .put("top", composer.getTop() + composer.box.getTop() + bounds.top)
                            .put("right", composer.getLeft() + composer.box.getLeft() + bounds.right)
                            .put("bottom", composer.getTop() + composer.box.getTop() + bounds.bottom)
                            .put("radius", 19 * scale);
                    java.nio.file.Files.writeString(new File(directory, name + ".json").toPath(), geometry.toString(2));
                    assertEquals(12 * scale, geometry.getDouble("left"), .0001);
                    assertEquals(64 * scale, geometry.getDouble("top"), .0001);
                    assertEquals(348 * scale, geometry.getDouble("right"), .0001);
                    assertEquals(163 * scale, geometry.getDouble("bottom"), .0001);
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void composerPaintSnapsInWorkspaceCoordinatesWithoutReplacingOrScalingItsEditor() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                LinearLayout page = ui.workspace();
                View spacer = new View(ui.context);
                page.addView(spacer, new LinearLayout.LayoutParams(-1, ui.dp(64)));
                NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
                page.addView(composer); controller.get().setContentView(page);
                composer.input.setText("First\nSecond"); composer.input.setSelection(1, 4);
                android.text.Editable document = composer.input.getText();
                float scale = ui.context.getResources().getDisplayMetrics().density;
                for (int offset : new int[] {0, 1, 3, 0}) {
                    spacer.getLayoutParams().height = ui.dp(64) + offset;
                    page.setPadding(offset, 0, offset, 0);
                    Bitmap image = render(page, ui.dp(360), ui.dp(240)); image.recycle();
                    RectF bounds = ReflectionHelpers.getField(composer.box, "outer");
                    int x = composer.getLeft() + composer.box.getLeft(), y = composer.getTop() + composer.box.getTop();
                    assertEquals(Math.round(x / scale) * scale, x + bounds.left, .0001f);
                    assertEquals(Math.round(y / scale) * scale, y + bounds.top, .0001f);
                    assertEquals(Math.round((x + composer.box.getWidth()) / scale) * scale, x + bounds.right, .0001f);
                    assertEquals(Math.round((y + composer.box.getHeight()) / scale) * scale, y + bounds.bottom, .0001f);
                    assertSame(document, composer.input.getText());
                    assertEquals(1, composer.input.getSelectionStart()); assertEquals(4, composer.input.getSelectionEnd());
                    for (View child : new View[] {composer.input, composer.bottom, composer.send, composer.model}) {
                        assertEquals(1f, child.getScaleX(), 0); assertEquals(1f, child.getScaleY(), 0);
                        assertEquals(0f, child.getTranslationX(), 0); assertEquals(0f, child.getTranslationY(), 0);
                    }
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void childrenCannotSquareOffTheRoundedBorderAtAnyDensity() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeSurface surface = new NativeSurface(ui, Color.WHITE, Color.BLACK, 19, 0, 8, 32);
                View child = new View(ui.context); child.setBackgroundColor(Color.RED);
                surface.addView(child, new LinearLayout.LayoutParams(-1, -1));
                controller.get().setContentView(surface);
                Bitmap image = render(surface, ui.dp(100), ui.dp(100));
                assertEquals(Color.RED, image.getPixel(ui.dp(50), ui.dp(50)));
                assertNotEquals(Color.RED, image.getPixel(ui.dp(2), ui.dp(98)));
                assertNotEquals(Color.RED, image.getPixel(ui.dp(98), ui.dp(98)));
                assertEquals(Color.BLACK, image.getPixel(ui.dp(50), image.getHeight() - 1));
                image.recycle();
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void cachedOuterShadowExcludesTheUnshiftedPaddingBoxEvenWithTransparentFill() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeSurface surface = new NativeSurface(ui, Color.TRANSPARENT, Color.BLACK, 19, 0, 8, 32);
                controller.get().setContentView(surface);
                Bitmap image = render(surface, ui.dp(160), ui.dp(100)); image.recycle();
                Bitmap mask = ReflectionHelpers.getField(surface, "shadow");
                int spread = ReflectionHelpers.getField(surface, "spread");
                float offsetY = ReflectionHelpers.getField(surface, "offsetY");
                assertEquals(0, Color.alpha(mask.getPixel(spread + ui.dp(80), Math.round(spread + ui.dp(50) - offsetY))));
                assertEquals(0, Color.alpha(mask.getPixel(spread + ui.dp(80), Math.round(spread + ui.dp(5) - offsetY))));
                assertTrue(Color.alpha(mask.getPixel(spread + ui.dp(80), Math.round(spread + ui.dp(104) - offsetY))) > 0);
                RectF outer = ReflectionHelpers.getField(surface, "outer"), center = ReflectionHelpers.getField(surface, "center");
                float stroke = ReflectionHelpers.getField(surface, "stroke");
                assertEquals(outer.left + stroke / 2, center.left, 0);
                assertEquals(outer.bottom - stroke / 2, center.bottom, 0);
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void childBackgroundUsesTheSameAntialiasedCurveAsTheInnerBorder() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeSurface surface = new NativeSurface(ui, Color.WHITE, Color.BLACK, 19, 0, 0, 0);
                surface.setBackgroundColor(Color.WHITE);
                controller.get().setContentView(surface);
                for (int height : new int[] {99, 227, 99}) {
                    int width = ui.dp(160), pixels = ui.dp(height);
                    Bitmap expected = render(surface, width, pixels);
                    Paint childPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
                    childPaint.setColor(Color.RED);
                    RectF inner = ReflectionHelpers.getField(surface, "inner");
                    float radius = 18 * ui.context.getResources().getDisplayMetrics().density;
                    new Canvas(expected).drawRoundRect(inner, radius, radius, childPaint);
                    View child = new View(ui.context); child.setBackgroundColor(Color.RED);
                    surface.addView(child, new LinearLayout.LayoutParams(-1, -1));
                    Bitmap actual = render(surface, width, pixels);
                    int blended = 0;
                    for (int y = pixels - ui.dp(20); y < pixels; y++) {
                        for (int x = 0; x < width; x++) {
                            if (x >= ui.dp(20) && x < width - ui.dp(20)) continue;
                            int want = expected.getPixel(x, y), got = actual.getPixel(x, y);
                            for (int shift : new int[] {0, 8, 16, 24})
                                assertTrue("Smooth bottom corner at " + density + "/" + height + ": " + x + "," + y
                                                + " expected=" + Integer.toHexString(want) + " actual=" + Integer.toHexString(got),
                                        Math.abs((want >>> shift & 255) - (got >>> shift & 255)) <= 1);
                            if (Color.green(want) > 0 && Color.green(want) < 255 && Color.red(want) > Color.green(want)) blended++;
                        }
                    }
                    assertTrue("Both bottom curves need partially covered child pixels", blended > 2);
                    surface.removeView(child);
                    actual.recycle(); expected.recycle();
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void realComposerBottomCornersStayClippedAndShadowedThroughGrowthAndWindowResize() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w900dp-h800dp-" + density);
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    LinearLayout page = ui.workspace(); page.setBackgroundColor(ui.elevated);
                    page.addView(new View(ui.context), new LinearLayout.LayoutParams(-1, 0, 1));
                    NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
                    composer.model.bind("gpt-6.1-codex", "codex", "Choose model");
                    composer.permission.bind("Ask for approval", "shield-question", "Permissions");
                    page.addView(composer); controller.get().setContentView(page);
                    composer.bottom.setBackgroundColor(Color.RED);
                    int[][] sizes = {{360,800}, {220,500}, {900,500}, {360,560}};
                    String[] values = {"", "One\nTwo\nThree\nFour\nFive\nSix", "One\nTwo\nThree", ""};
                    for (int i = 0; i < sizes.length; i++) {
                        composer.input.setText(values[i]);
                        Bitmap image = render(page, ui.dp(sizes[i][0]), ui.dp(sizes[i][1]));
                        int left = composer.getLeft() + composer.box.getLeft();
                        int right = left + composer.box.getWidth();
                        int bottom = composer.getTop() + composer.box.getBottom();
                        assertEquals(0f, composer.box.getElevation(), 0f);
                        assertFalse(page.getClipChildren()); assertFalse(composer.getClipChildren()); assertFalse(composer.getClipToPadding());
                        assertEquals(Color.RED, image.getPixel((left + right) / 2, bottom - ui.dp(3)));
                        float scale = ui.context.getResources().getDisplayMetrics().density;
                        // The last integer View row can now lie on the CSS border's AA fringe.
                        int borderCenter = (int) Math.floor(Math.round(bottom / scale) * scale - scale / 2);
                        assertEquals(ui.borderStrong, image.getPixel((left + right) / 2, borderCenter));
                        assertNotEquals("Left child background must not fill the rounded corner", Color.RED,
                                image.getPixel(left + ui.dp(2), bottom - ui.dp(2)));
                        assertNotEquals("Right child background must not fill the rounded corner", Color.RED,
                                image.getPixel(right - ui.dp(2), bottom - ui.dp(2)));
                        int shadowLeft = image.getPixel(left - ui.dp(3), bottom + ui.dp(5));
                        int shadowRight = image.getPixel(right + ui.dp(3) - 1, bottom + ui.dp(5));
                        assertNotEquals("Shadow must continue below the left corner", ui.elevated, shadowLeft);
                        assertNotEquals("Shadow must continue below the right corner", ui.elevated, shadowRight);
                        for (int shift : new int[] {0, 8, 16})
                            assertTrue("Bottom-corner shadows stay symmetric", Math.abs((shadowLeft >> shift & 255) - (shadowRight >> shift & 255)) <= 1);
                        assertTrue(composer.model.getLeft() >= 0);
                        assertEquals(composer.bottom.getWidth() - composer.bottom.getPaddingRight(), composer.send.getRight());
                        image.recycle();
                    }
                }
            }
        }
    }

    @Test public void popupFramesRenderTheFourOriginalWebSurfaceProfiles() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    FrameLayout page = new FrameLayout(ui.context); page.setBackgroundColor(ui.elevated);
                    int[][] profiles = {{17, ui.borderStrong, 12, 40, ui.shadow}, {13, ui.borderStrong, 12, 40, ui.shadow},
                            {12, ui.line, 8, 30, ui.shadow}, {10, ui.borderStrong, 8, 28, 0x33000000}};
                    for (int i = 0; i < profiles.length; i++) {
                        int[] profile = profiles[i];
                        NativePopupFrame frame = new NativePopupFrame(ui, new View(ui.context), profile[0], profile[1], profile[2], profile[3], profile[4]);
                        Rect bounds = frame.place(new Rect(0, 0, width, 800), 12, 64 + i * 160, width - 24, 80);
                        FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(bounds.width(), bounds.height());
                        params.leftMargin = bounds.left; params.topMargin = bounds.top;
                        page.addView(frame, params);
                    }
                    controller.get().setContentView(page);
                    Bitmap image = render(page, width, 800);
                    try (FileOutputStream output = new FileOutputStream(new File(directory, "native-popup-surface-" + width + (dark ? "-dark" : "-light") + ".png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                    }
                    image.recycle();
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void fractionalBoundsDrivePaintClipAndShadowCacheWithoutScalingChildren() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                float scale = ui.context.getResources().getDisplayMetrics().density;
                NativeSurface surface = new NativeSurface(ui, Color.WHITE, Color.BLACK, 11, 0, 3, 10);
                View child = new View(ui.context); child.setBackgroundColor(Color.RED);
                surface.addView(child, new LinearLayout.LayoutParams(-1, -1));
                controller.get().setContentView(surface);
                Bitmap firstImage = render(surface, ui.dp(100), ui.dp(80)); firstImage.recycle();
                float width = 99.25f * scale, height = 79.75f * scale;
                surface.setCssBorderBox(width, height, 0, 0);
                assertNull(ReflectionHelpers.getField(surface, "shadow"));
                Bitmap image = render(surface, ui.dp(100), ui.dp(80));
                RectF outer = ReflectionHelpers.getField(surface, "outer"), inner = ReflectionHelpers.getField(surface, "inner");
                float paintedWidth = Math.round(width / scale) * scale, paintedHeight = Math.round(height / scale) * scale;
                assertEquals(paintedWidth, outer.width(), 0f); assertEquals(paintedHeight, outer.height(), 0f);
                assertEquals(paintedWidth - 2 * scale, inner.width(), .0001f);
                assertEquals(paintedHeight - 2 * scale, inner.height(), .0001f);
                assertEquals(Color.RED, image.getPixel(ui.dp(50), ui.dp(40)));
                assertNotEquals(Color.RED, image.getPixel(ui.dp(99), ui.dp(40)));
                assertNotEquals(Color.RED, image.getPixel(ui.dp(2), ui.dp(78)));
                assertEquals(ui.dp(100), child.getWidth());
                assertEquals(1f, child.getScaleX(), 0f); assertEquals(1f, child.getScaleY(), 0f);
                image.recycle();
                Bitmap first = ReflectionHelpers.getField(surface, "shadow"); assertNotNull(first);
                surface.setTranslationX(.25f * scale);
                surface.setCssBorderBox(width, height, 0, 0);
                assertSame(first, ReflectionHelpers.getField(surface, "shadow"));
                surface.setCssBorderBox(width, height, .125f * scale, .125f * scale);
                assertSame("Moving the snapped border without changing its size reuses the mask", first, ReflectionHelpers.getField(surface, "shadow"));
                surface.setCssBorderBox(width + scale, height, 0, 0);
                assertNull("A snapped border change invalidates the mask even if View dimensions do not change", ReflectionHelpers.getField(surface, "shadow"));
                Bitmap resized = render(surface, ui.dp(100), ui.dp(80)); resized.recycle();
                assertNotSame(first, ReflectionHelpers.getField(surface, "shadow"));
                controller.get().setContentView(new View(ui.context));
                assertNull(ReflectionHelpers.getField(surface, "shadow"));
                surface.setCssBorderBox(0, 0, 0, 0);
                for (float invalid : new float[] {-1, Float.NaN, Float.NEGATIVE_INFINITY, Float.POSITIVE_INFINITY}) {
                    assertThrows(IllegalArgumentException.class, () -> surface.setCssBorderBox(invalid, 80, 0, 0));
                    assertThrows(IllegalArgumentException.class, () -> surface.setCssBorderBox(100, invalid, 0, 0));
                }
                for (float invalid : new float[] {Float.NaN, Float.NEGATIVE_INFINITY, Float.POSITIVE_INFINITY}) {
                    assertThrows(IllegalArgumentException.class, () -> surface.setCssBorderBox(100, 80, invalid, 0));
                    assertThrows(IllegalArgumentException.class, () -> surface.setCssBorderBox(100, 80, 0, invalid));
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void floatingWindowUsesWebShadowAndRetainsItsBoundsAndHitRegions() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + density);
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    NativeFloatingWindow window = new NativeFloatingWindow(ui,
                            new NativeFloatingWindow.Bounds(12, 80, width - 24, 520), ignored -> {});
                    window.setBackgroundColor(ui.surface); controller.get().setContentView(window);
                    Bitmap image = render(window, ui.dp(width), ui.dp(800));
                    assertTrue(window.sheet instanceof NativeSurface);
                    assertEquals(0f, window.sheet.getElevation(), 0f);
                    assertEquals(ui.dp(12), window.sheet.getLeft()); assertEquals(ui.dp(80), window.sheet.getTop());
                    assertEquals(ui.dp(width - 24), window.sheet.getWidth()); assertEquals(ui.dp(520), window.sheet.getHeight());
                    assertNotEquals(ui.surface, image.getPixel(ui.dp(width / 2f), ui.dp(625)));
                    for (View edge : window.edges) {
                        assertTrue(edge.getWidth() > 0 && edge.getHeight() > 0);
                        assertTrue(edge.getLeft() >= window.sheet.getLeft() && edge.getRight() <= window.sheet.getRight());
                        assertTrue(edge.getTop() >= window.sheet.getTop() && edge.getBottom() <= window.sheet.getBottom());
                    }
                    if (android.os.Build.VERSION.SDK_INT == 35 && density.equals("mdpi")) {
                        try (FileOutputStream output = new FileOutputStream(new File(directory, "native-floating-surface-" + width + (dark ? "-dark" : "-light") + ".png"))) {
                            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                        }
                    }
                    image.recycle();
                    window.update(new NativeFloatingWindow.Bounds(12, 140, width - 24, 400));
                    Bitmap resized = render(window, ui.dp(width), ui.dp(800));
                    assertEquals(ui.dp(140), window.sheet.getTop()); assertEquals(ui.dp(400), window.sheet.getHeight());
                    assertEquals("Resizing must invalidate the old shadow mask", ui.surface, resized.getPixel(ui.dp(width / 2f), ui.dp(625)));
                    resized.recycle();
                }
            }
        }
    }
}
