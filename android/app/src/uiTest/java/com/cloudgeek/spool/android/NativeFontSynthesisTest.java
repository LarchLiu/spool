package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.text.Selection;
import android.text.Spannable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.TextPaint;
import android.text.style.MetricAffectingSpan;
import android.text.style.StyleSpan;
import android.view.View;
import android.widget.TextView;
import java.io.File;
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
public class NativeFontSynthesisTest {
    private void layout(TextView view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void cssCjkHeadersUseTheAvailableRealFaceWithoutSyntheticWeightOrSlant() throws Exception {
        JSONArray samples = new JSONArray(); int maximum = 0, outside = 0;
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeCjkFallback fonts = NativeCjkFallbackTest.runtimeFonts();
                for (String locale : new String[] {"zh-CN", "ja"}) {
                    NativeUi ui = new NativeUi(controller.get(), false, locale); ui.cjkFonts = fonts;
                    String value = locale.equals("ja") ? "名前と数値" : "名称与数值";
                    TextView text = ui.text(value, 13, ui.text); ui.typography(text, 13, ui.text, 650);
                    text.setTextSize(12.88f); ui.cssLineHeight(text, 1.85f); ui.preciseText(text);
                    text.setTextIsSelectable(true); controller.get().setContentView(text);
                    int width = ui.dp(180), height = ui.dp(60); layout(text, width, height);
                    Bitmap actual = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                    Bitmap expected = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                    text.draw(new Canvas(actual));
                    Paint real = new Paint(text.getPaint()); real.setTypeface(fonts.face(locale, 400)); real.setFakeBoldText(false); real.setTextSkewX(0);
                    new Canvas(expected).drawTextRun(value, 0, value.length(), 0, value.length(), text.getTotalPaddingLeft(), text.getBaseline(), false, real);
                    int count = 0, max = 0;
                    for (int y = 0; y < height; y++) for (int x = 0; x < width; x++) {
                        int a = actual.getPixel(x, y), b = expected.getPixel(x, y), delta = 0;
                        for (int shift : new int[] {0, 8, 16, 24}) delta = Math.max(delta, Math.abs((a >>> shift & 255) - (b >>> shift & 255)));
                        max = Math.max(max, delta); if (delta > 1) count++;
                    }
                    maximum = Math.max(maximum, max); outside += count;
                    samples.put(new JSONObject().put("density", density).put("locale", locale).put("maximum", max).put("outsideTolerance", count));
                    if (density.equals("420dpi")) for (boolean reference : new boolean[] {false, true}) {
                        try (java.io.OutputStream stream = Files.newOutputStream(output.resolve("native-cjk-synthesis-api" + android.os.Build.VERSION.SDK_INT + "-" + locale + (reference ? "-real-face" : "") + ".png"))) {
                            assertTrue((reference ? expected : actual).compress(Bitmap.CompressFormat.PNG, 100, stream));
                        }
                    }
                    actual.recycle(); expected.recycle();
                    Object document = text.getText(); Selection.setSelection((Spannable) document, 1, value.length() - 1);
                    layout(text, ui.dp(220), height);
                    assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(value.length() - 1, text.getSelectionEnd());
                }
            }
        }
        Files.writeString(output.resolve("native-cjk-synthesis-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject()
                .put("purpose", "Production native text versus direct real-font paint; not a Web/device raster gate")
                .put("cases", samples).put("maximum", maximum).put("outsideTolerance", outside).toString(2));
        assertEquals("Unavailable CJK bold must not be synthesized", 0, outside);
    }

    @Test public void inlineCjkStylesKeepLatinSpansAndNativeSelectionWithoutInventingFontFaces() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "ja"); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
            String value = "Latin 日本語 italic 仮名 end";
            SpannableString styled = new SpannableString(value);
            styled.setSpan(new StyleSpan(Typeface.BOLD), 0, 9, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            styled.setSpan(new StyleSpan(Typeface.ITALIC), 10, 19, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            TextView text = ui.text("", 14, ui.text); ui.cssLineHeight(text, 1.85f);
            text.setText(styled, TextView.BufferType.SPANNABLE); text.setTextIsSelectable(true);
            controller.get().setContentView(text); layout(text, ui.dp(320), ui.dp(80));
            Spanned document = (Spanned) text.getText();
            assertEquals(value, document.toString()); assertEquals(2, document.getSpans(0, value.length(), StyleSpan.class).length);
            for (int offset : new int[] {6, 17}) {
                TextPaint paint = new TextPaint(text.getPaint());
                for (MetricAffectingSpan span : document.getSpans(offset, offset + 1, MetricAffectingSpan.class)) span.updateDrawState(paint);
                assertEquals(Typeface.NORMAL, paint.getTypeface().getStyle()); assertFalse(paint.isFakeBoldText()); assertEquals(0f, paint.getTextSkewX(), 0f);
            }
            TextPaint latin = new TextPaint(text.getPaint());
            for (MetricAffectingSpan span : document.getSpans(0, 1, MetricAffectingSpan.class)) span.updateDrawState(latin);
            assertTrue(latin.getTypeface().isBold());
            Selection.setSelection((Spannable) document, 2, 18); layout(text, ui.dp(220), ui.dp(100));
            assertSame(document, text.getText()); assertEquals(2, text.getSelectionStart()); assertEquals(18, text.getSelectionEnd());
        }
    }

    @Test public void actualWebFontFixturesRenderThroughProductionMarkdownWithASeparateMissingConfigControl() throws Exception {
        JSONObject reference;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-cjk-fonts.json")) {
            assertNotNull(input); reference = new JSONObject(new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        JSONArray cases = reference.getJSONArray("cases"); assertEquals(6, cases.length());
        for (int i = 0; i < cases.length(); i++) {
            JSONObject fixture = cases.getJSONObject(i); float density = (float) fixture.getDouble("density");
            RuntimeEnvironment.setQualifiers("w360dp-h320dp-" + (density == 1 ? "mdpi" : density == 3 ? "xxhdpi" : "420dpi"));
            for (boolean control : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), false, fixture.getString("locale"));
                    ui.cjkFonts = control ? new NativeCjkFallback(new File(System.getProperty("java.io.tmpdir"), "missing-font-config.xml")) : NativeCjkFallbackTest.runtimeFonts();
                    NativeMarkdownFlow page = new NativeMarkdownFlow(ui); page.collapseTop = page.collapseBottom = false;
                    page.inset(18 * density, 25 * density, 18 * density, 0); page.setBackgroundColor(ui.elevated);
                    page.add(NativeMarkdown.view(ui, fixture.getString("source"), false, ignored -> fail("No links may open during rendering")), 0, 0);
                    controller.get().setContentView(page);
                    int width = ui.dp(360), height = ui.dp(320);
                    page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
                    page.layout(0, 0, width, height);
                    Bitmap image = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                    try (java.io.OutputStream stream = Files.newOutputStream(output.resolve("native-api" + android.os.Build.VERSION.SDK_INT + "-cjk-synthesis-"
                            + fixture.getString("locale") + "-dpr" + (density == 1 ? "1" : density == 3 ? "3" : "2.625") + (control ? "-missing-config-control" : "") + ".png"))) {
                        page.draw(new Canvas(image)); assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                    } finally { image.recycle(); }
                }
            }
        }
    }
}
