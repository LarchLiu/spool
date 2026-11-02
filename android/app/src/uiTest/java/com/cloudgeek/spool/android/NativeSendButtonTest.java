package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.animation.ValueAnimator;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.RectF;
import android.view.View;
import android.widget.LinearLayout;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
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
public class NativeSendButtonTest {
    private void layout(View root, int width, int height) {
        root.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, width, height);
    }
    private JSONObject box(float x, float y, float width, float height) throws Exception {
        return new JSONObject().put("x", x).put("y", y).put("width", width).put("height", height);
    }

    @Test public void originalGlyphsDisabledColorsAnd800msSpinMatchProductionWebButton() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("sendButtonCases");
        }
        int api = android.os.Build.VERSION.SDK_INT;
        File directory = new File(System.getProperty("spool.ui.artifacts")); assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark, locale);
                    LinearLayout page = ui.padded(24, 24); page.setBackgroundColor(ui.elevated);
                    List<NativeUi.IconButton> buttons = new ArrayList<>();
                    for (int i = 0; i < cases.length(); i++) {
                        JSONObject item = cases.getJSONObject(i); boolean active = item.getBoolean("active"), stopping = item.getBoolean("stopping");
                        NativeUi.IconButton button = ui.sendButton(ui.t(active ? stopping ? "Stopping…" : "Stop response" : "Send message"), () -> {});
                        button.setKind(active ? stopping ? "spinner" : "stop" : "send"); button.setEnabled(!item.getBoolean("disabled"));
                        LinearLayout row = ui.row(); row.addView(button); page.addView(row, new LinearLayout.LayoutParams(-1, 56)); buttons.add(button);
                    }
                    controller.get().setContentView(page); layout(page, width, 328);
                    JSONArray samples = new JSONArray();
                    for (int time : new int[] {0, 200, 400, 600}) {
                        for (NativeUi.IconButton button : buttons) {
                            ValueAnimator spin = ReflectionHelpers.getField(button, "spin");
                            if (spin != null) { assertEquals(800, spin.getDuration()); spin.setCurrentPlayTime(time); }
                        }
                        Bitmap image = Bitmap.createBitmap(width, 328, Bitmap.Config.ARGB_8888); page.draw(new Canvas(image));
                        JSONArray states = new JSONArray();
                        for (int i = 0; i < buttons.size(); i++) {
                            NativeUi.IconButton button = buttons.get(i);
                            int x = page.getPaddingLeft(), y = page.getPaddingTop() + i * 56 + (56 - button.getHeight()) / 2;
                            // Rows are center-aligned like native controls; the fixture aligns Web buttons identically.
                            RectF glyph = new RectF(button.getDrawable().getBounds()); button.getImageMatrix().mapRect(glyph);
                            glyph.offset(x + button.getPaddingLeft(), y + button.getPaddingTop());
                            states.put(new JSONObject().put("id", cases.getJSONObject(i).getString("id"))
                                    .put("button", box(x, y, button.getWidth(), button.getHeight())).put("glyph", box(glyph.left, glyph.top, glyph.width(), glyph.height()))
                                    .put("label", button.getContentDescription().toString()).put("enabled", button.isEnabled())
                                    .put("background", image.getPixel(x + 16, y + 2)).put("angle", (float) ReflectionHelpers.getField(button, "glyphRotation")));
                        }
                        samples.put(new JSONObject().put("time", time).put("states", states));
                        try (FileOutputStream out = new FileOutputStream(new File(directory, "native-api" + api + "-send-button-" + width + (dark ? "-dark-" : "-light-") + locale + "-" + time + ".png"))) {
                            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, out));
                        }
                        image.recycle();
                    }
                    Files.writeString(new File(directory, "native-api" + api + "-send-button-" + width + (dark ? "-dark-" : "-light-") + locale + ".json").toPath(), samples.toString(2));
                }
            }
        }
    }

    @Test public void onlyTheSpinnerGlyphRotatesAndItsAnimationStopsWhenHiddenOrDetached() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); NativeUi.IconButton button = ui.sendButton("Stopping", () -> {});
            button.setKind("spinner"); assertNull(ReflectionHelpers.getField(button, "spin"));
            LinearLayout page = ui.column(); page.addView(button); controller.get().setContentView(page); layout(page, 360, 800);
            ValueAnimator spin = ReflectionHelpers.getField(button, "spin"); assertNotNull(spin); spin.setCurrentPlayTime(200);
            assertEquals(90f, (float) ReflectionHelpers.getField(button, "glyphRotation"), .001f); assertEquals(0f, button.getRotation(), 0);
            button.setVisibility(View.GONE); assertNull(ReflectionHelpers.getField(button, "spin")); assertFalse(spin.isRunning());
            button.setVisibility(View.VISIBLE); assertNotNull(ReflectionHelpers.getField(button, "spin"));
            button.setKind("stop"); assertNull(ReflectionHelpers.getField(button, "spin"));
            button.setKind("spinner"); page.removeView(button); assertNull(ReflectionHelpers.getField(button, "spin"));
        }
    }
}
