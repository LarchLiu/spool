package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.RectF;
import android.view.View;
import android.widget.FrameLayout;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.security.MessageDigest;
import java.util.HexFormat;
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
public class NativeRoundedBoxTest {
    private byte[] resource(String name) throws Exception {
        try (InputStream stream = getClass().getResourceAsStream("/composer-border/" + name)) {
            assertNotNull(name, stream); return stream.readAllBytes();
        }
    }

    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void analyticBorderMatchesEveryPixelOfOriginalCssAtThreeDensities() throws Exception {
        JSONObject reference = new JSONObject(new String(resource("reference.json"), StandardCharsets.UTF_8));
        JSONArray fixtures = reference.getJSONArray("fixtures"), samples = new JSONArray();
        File output = new File(System.getProperty("spool.ui.artifacts")); assertTrue(output.isDirectory() || output.mkdirs());
        int failures = 0;
        for (int i = 0; i < fixtures.length(); i++) {
            JSONObject fixture = fixtures.getJSONObject(i), viewport = fixture.getJSONObject("viewport"), box = fixture.getJSONObject("box");
            float scale = (float) fixture.getDouble("density");
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + (scale == 1 ? "mdpi" : scale == 3 ? "xxhdpi" : "420dpi"));
            byte[] bytes = resource(fixture.getString("name"));
            assertEquals(fixture.getString("sha256"), HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)));
            Bitmap expected = BitmapFactory.decodeByteArray(bytes, 0, bytes.length);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), fixture.getString("theme").equals("dark"));
                FrameLayout page = new FrameLayout(ui.context); page.setBackgroundColor(ui.elevated); page.setClipChildren(false);
                NativeSurface surface = new NativeSurface(ui, ui.elevated, ui.borderStrong, (float) box.getDouble("radius"), 0, 0, 0);
                surface.useAnalyticBorder();
                FrameLayout.LayoutParams at = new FrameLayout.LayoutParams(ui.dp((float) (box.getDouble("right") - box.getDouble("left"))),
                        ui.dp((float) (box.getDouble("bottom") - box.getDouble("top"))));
                at.leftMargin = ui.dp((float) box.getDouble("left")); at.topMargin = ui.dp((float) box.getDouble("top"));
                page.addView(surface, at); controller.get().setContentView(page);
                layout(page, ui.dp(viewport.getInt("width")), ui.dp(viewport.getInt("height")));
                surface.setCssBorderBox(surface.getWidth(), surface.getHeight(), surface.getLeft(), surface.getTop());
                assertEquals(expected.getWidth(), page.getWidth()); assertEquals(expected.getHeight(), page.getHeight());
                Bitmap actual = Bitmap.createBitmap(page.getWidth(), page.getHeight(), Bitmap.Config.ARGB_8888); page.draw(new Canvas(actual));
                int maximum = 0, outside = 0;
                for (int y = 0; y < actual.getHeight(); y++) for (int x = 0; x < actual.getWidth(); x++) {
                    int want = expected.getPixel(x, y), got = actual.getPixel(x, y), difference = 0;
                    for (int shift = 0; shift <= 24; shift += 8) difference = Math.max(difference, Math.abs((want >>> shift & 255) - (got >>> shift & 255)));
                    maximum = Math.max(maximum, difference); if (difference > 1) outside++;
                }
                samples.put(new JSONObject().put("fixture", fixture.getString("name")).put("pixels", actual.getWidth() * actual.getHeight())
                        .put("maximum", maximum).put("outsideTolerance", outside));
                failures += outside;
                try (FileOutputStream stream = new FileOutputStream(new File(output, "native-api" + android.os.Build.VERSION.SDK_INT + "-composer-border-" + fixture.getString("name")))) {
                    assertTrue(actual.compress(Bitmap.CompressFormat.PNG, 100, stream));
                } finally { actual.recycle(); expected.recycle(); }
            }
        }
        Files.writeString(new File(output, "native-composer-border-api" + android.os.Build.VERSION.SDK_INT + ".json").toPath(), samples.toString(2));
        assertEquals("Full original CSS border frames must match within one channel unit; no crops or resized references", 0, failures);
    }

    @Test public void cachedCoverageUsesCornerSizedStorageAndInvalidatesGeometryColorAndDetach() {
        NativeRoundedBox box = new NativeRoundedBox(); RectF bounds = new RectF(0, 0, 900, 500);
        box.prepare(bounds, 19, 1, Color.WHITE, Color.BLACK);
        Bitmap[] first = ((Bitmap[]) ReflectionHelpers.getField(box, "backgrounds")).clone();
        int pixels = 0; for (Bitmap image : first) if (image != null) pixels += image.getWidth() * image.getHeight();
        assertTrue("Do not allocate a full surface bitmap", pixels < 2000);
        box.prepare(bounds, 19, 1, Color.WHITE, Color.BLACK);
        Bitmap[] same = ReflectionHelpers.getField(box, "backgrounds");
        for (int i = 0; i < first.length; i++) assertSame(first[i], same[i]);
        box.prepare(bounds, 19, 1, Color.RED, Color.BLACK);
        assertNotSame(first[0], ((Bitmap[]) ReflectionHelpers.getField(box, "backgrounds"))[0]);
        bounds.inset(.125f, .25f); box.prepare(bounds, 19, 1, Color.RED, Color.BLACK);
        Bitmap[] moved = ReflectionHelpers.getField(box, "backgrounds");
        for (int i = 0; i < first.length; i++) assertNotSame(first[i], moved[i]);
        box.clear(); for (Bitmap image : (Bitmap[]) ReflectionHelpers.getField(box, "backgrounds")) assertNull(image);
        for (RectF small : new RectF[] {new RectF(), new RectF(0, 0, 1, 1), new RectF(.25f, .25f, 5.5f, 10.25f)}) {
            box.prepare(small, 19, 1, Color.TRANSPARENT, Color.BLACK);
            Bitmap image = Bitmap.createBitmap(12, 12, Bitmap.Config.ARGB_8888); box.draw(new Canvas(image)); image.recycle();
        }
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
            controller.get().setContentView(composer); layout(composer, 360, 109);
            Bitmap image = Bitmap.createBitmap(360, 109, Bitmap.Config.ARGB_8888); composer.draw(new Canvas(image)); image.recycle();
            NativeRoundedBox production = ReflectionHelpers.getField(composer.box, "analyticBorder"); assertNotNull(production);
            assertNotNull(((Bitmap[]) ReflectionHelpers.getField(production, "backgrounds"))[0]);
            controller.get().setContentView(new View(ui.context));
            for (Bitmap tile : (Bitmap[]) ReflectionHelpers.getField(production, "backgrounds")) assertNull(tile);
        }
    }
}
