package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.style.LeadingMarginSpan;
import android.view.View;
import android.widget.TextView;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeMarkdownInlineCodeTest {
    @Test public void wrappedCodeBackgroundsStayCompactAndRoundedWhenLineSpacingGrows() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        String literal = "notifyDesktopServerReady recordDesktopServerState notifyDesktopServerState";
        String source = "切换服务器后，`" + literal + "` 上报连接状态。";
        for (boolean dark : new boolean[] {false, true}) {
            try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, "zh-CN");
                NativeMarkwonHistory history = new NativeMarkwonHistory(ui, new NativeHistoryScroll.State(), ignored -> fail());
                try {
                    controller.get().setContentView(history);
                    history.begin(); history.markdown("answer", source); history.end();
                    NativeMarkwonHistoryTest.await(history);
                    TextView text = NativeMarkwonHistoryTest.texts(history).stream()
                            .filter(view -> view.getText().toString().contains(literal)).findFirst().orElseThrow();
                    int start = text.getText().toString().indexOf(literal), end = start + literal.length();
                    assertTrue("Long inline code must still wrap", text.getLayout().getLineForOffset(end - 1) > text.getLayout().getLineForOffset(start));
                    float[] compact = backgroundHeights(text, ui);
                    text.setLineSpacing(ui.dp(24), 1); NativeMarkwonHistoryTest.layout(history);
                    float[] spaced = backgroundHeights(text, ui);
                    assertArrayEquals("Decoration height must not follow the paragraph line box", compact, spaced, .01f);
                    assertEquals(literal, text.getText().subSequence(start, end).toString());
                    java.nio.file.Path output = java.nio.file.Path.of(System.getProperty("spool.ui.artifacts"), "inline-code");
                    java.nio.file.Files.createDirectories(output);
                    Bitmap image = Bitmap.createBitmap(text.getWidth(), text.getHeight(), Bitmap.Config.ARGB_8888);
                    try {
                        Canvas canvas = new Canvas(image); canvas.drawColor(ui.elevated); text.draw(canvas);
                        try (var stream = java.nio.file.Files.newOutputStream(output.resolve("wrapped-" + (dark ? "dark" : "light") + "-api" + android.os.Build.VERSION.SDK_INT + ".png"))) {
                            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                        }
                    } finally { image.recycle(); }
                } finally { history.dispose(); }
            }
        }
    }

    private float[] backgroundHeights(TextView text, NativeUi ui) {
        java.util.List<Float> heights = new java.util.ArrayList<>();
        Bitmap image = Bitmap.createBitmap(text.getWidth(), text.getHeight(), Bitmap.Config.ARGB_8888);
        try {
            text.draw(new Canvas(image) {
                @Override public void drawRoundRect(float left, float top, float right, float bottom, float rx, float ry, android.graphics.Paint paint) {
                    if (paint.getColor() == ui.soft) {
                        assertTrue("Inline code needs small rounded corners", rx > 0 && rx <= ui.dp(5) && rx == ry);
                        assertTrue("Background must leave space between text lines", bottom - top < text.getLineHeight() - ui.dp(2));
                        int line = text.getLayout().getLineForVertical((int) ((top + bottom) / 2));
                        assertTrue("Wrapped backgrounds must end with the text, not fill the row", right <= text.getLayout().getLineRight(line) + 1);
                        heights.add(bottom - top);
                    }
                    super.drawRoundRect(left, top, right, bottom, rx, ry, paint);
                }
            });
            assertTrue("Every wrapped code line needs a background", heights.size() > 1);
            float[] result = new float[heights.size()];
            for (int i = 0; i < result.length; i++) result[i] = heights.get(i);
            return result;
        } finally { image.recycle(); }
    }

    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        view.layout(0, 0, width, view.getMeasuredHeight());
    }

    private Bitmap codeImage(NativeDisclosureRow row, String code) {
        int start = row.label.getText().toString().indexOf(code), end = start + code.length();
        assertTrue(start >= 0);
        assertTrue("The code must remain fully visible before the ellipsis", row.label.getLayout().getEllipsisStart(0) > end);
        int left = row.label.getLeft() + (int) Math.floor(row.label.getLayout().getPrimaryHorizontal(start));
        int right = row.label.getLeft() + (int) Math.ceil(row.label.getLayout().getPrimaryHorizontal(end));
        Bitmap image = Bitmap.createBitmap(right - left, row.getHeight(), Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(image); canvas.translate(-left, 0); row.draw(canvas);
        return image;
    }

    @Test public void ellipsizedThinkingPreviewsKeepTheSameCodeGlyphsAsParagraphLayout() {
        for (String density : new String[] {"mdpi", "420dpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
                for (boolean dark : new boolean[] {false, true}) for (String locale : new String[] {"en", "zh-CN"}) {
                    NativeUi ui = new NativeUi(controller.get(), dark, locale);
                    for (String code : new String[] {"pending", "bun -e"}) {
                        SpannableStringBuilder title = new SpannableStringBuilder(ui.t("Thinking") + " · ");
                        title.append(NativeMarkdown.preview(ui, "Review `" + code + "` before continuing. ".repeat(12)));
                        SpannableStringBuilder paragraph = new SpannableStringBuilder(title);
                        // A zero-margin paragraph keeps the same appearance while avoiding the
                        // platform's single-line optimization and its rewritten replacement text.
                        paragraph.setSpan(new LeadingMarginSpan.Standard(0), 0, paragraph.length(), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
                        NativeDisclosureRow actual = new NativeDisclosureRow(ui, title, "brain", "", false, false, false, () -> {});
                        NativeDisclosureRow expected = new NativeDisclosureRow(ui, paragraph, "brain", "", false, false, false, () -> {});
                        for (int width : new int[] {240, 320, 412}) {
                            layout(actual, ui.dp(width)); layout(expected, ui.dp(width));
                            assertTrue("The preview must exercise truncation", actual.label.getLayout().getEllipsisCount(0) > 0);
                            assertEquals(expected.label.getWidth(), actual.label.getWidth());
                            assertEquals(expected.getHeight(), actual.getHeight());
                            Bitmap a = codeImage(actual, code), b = codeImage(expected, code);
                            try {
                                assertTrue(density + "/" + dark + "/" + locale + "/" + code + "/" + width, a.sameAs(b));
                            } finally { a.recycle(); b.recycle(); }
                        }
                    }
                }
            }
        }
    }
}
