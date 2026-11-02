package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Paint;
import android.os.Build;
import android.widget.TextView;
import android.graphics.Typeface;
import android.text.Selection;
import android.text.Spannable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.StyleSpan;
import android.view.View;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeTypographyTest {
    @Test public void textAndInputsUseTheSelectedUiLanguageForFontAndHyphenationFallback() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String locale : new String[] {"zh-CN", "en", "ja"}) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                assertEquals(locale, ui.text("Shared text", 14, ui.text).getTextLocale().toLanguageTag());
                assertEquals(locale, ui.field("", false).getTextLocale().toLanguageTag());
            }
        }
    }

    @Test public void staticRobotoWeightsFollowTheCssMatchingOrder() {
        int[][] cases = {{100,100},{200,100},{300,300},{350,300},{400,400},{450,500},{500,500},
                {550,700},{600,700},{650,700},{700,700},{750,900},{800,900},{900,900},{1000,900}};
        for (int[] sample : cases) assertEquals(sample[1], NativeUi.staticFontWeight(sample[0]));
    }

    @Test public void cssWeightFallbackUsesTheSameFontAdvancesAsTheBrowser() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("typographyCases");
        }
        JSONArray samples = new JSONArray();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            for (int i = 0; i < cases.length(); i++) {
                JSONObject item = cases.getJSONObject(i);
                TextView text = ui.text(item.getString("text"), item.getInt("size"), ui.text);
                ui.typography(text, item.getInt("size"), ui.text, item.getInt("weight"));
                assertSame(NativeTypefaces.weight(item.getInt("weight")), text.getTypeface());
                ui.preciseText(text);
                Paint paint = text.getPaint();
                float selected = paint.measureText(item.getString("text"));
                String value = item.getString("text");
                float selectedRun = paint.getRunAdvance(value, 0, value.length(), 0, value.length(), false, value.length());
                boolean accepted = text.setFontVariationSettings("'wght' " + item.getInt("weight"));
                ui.preciseText(text);
                Paint.FontMetrics metrics = text.getPaint().getFontMetrics();
                JSONArray positions = new JSONArray();
                for (int offset = 0; offset <= value.length(); offset++) positions.put(text.getPaint().getRunAdvance(value, 0, value.length(), 0, value.length(), false, offset));
                samples.put(new JSONObject(item.toString()).put("selectedAdvance", selected).put("variationAccepted", accepted)
                        .put("selectedRun", selectedRun).put("advance", text.getPaint().getRunAdvance(value, 0, value.length(), 0, value.length(), false, value.length()))
                        .put("positions", positions).put("ascent", -metrics.ascent).put("descent", metrics.descent));
            }
        }
        Path directory = Path.of(System.getProperty("spool.ui.artifacts"));
        Files.createDirectories(directory);
        Files.writeString(directory.resolve("native-fonts-api" + Build.VERSION.SDK_INT + ".json"), samples.toString(2));
        JSONObject sources = new JSONObject();
        for (String name : new String[] {"Thin", "Light", "Regular", "Medium", "Bold", "Black"}) for (boolean italic : new boolean[] {false, true}) {
            String file = "Roboto-" + (name.equals("Regular") && italic ? "Italic" : name + (italic ? "Italic" : "")) + ".ttf";
            sources.put(file, resourceHash(file));
        }
        sources.put("DroidSansMono.ttf", resourceHash("DroidSansMono.ttf"));
        sources.put("NotoSansCJK-Regular.ttc", resourceHash("NotoSansCJK-Regular.ttc"));
        Files.writeString(directory.resolve("native-fonts-source-api" + Build.VERSION.SDK_INT + ".json"), sources.toString(2));
    }

    @Test public void sharedVariantsKeepLegacySystemFontMetricsWithoutAllocatingOneFacePerView() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String locale : new String[] {"en", "zh-CN", "ja"}) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                for (int weight : new int[] {100, 300, 400, 450, 500, 600, 650, 700, 900}) {
                    int face = NativeUi.staticFontWeight(weight);
                    TextView legacy = new TextView(controller.get());
                    legacy.setTextLocale(java.util.Locale.forLanguageTag(locale));
                    legacy.setTextSize(14);
                    legacy.setTypeface(Build.VERSION.SDK_INT >= 28
                            ? Typeface.create(Typeface.create("sans-serif", Typeface.NORMAL), face, false)
                            : Typeface.create(face == 100 ? "sans-serif-thin" : face == 300 ? "sans-serif-light" : face == 500 ? "sans-serif-medium" : face == 900 ? "sans-serif-black" : "sans-serif", face == 700 ? Typeface.BOLD : Typeface.NORMAL));
                    legacy.setFontVariationSettings("'wght' " + weight);
                    Typeface shared = NativeTypefaces.weight(weight);
                    for (int repeat = 0; repeat < 8; repeat++) {
                        TextView text = ui.text("", 14, ui.text); ui.typography(text, 14, ui.text, weight);
                        assertSame(shared, text.getTypeface());
                        for (String value : new String[] {"Variable font 123", "中文消息", "日本語のメッセージ"}) {
                            assertEquals(legacy.getPaint().measureText(value), text.getPaint().measureText(value), .001f);
                            assertEquals(legacy.getPaint().getFontMetrics().ascent, text.getPaint().getFontMetrics().ascent, .001f);
                            assertEquals(legacy.getPaint().getFontMetrics().descent, text.getPaint().getFontMetrics().descent, .001f);
                        }
                    }
                }
            }
        }
    }

    private String resourceHash(String file) throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/fonts/" + file)) {
            assertNotNull("Native runtime font resource " + file, input);
            byte[] hash = java.security.MessageDigest.getInstance("SHA-256").digest(input.readAllBytes());
            StringBuilder output = new StringBuilder();
            for (byte value : hash) output.append(String.format(java.util.Locale.ROOT, "%02x", value & 255));
            return output.toString();
        }
    }

    @Test public void fractionalLineBoxesPreserveStyledTextAndSelection() throws Exception {
        org.robolectric.RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            String source = String.join("\n", java.util.Collections.nCopies(24, "A styled native line."));
            SpannableString styled = new SpannableString(source);
            styled.setSpan(new StyleSpan(Typeface.BOLD), 2, 8, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            TextView text = ui.text("", 14, ui.text);
            text.setTextIsSelectable(true);
            ui.fixedLineHeight(text, 1.75f);
            text.setText(styled, TextView.BufferType.SPANNABLE);
            controller.get().setContentView(text);
            checkLines(text, 24.5f);
            Selection.setSelection((Spannable) text.getText(), 2, 8);
            checkLines(text, 24.5f);
            assertEquals(2, text.getSelectionStart()); assertEquals(8, text.getSelectionEnd());
            ui.fixedLineHeight(text, 1.2f);
            checkLines(text, 16.8f);
            assertEquals(source, text.getText().toString());
            assertEquals(1, ((Spanned) text.getText()).getSpans(0, source.length(), StyleSpan.class).length);
            assertEquals(1, ((Spanned) text.getText()).getSpans(0, source.length(), NativeLineHeight.class).length);
            assertEquals(2, text.getSelectionStart()); assertEquals(8, text.getSelectionEnd());
            ui.cssLineHeight(text, 1.85f);
            Object document = text.getText();
            checkLines(text, (float) webLineMetrics().getJSONArray("cases").getJSONObject(0).getDouble("measuredHeight"));
            assertSame(document, text.getText());
            assertEquals(2, text.getSelectionStart()); assertEquals(8, text.getSelectionEnd());
        }
    }

    @Test public void explicitCssLineMetricsMatchActualWebStrutsAtFractionalHeadingSizes() throws Exception {
        JSONArray cases = webLineMetrics().getJSONArray("cases");
        assertEquals(6, cases.length());
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            org.robolectric.RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                float scale = controller.get().getResources().getDisplayMetrics().density;
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject item = cases.getJSONObject(i);
                    NativeUi ui = new NativeUi(controller.get(), false, item.getString("locale"));
                    ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                    float size = (float) item.getDouble("size"), lineHeight = (float) item.getDouble("lineHeight");
                    TextView text = ui.text(item.getString("text"), Math.round(size), ui.text);
                    ui.typography(text, Math.round(size), ui.text, item.getInt("weight")); text.setTextSize(size);
                    ui.preciseText(text); ui.cssLineHeight(text, lineHeight / size);
                    text.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(324), View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(ui.dp(200), View.MeasureSpec.AT_MOST));
                    text.layout(0, 0, text.getMeasuredWidth(), text.getMeasuredHeight());
                    assertEquals(item.getString("id") + "/" + density, Math.round(item.getDouble("baselineOffset") * scale), text.getLayout().getLineBaseline(0));
                    assertEquals(1, text.getLineCount());
                    assertEquals(Math.round(item.getDouble("measuredHeight") * scale), text.getMeasuredHeight());
                    assertEquals(item.getDouble("measuredHeight") * scale, NativeUi.cssTextHeight(text), .0001);
                }
            }
        }
    }

    private JSONObject webLineMetrics() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-line-metrics.json")) {
            assertNotNull(input);
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    private void checkLines(TextView text, float height) {
        text.measure(View.MeasureSpec.makeMeasureSpec(320, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(4000, View.MeasureSpec.AT_MOST));
        text.layout(0, 0, text.getMeasuredWidth(), text.getMeasuredHeight());
        assertEquals(24, text.getLineCount());
        assertEquals(Math.round(height * 24), text.getMeasuredHeight());
        for (int i = 0; i < 24; i++) assertEquals(height * i, text.getLayout().getLineBaseline(i) - text.getLayout().getLineBaseline(0), 1);
    }
}
