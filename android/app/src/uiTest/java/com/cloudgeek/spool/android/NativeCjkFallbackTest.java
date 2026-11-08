package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.os.Build;
import android.text.Selection;
import android.text.Spannable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.TextPaint;
import android.text.style.ForegroundColorSpan;
import android.widget.TextView;
import java.io.File;
import java.nio.file.Files;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeCjkFallbackTest {
    static NativeCjkFallback runtimeFonts() {
        // Robolectric relocates the real fontconfig/font files. Production reads /system/etc/fonts.xml.
        new Paint();
        File directory = new File(System.getProperty("robolectric.nativeruntime.fontdir"));
        assertTrue(directory.isDirectory()); assertTrue(new File(directory, "fonts.xml").isFile());
        return new NativeCjkFallback(new File(directory, "fonts.xml"), directory);
    }

    private TextView measure(NativeUi ui, String value, int size, int weight, boolean fixed) {
        TextView text = ui.text(value, size, ui.text); ui.typography(text, size, ui.text, weight);
        ui.normalFallbackLineSpacing(text);
        if (fixed) ui.fixedLineHeight(text, 1.5f);
        text.measure(android.view.View.MeasureSpec.makeMeasureSpec(200, android.view.View.MeasureSpec.EXACTLY),
                android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED));
        text.layout(0, 0, text.getMeasuredWidth(), text.getMeasuredHeight()); return text;
    }

    @Test public void configuredLocaleFacesUseTheOriginalTtcIndexAndActualFontMetrics() throws Exception {
        NativeCjkFallback fonts = runtimeFonts();
        File directory = new File(System.getProperty("robolectric.nativeruntime.fontdir"));
        for (String file : new String[] {"fonts.xml", "NotoSansCJK-Regular.ttc"}) {
            try (java.io.InputStream source = getClass().getResourceAsStream("/fonts/" + file)) {
                assertNotNull(source); assertArrayEquals(source.readAllBytes(), Files.readAllBytes(new File(directory, file).toPath()));
            }
        }
        for (String locale : new String[] {"zh-CN", "ja"}) {
            Typeface face = fonts.face(locale, 400); assertNotNull(face); assertSame(face, fonts.face(locale, 400));
            TextPaint actual = new TextPaint(); actual.setTypeface(face); actual.setTextSize(12);
            Typeface expected = new Typeface.Builder(new File(directory, "NotoSansCJK-Regular.ttc"))
                    .setTtcIndex(locale.equals("ja") ? 0 : 2).setWeight(400).setItalic(false).build();
            TextPaint reference = new TextPaint(); reference.setTypeface(expected); reference.setTextSize(12);
            assertEquals(reference.getFontMetrics().ascent, actual.getFontMetrics().ascent, 0f);
            assertEquals(reference.getFontMetrics().descent, actual.getFontMetrics().descent, 0f);
            assertEquals(reference.measureText("骨直令"), actual.measureText("骨直令"), 0f);
            TextPaint latin = new TextPaint(); latin.setTypeface(Typeface.create("sans-serif", Typeface.NORMAL)); latin.setTextSize(12);
            assertTrue(actual.getFontMetrics().descent - actual.getFontMetrics().ascent > latin.getFontMetrics().descent - latin.getFontMetrics().ascent);
        }
        assertNull(fonts.face("en", 400));
    }

    @Test public void fallbackIsAppliedToCjkRunsNotLatinAndTextChangesKeepOtherSpansAndSelection() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "ja"); ui.cjkFonts = runtimeFonts();
            TextView text = measure(ui, "ABC 日本語。XYZ", 12, 500, false);
            controller.get().setContentView(text);
            text.setTextIsSelectable(true); text.setText(text.getText(), TextView.BufferType.SPANNABLE);
            Spannable source = (Spannable) text.getText();
            source.setSpan(new ForegroundColorSpan(ui.primary), 0, 3, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            Selection.setSelection(source, 1, 6);
            ui.typography(text, 12, ui.text, 700); ui.normalFallbackLineSpacing(text);
            assertEquals("ABC 日本語。XYZ", text.getText().toString());
            if (Build.VERSION.SDK_INT == 26) { assertEquals(1, text.getSelectionStart()); assertEquals(6, text.getSelectionEnd()); }
            assertEquals(1, ((Spanned) text.getText()).getSpans(0, text.length(), ForegroundColorSpan.class).length);
            if (Build.VERSION.SDK_INT == 26) {
                Spanned spans = (Spanned) text.getText();
                NativeCjkFallback.FaceSpan[] faces = spans.getSpans(0, text.length(), NativeCjkFallback.FaceSpan.class);
                assertEquals(1, faces.length); assertEquals(4, spans.getSpanStart(faces[0])); assertEquals(8, spans.getSpanEnd(faces[0]));
                assertEquals(0, spans.getSpans(0, 3, NativeCjkFallback.FaceSpan.class).length);
            }
            text.setText("Latin only", TextView.BufferType.SPANNABLE);
            assertEquals(0, ((Spanned) text.getText()).getSpans(0, text.length(), NativeCjkFallback.FaceSpan.class).length);
        }
    }

    @Test public void malformedMissingOrEscapingFontFilesRetainThePlatformTypeface() throws Exception {
        File directory = Files.createTempDirectory(new File(System.getProperty("java.io.tmpdir")).toPath(), "font-config-").toFile();
        File config = new File(directory, "fonts.xml");
        try {
            for (String xml : new String[] {"<familyset><family", "<familyset><family lang='ja'><font>../NotoSansCJK-Regular.ttc</font></family></familyset>",
                    "<familyset><family lang='ja'><font index='invalid'>missing.ttf</font></family></familyset>",
                    "<familyset><family lang='ja'><font index='-1'>missing.ttf</font></family></familyset>",
                    "<familyset><family lang='ja'><font>missing.ttf<axis tag='wght' stylevalue='NaN'/></font></family></familyset>",
                    "<familyset><family name='not-a-fallback' lang='ja'><font>missing.ttf</font></family></familyset>"}) {
                Files.writeString(config.toPath(), xml);
                NativeCjkFallback fonts = new NativeCjkFallback(config, directory);
                assertNull(fonts.face("ja", 400));
                assertEquals("日本語", fonts.apply("日本語", "ja", 400).toString());
                assertEquals(0, ((Spanned) fonts.apply("日本語", "ja", 400)).getSpans(0, 3, NativeCjkFallback.FaceSpan.class).length);
            }
        } finally { Files.deleteIfExists(config.toPath()); Files.delete(directory.toPath()); }
    }

    @Test public void configuredFamilyOrderAxesAndAllowedRootsAreNotReplacedByAFontNameGuess() throws Exception {
        NativeCjkFallback source = runtimeFonts(); assertNotNull(source.face("ja", 400));
        File fonts = new File(System.getProperty("robolectric.nativeruntime.fontdir"));
        File scratch = Files.createTempDirectory(new File(System.getProperty("java.io.tmpdir")).toPath(), "font-family-").toFile();
        File config = new File(scratch, "fonts.xml");
        try {
            String first = "<family lang='ja'><font index='0' weight='400'>NotoSansCJK-Regular.ttc</font></family>";
            String later = "<family lang='ja'><font weight='700'>Roboto-Bold.ttf</font></family>";
            Files.writeString(config.toPath(), "<familyset>" + first + later + "</familyset>");
            NativeCjkFallback selected = new NativeCjkFallback(config, fonts);
            Paint actual = new Paint(); actual.setTypeface(selected.face("ja", 700)); actual.setTextSize(14);
            Paint expected = new Paint(); expected.setTypeface(source.face("ja", 700)); expected.setTextSize(14);
            assertEquals(expected.getFontMetrics().ascent, actual.getFontMetrics().ascent, 0f);
            assertEquals(expected.getFontMetrics().descent, actual.getFontMetrics().descent, 0f);
            Files.writeString(config.toPath(), "<familyset><family lang='ja'><font>" + new File(fonts, "NotoSansCJK-Regular.ttc").getAbsolutePath() + "</font></family></familyset>");
            assertNull(new NativeCjkFallback(config, scratch).face("ja", 400));
            Files.writeString(config.toPath(), "<!DOCTYPE familyset [<!ENTITY font SYSTEM 'file:///etc/passwd'>]><familyset><family lang='ja'><font>&font;</font></family></familyset>");
            assertNull(new NativeCjkFallback(config, fonts).face("ja", 400));
        } finally { Files.deleteIfExists(config.toPath()); Files.delete(scratch.toPath()); }
    }

    @Test public void cjkMarksAndSupplementaryCharactersStayInTheSameShapingRun() {
        NativeCjkFallback fonts = runtimeFonts();
        String value = "A\uD840\uDC00\uDB40\uDD00か\u3099・ーZ";
        Spanned text = (Spanned) fonts.apply(value, "ja", 400);
        NativeCjkFallback.FaceSpan[] spans = text.getSpans(0, text.length(), NativeCjkFallback.FaceSpan.class);
        assertEquals(value, text.toString()); assertEquals(1, spans.length);
        assertEquals(1, text.getSpanStart(spans[0])); assertEquals(value.length() - 1, text.getSpanEnd(spans[0]));
    }

    @Test @Config(sdk = 35) public void localesWithoutCustomFacesPreservePlainTextAndExistingStyles() {
        NativeCjkFallback fonts = runtimeFonts();
        String plain = "Read 中文の結果 👩🏽‍💻";
        SpannableString styled = new SpannableString(plain);
        ForegroundColorSpan color = new ForegroundColorSpan(0xff123456);
        styled.setSpan(color, 5, 7, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        for (String locale : new String[] {"en", "en-US", "ar", "ko"}) {
            assertSame(plain, fonts.apply(plain, locale, 400));
            assertSame(plain, fonts.applyCss(plain, locale, 400));
            assertSame(styled, fonts.applyCss(styled, locale, 400));
            assertEquals(5, styled.getSpanStart(color)); assertEquals(7, styled.getSpanEnd(color));
        }
        assertTrue("The A/B reference retains the former empty span wrapper", fonts.applyCss(plain, "en", 400, false) instanceof Spanned);
    }

    @Test public void switchingAwayFromCustomLocaleRemovesItsFaceButRetainsOtherSpans() {
        NativeCjkFallback fonts = runtimeFonts();
        for (String locale : new String[] {"ja", "zh-CN"}) {
            SpannableString source = new SpannableString("中文と日本語");
            ForegroundColorSpan color = new ForegroundColorSpan(0xff123456);
            source.setSpan(color, 0, 2, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            Spanned localized = (Spanned) fonts.apply(source, locale, 400);
            assertTrue(localized.getSpans(0, localized.length(), NativeCjkFallback.FaceSpan.class).length > 0);
            Spanned english = (Spanned) fonts.applyCss(localized, "en", 400);
            assertEquals(0, english.getSpans(0, english.length(), NativeCjkFallback.FaceSpan.class).length);
            assertEquals(0, english.getSpanStart(color)); assertEquals(2, english.getSpanEnd(color));
            assertEquals(source.toString(), english.toString());
        }
    }

    @Test public void cssWeightOrderAndGenuineItalicFacesAreNotReplacedBySyntheticStyles() throws Exception {
        runtimeFonts(); File fonts = new File(System.getProperty("robolectric.nativeruntime.fontdir"));
        File scratch = Files.createTempDirectory(new File(System.getProperty("java.io.tmpdir")).toPath(), "font-style-").toFile();
        File config = new File(scratch, "fonts.xml");
        try {
            Files.writeString(config.toPath(), "<familyset><family lang='ja'>"
                    + "<font weight='400'>Roboto-Regular.ttf</font><font weight='500'>Roboto-Medium.ttf</font>"
                    + "<font weight='700'>Roboto-Bold.ttf</font><font weight='400' style='italic'>Roboto-Italic.ttf</font>"
                    + "<font weight='900'>Roboto-Black.ttf</font>"
                    + "</family></familyset>");
            NativeCjkFallback source = new NativeCjkFallback(config, fonts);
            Paint chosen = new Paint(); chosen.setTextSize(14);
            Paint expected = new Paint(); expected.setTextSize(14);
            for (int[] sample : new int[][] {{350,400}, {400,400}, {450,500}, {500,500}, {550,700}, {650,700}, {800,900}}) {
                chosen.setTypeface(source.face("ja", sample[0]));
                expected.setTypeface(new Typeface.Builder(new File(fonts, sample[1] == 400 ? "Roboto-Regular.ttf" : sample[1] == 500 ? "Roboto-Medium.ttf" : sample[1] == 700 ? "Roboto-Bold.ttf" : "Roboto-Black.ttf")).setWeight(sample[1]).setItalic(false).build());
                assertEquals("CSS weight matching at " + sample[0], expected.measureText("Hamburgefontsiv"), chosen.measureText("Hamburgefontsiv"), 0f);
            }
            SpannableString styled = new SpannableString("日本語"); styled.setSpan(new android.text.style.StyleSpan(Typeface.ITALIC), 0, 3, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            Spanned rendered = (Spanned) source.applyCss(styled, "ja", 400);
            TextPaint paint = new TextPaint(); paint.setTextSize(14); paint.setFakeBoldText(true); paint.setTextSkewX(-.25f);
            for (android.text.style.MetricAffectingSpan span : rendered.getSpans(0, 3, android.text.style.MetricAffectingSpan.class)) span.updateDrawState(paint);
            assertTrue("A configured real italic face must stay italic", paint.getTypeface().isItalic());
            assertFalse(paint.isFakeBoldText()); assertEquals(0f, paint.getTextSkewX(), 0f);
            styled = new SpannableString("日本語"); styled.setSpan(new android.text.style.StyleSpan(Typeface.BOLD), 0, 3, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            rendered = (Spanned) source.applyCss(styled, "ja", 650);
            NativeCjkFallback.FaceSpan bold = rendered.getSpans(0, 3, NativeCjkFallback.FaceSpan.class)[0];
            chosen.setTypeface(bold.face); expected.setTypeface(new Typeface.Builder(new File(fonts, "Roboto-Black.ttf")).setWeight(900).build());
            assertEquals("A strong child of CSS weight650 resolves to900", expected.measureText("Hamburgefontsiv"), chosen.measureText("Hamburgefontsiv"), 0f);
            Files.writeString(config.toPath(), "<familyset><family lang='ja'><font weight='400'>Roboto-Regular.ttf<axis tag='wght' stylevalue='400'/></font></family></familyset>");
            source = new NativeCjkFallback(config, fonts);
            assertSame("CSS fallback must defer variable-weight families to native shaping", styled, source.applyCss(styled, "ja", 650));
        } finally { Files.deleteIfExists(config.toPath()); Files.delete(scratch.toPath()); }
    }

    @Test public void explicitCssLineHeightStillOverridesFallbackFaceMetricsWithoutCumulativeDrift() {
        org.robolectric.RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String locale : new String[] {"zh-CN", "ja"}) {
                NativeUi ui = new NativeUi(controller.get(), false, locale); ui.cjkFonts = runtimeFonts();
                String value = String.join("\n", java.util.Collections.nCopies(24, "ABC 中文と日本語。"));
                TextView text = measure(ui, value, 11, 400, true);
                controller.get().setContentView(text);
                assertEquals(24, text.getLineCount()); assertEquals(396, text.getMeasuredHeight());
                for (int i = 0; i < 24; i++) assertEquals(i * 16.5f, text.getLayout().getLineBaseline(i) - text.getLayout().getLineBaseline(0), 1f);
                text.setText("Same replacement 中文");
                assertEquals(1, ((Spanned) text.getText()).getSpans(0, text.length(), NativeLineHeight.class).length);
                if (Build.VERSION.SDK_INT == 26) assertEquals(1, ((Spanned) text.getText()).getSpans(0, text.length(), NativeCjkFallback.FaceSpan.class).length);
            }
        }
    }
}
