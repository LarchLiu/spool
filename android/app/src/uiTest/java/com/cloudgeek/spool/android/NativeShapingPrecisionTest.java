package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Paint;
import android.text.TextPaint;
import android.text.Layout;
import android.view.View;
import android.view.ViewGroup;
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

/** Observe fractional-size shaping separately from line-breaking and glyph raster gates. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeShapingPrecisionTest {
    @Test public void explicitNewlinesKeepTheLongestParagraphsFractionalIntrinsicWidth() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("messageCases");
        }
        JSONArray samples = new JSONArray();
        RuntimeEnvironment.setQualifiers("w412dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (int index : new int[] {4, 7}) {
                JSONObject item = cases.getJSONObject(index);
                NativeUi ui = new NativeUi(controller.get(), false, item.getString("locale"));
                NativeUserRow row = new NativeUserRow(ui, item.optJSONObject("operator"), item.getString("text"), item.optLong("timestamp"), null, ignored -> fail());
                ViewGroup markdown = (ViewGroup) row.message.content.getChildAt(0);
                TextView text = (TextView) markdown.getChildAt(0);
                Object document = text.getText();
                android.text.Selection.setSelection((android.text.Spannable) document, 1, 5);
                controller.get().setContentView(row);
                JSONArray passes = new JSONArray();
                for (int pass = 0; pass < 4; pass++) {
                    row.measure(View.MeasureSpec.makeMeasureSpec(376, View.MeasureSpec.EXACTLY),
                            View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                    row.layout(0, 0, row.getMeasuredWidth(), row.getMeasuredHeight());
                    passes.put(text.getMeasuredWidth());
                }
                JSONArray lines = new JSONArray();
                for (int line = 0; line < text.getLineCount(); line++) lines.put(text.getLayout().getLineMax(line));
                samples.put(new JSONObject().put("id", item.getString("id")).put("measuredWidth", text.getMeasuredWidth())
                        .put("desiredWidth", Layout.getDesiredWidth(text.getText(), text.getPaint())).put("lineWidths", lines).put("passes", passes));
                assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
                int fullLines = text.getLineCount();
                row.measure(View.MeasureSpec.makeMeasureSpec(176, View.MeasureSpec.EXACTLY),
                        View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                row.layout(0, 0, row.getMeasuredWidth(), row.getMeasuredHeight());
                assertTrue("A constrained paragraph must still wrap", text.getLineCount() > fullLines);
                assertEquals("Soft-wrapped text must keep the available content width", 104, text.getMeasuredWidth());
                assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
            }
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-multiline-width-api" + android.os.Build.VERSION.SDK_INT + ".json"), samples.toString(2));
        for (int index = 0; index < samples.length(); index++) {
            JSONObject sample = samples.getJSONObject(index);
            assertEquals(sample.toString(), (int) Math.ceil(sample.getDouble("desiredWidth")), sample.getInt("measuredWidth"));
        }
    }

    @Test public void multilineIntrinsicWidthsPreserveStyledRunsTabsAndNativeConstraintsAtEveryDensity() throws Exception {
        JSONArray fixtures;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            fixtures = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8)).getJSONArray("messageCases");
        }
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w900dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                for (int index : new int[] {4, 5, 7}) {
                    JSONObject fixture = fixtures.getJSONObject(index);
                    NativeUi ui = new NativeUi(controller.get(), false, fixture.getString("locale"));
                    String source = "**Bold** and *emphasis*\n" + fixture.getString("text") + " `code`";
                    NativeUserRow row = new NativeUserRow(ui, null, source, 0, null, ignored -> fail());
                    controller.get().setContentView(row);
                    TextView text = (TextView) ((ViewGroup) row.message.content.getChildAt(0)).getChildAt(0);
                    Object document = text.getText();
                    android.text.Selection.setSelection((android.text.Spannable) document, 1, 5);
                    for (int pass = 0; pass < 3; pass++) {
                        row.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(900), View.MeasureSpec.AT_MOST),
                                View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                        row.layout(0, 0, row.getMeasuredWidth(), row.getMeasuredHeight());
                        assertEquals(density + "/" + fixture.getString("id"),
                                (int) Math.ceil(Layout.getDesiredWidth(text.getText(), text.getPaint())), text.getMeasuredWidth());
                        assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(5, text.getSelectionEnd());
                    }
                }
                NativeUi ui = new NativeUi(controller.get(), false);
                String value = "Column\tValue\nOne\tTwo";
                TextView nativeText = ui.text(value, 14, ui.text), platform = new TextView(ui.context);
                nativeText.setLayoutParams(new ViewGroup.LayoutParams(-2, -2));
                platform.setLayoutParams(new ViewGroup.LayoutParams(-2, -2));
                ui.typography(platform, 14, ui.text, 400); platform.setText(value);
                ui.preciseText(nativeText, true); ui.preciseText(platform, true);
                nativeText.setTextIsSelectable(true); platform.setTextIsSelectable(true);
                for (int minimum : new int[] {0, ui.dp(300)}) {
                    nativeText.setMinWidth(minimum); platform.setMinWidth(minimum);
                    for (TextView text : new TextView[] {nativeText, platform}) {
                        text.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(600), View.MeasureSpec.AT_MOST),
                                View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                    }
                    assertTrue(nativeText.getLayout().getLineContainsTab(0));
                    assertEquals("Native tab stops and min width must not be replaced with plain glyph advances",
                            platform.getMeasuredWidth(), nativeText.getMeasuredWidth());
                }
                nativeText.setText("A paragraph of ordinary words\nSecond line");
                nativeText.setMinWidth(0); nativeText.setMaxWidth(ui.dp(80));
                nativeText.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(600), View.MeasureSpec.AT_MOST),
                        View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                assertEquals("The platform max-width cap must still wrap multiline text", ui.dp(80), nativeText.getMeasuredWidth());
                nativeText.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(137), View.MeasureSpec.EXACTLY),
                        View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                assertEquals(ui.dp(137), nativeText.getMeasuredWidth());
            }
        }
    }

    @Test public void fractionalRunAdvancesKeepEveryAsciiCaretBoundary() throws Exception {
        JSONArray samples = new JSONArray();
        String value = "across several native text lines while";
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h600dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, "en");
                float scale = ui.context.getResources().getDisplayMetrics().density;
                TextView text = ui.text(value, 14, ui.text); ui.preciseText(text, true);
                for (float size : new float[] {12.875f, 12.88f, 13f}) {
                    text.setTextSize(size);
                    for (String mode : new String[] {"production", "hinting-off", "linear-off", "no-features"}) {
                        TextPaint paint = new TextPaint(text.getPaint());
                        if (mode.equals("hinting-off")) paint.setHinting(Paint.HINTING_OFF);
                        if (mode.equals("linear-off")) paint.setLinearText(false);
                        if (mode.equals("no-features")) paint.setFontFeatureSettings(null);
                        JSONArray positions = new JSONArray();
                        float previous = -1;
                        for (int i = 0; i <= value.length(); i++) {
                            float position = paint.getRunAdvance(value, 0, value.length(), 0, value.length(), false, i);
                            assertTrue("Each ASCII caret must retain its own horizontal position", position > previous);
                            positions.put(position); previous = position;
                        }
                        float[] advances = new float[value.length()];
                        float total = paint.getTextRunAdvances(value.toCharArray(), 0, value.length(), 0, value.length(), false, advances, 0);
                        assertEquals(previous, total, .0001f);
                        samples.put(new JSONObject().put("density", scale).put("size", size).put("paintSize", paint.getTextSize())
                                .put("mode", mode).put("text", value).put("flags", paint.getFlags()).put("hinting", paint.getHinting())
                                .put("advance", total).put("positions", positions).put("measureText", paint.measureText(value)));
                    }
                }
            }
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-shaping-precision-api" + android.os.Build.VERSION.SDK_INT + ".json"), samples.toString(2));
    }
}
