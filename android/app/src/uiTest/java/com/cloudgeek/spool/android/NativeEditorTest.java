package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.os.Build;
import android.text.Layout;
import android.view.View;
import android.widget.LinearLayout;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.file.Files;
import java.nio.charset.StandardCharsets;
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
public class NativeEditorTest {
    private void layout(View page, int width, int height) {
        page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        page.layout(0, 0, width, height);
    }

    @Test public void recordProductionEditorBaselinesAndVisibleLineBoxes() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        JSONArray samples = new JSONArray();
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("editorCases");
        }
        for (int width : new int[] {320, 360, 412, 900}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                LinearLayout page = ui.workspace();
                page.addView(new View(ui.context), new LinearLayout.LayoutParams(-1, 0, 1));
                NativeUi.Composer composer = ui.new Composer(ui.t("Message {agent}", "agent", "Codex"), () -> {}, () -> {}, () -> {}, () -> {});
                composer.model.bind("gpt-6.1-codex", "codex", "Choose model");
                composer.permission.bind("Ask for approval", "shield-question", "Permissions");
                page.addView(composer); controller.get().setContentView(page);
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject item = cases.getJSONObject(i);
                    if (!locale.equals(item.getString("locale"))) continue;
                    String id = item.getString("id"), value = item.getString("value");
                    composer.input.setText(value);
                    layout(page, width, 800);
                    Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888);
                    page.draw(new Canvas(image));
                    try (FileOutputStream output = new FileOutputStream(new File(directory,
                            "native-editor-api" + Build.VERSION.SDK_INT + "-" + width + "-" + id + ".png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                    }
                    image.recycle();
                    Layout layout = value.isEmpty() ? ReflectionHelpers.getField(composer.input, "mHintLayout") : composer.input.getLayout();
                    assertNotNull(layout);
                    Paint.FontMetrics metrics = composer.input.getPaint().getFontMetrics();
                    JSONArray lines = new JSONArray();
                    int drawTop = value.isEmpty() ? composer.input.getExtendedPaddingTop()
                            + Math.max(0, (composer.input.getHeight() - composer.input.getCompoundPaddingTop()
                                    - composer.input.getCompoundPaddingBottom() - layout.getHeight()) / 2) : composer.input.getTotalPaddingTop();
                    for (int line = 0; line < layout.getLineCount(); line++) {
                        int start = layout.getLineStart(line), end = layout.getLineEnd(line);
                        while (end > start && (layout.getText().charAt(end - 1) == '\n' || layout.getText().charAt(end - 1) == '\r')) end--;
                        float advance = end == layout.getLineEnd(line) && end < layout.getText().length()
                                ? layout.getLineWidth(line) : Math.abs(layout.getPrimaryHorizontal(end) - layout.getPrimaryHorizontal(start));
                        lines.put(new JSONObject().put("start", layout.getLineStart(line)).put("end", layout.getLineEnd(line))
                                .put("text", layout.getText().subSequence(start, end).toString())
                                .put("baseline", drawTop + layout.getLineBaseline(line) - composer.input.getScrollY())
                                .put("top", drawTop + layout.getLineTop(line) - composer.input.getScrollY())
                                .put("bottom", drawTop + layout.getLineBottom(line) - composer.input.getScrollY())
                                .put("x", composer.input.getTotalPaddingLeft() + layout.getLineLeft(line))
                                .put("width", advance)
                                .put("layoutWidth", layout.getLineWidth(line)));
                    }
                    samples.put(new JSONObject().put("width", width).put("id", id).put("value", value).put("locale", locale)
                            .put("editor", new JSONObject().put("width", composer.input.getWidth()).put("height", composer.input.getHeight())
                                    .put("fontSize", composer.input.getTextSize()).put("paddingTop", composer.input.getPaddingTop())
                                    .put("paddingBottom", composer.input.getPaddingBottom()).put("paddingLeft", composer.input.getPaddingLeft())
                                    .put("scrollTop", composer.input.getScrollY()).put("baseline", composer.input.getBaseline())
                                    .put("ascent", -metrics.ascent).put("descent", metrics.descent)
                                    .put("flags", composer.input.getPaint().getFlags()).put("fontVariation", composer.input.getFontVariationSettings())
                                    .put("hint", composer.input.getHint().toString()))
                            .put("lines", lines));
                }
            }
        }
        Files.writeString(new File(directory, "native-editor-text-api" + Build.VERSION.SDK_INT + ".json").toPath(), samples.toString(2));
    }

    @Test public void editableLineBoxesPreserveDocumentCompositionSelectionAndStylesAcrossFontBreakpoints() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
            RuntimeEnvironment.setQualifiers("w900dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                NativeUi.Composer composer = ui.new Composer(ui.t("Message {agent}", "agent", "Codex"), () -> {}, () -> {}, () -> {}, () -> {});
                controller.get().setContentView(composer); layout(composer, ui.dp(360), ui.dp(400));
                composer.input.requestFocus(); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
                android.view.inputmethod.InputConnection connection = composer.input.onCreateInputConnection(new android.view.inputmethod.EditorInfo());
                assertNotNull(connection); assertTrue(connection.setComposingText("First line\nSecond line\nThird line", 1));
                android.text.Editable document = composer.input.getText();
                android.text.style.StyleSpan style = new android.text.style.StyleSpan(android.graphics.Typeface.BOLD);
                document.setSpan(style, 2, 8, android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
                composer.input.setSelection(3, 7);
                for (int width : new int[] {360, 900, 320, 900, 360}) {
                    composer.input.setHint(ui.t("Message {agent}", "agent", width == 900 ? "Claude" : "Codex"));
                    layout(composer, ui.dp(width), ui.dp(400)); layout(composer, ui.dp(width), ui.dp(400));
                    assertSame(document, composer.input.getText());
                    assertEquals(3, composer.input.getSelectionStart()); assertEquals(7, composer.input.getSelectionEnd());
                    assertEquals(0, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(document));
                    assertEquals(document.length(), android.view.inputmethod.BaseInputConnection.getComposingSpanEnd(document));
                    assertEquals(2, document.getSpanStart(style)); assertEquals(8, document.getSpanEnd(style));
                    assertEquals(1, document.getSpans(0, document.length(), NativeLineHeight.class).length);
                    NativeLineHeight span = document.getSpans(0, document.length(), NativeLineHeight.class)[0];
                    assertEquals(0, document.getSpanStart(span)); assertEquals(document.length(), document.getSpanEnd(span));
                    assertEquals(android.text.Spanned.SPAN_INCLUSIVE_INCLUSIVE, document.getSpanFlags(span));
                    android.text.Spanned hint = (android.text.Spanned) composer.input.getHint();
                    assertEquals(1, hint.getSpans(0, hint.length(), NativeLineHeight.class).length);
                    Layout lines = composer.input.getLayout(); assertEquals(3, lines.getLineCount());
                    for (int line = 0; line < lines.getLineCount(); line++)
                        assertEquals(composer.input.getTextSize() * 1.6f * line, lines.getLineBaseline(line) - lines.getLineBaseline(0), 1);
                }
                assertTrue(connection.finishComposingText());
                assertEquals(-1, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(document));
                composer.input.setSelection(document.length()); assertTrue(connection.commitText("\nFourth line", 1));
                layout(composer, ui.dp(360), ui.dp(400));
                assertSame(document, composer.input.getText()); assertEquals(4, composer.input.getLineCount());
                assertTrue(connection.deleteSurroundingText(document.length(), 0)); layout(composer, ui.dp(360), ui.dp(400));
                assertSame(document, composer.input.getText()); assertEquals(0, document.length());
                assertEquals(1, document.getSpans(0, 0, NativeLineHeight.class).length);
                Layout hintLayout = ReflectionHelpers.getField(composer.input, "mHintLayout");
                assertEquals(composer.input.getExtendedPaddingTop() + hintLayout.getLineBaseline(0), composer.input.getBaseline(), 1);
                android.view.accessibility.AccessibilityNodeInfo accessibility = composer.input.createAccessibilityNodeInfo();
                assertTrue(accessibility.isEditable()); assertEquals(composer.input.getHint().toString(), accessibility.getHintText().toString());
                accessibility.recycle();
            }
        }
    }
}
