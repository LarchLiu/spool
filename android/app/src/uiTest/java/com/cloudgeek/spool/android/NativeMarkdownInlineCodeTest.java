package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.style.LeadingMarginSpan;
import android.view.View;
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
