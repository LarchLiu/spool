package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.text.Selection;
import android.text.Spannable;
import android.text.method.LinkMovementMethod;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeMarkdownInitializationTest {
    private static void texts(View view, List<TextView> result) {
        if (view instanceof TextView) result.add((TextView) view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++)
            texts(((ViewGroup) view).getChildAt(i), result);
    }

    @Test public void freshBlocksConfigureSelectionFontsAndWrappingBeforeBindingContentOnce() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-420dpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "zh-CN");
            ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
            String source = "# [标题](https://example.com/old)\n\n中文 **加粗** and English.\n\n"
                    + "| 名称 | 说明 |\n| --- | --- |\n| 中文 | [链接](https://example.com/old) |";
            List<String> opened = new ArrayList<>();
            long before = NativeTextView.contentSets;
            View markdown = NativeMarkdown.view(ui, source, false, opened::add);
            List<TextView> texts = new ArrayList<>(); texts(markdown, texts);
            assertEquals(6, texts.size());
            assertEquals("Platform configuration must not repeatedly bind nonempty Markdown",
                    texts.size(), NativeTextView.contentSets - before);
            List<Object> documents = new ArrayList<>();
            for (TextView text : texts) {
                assertTrue(text.isTextSelectable());
                assertTrue(text.getMovementMethod() instanceof LinkMovementMethod);
                assertTrue(text.getText() instanceof Spannable);
                documents.add(text.getText());
                Selection.setSelection((Spannable) text.getText(), 0, text.length());
                NativeTextView nativeText = (NativeTextView) text;
                assertNotNull(nativeText.lineMetrics);
                assertEquals(1, ((android.text.Spanned) text.getText())
                        .getSpans(0, text.length(), NativeLineHeight.class).length);
                if ("markdown:th".equals(text.getTag()) || "markdown:td".equals(text.getTag()))
                    assertEquals(14 * .92f * text.getResources().getDisplayMetrics().scaledDensity, text.getTextSize(), .001);
            }
            NativeMarkdown.bind(markdown, source.replace("example.com/old", "example.com/new"));
            for (int i = 0; i < texts.size(); i++) {
                TextView text = texts.get(i);
                assertSame("Formatting updates keep each selectable document", documents.get(i), text.getText());
                assertEquals(0, text.getSelectionStart()); assertEquals(text.length(), text.getSelectionEnd());
                for (NativeMarkdownLink link : ((android.text.Spanned) text.getText())
                        .getSpans(0, text.length(), NativeMarkdownLink.class)) link.onClick(text);
            }
            assertEquals(java.util.Arrays.asList("https://example.com/new", "https://example.com/new"), opened);
        }
    }
}
