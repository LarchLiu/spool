package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.content.ClipboardManager;
import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.Looper;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.widget.LinearLayout;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeCodeBlockTest {
    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        view.layout(0, 0, width, view.getMeasuredHeight());
    }

    @Test public void syntaxSurvivesSelectionAndRejectsLateResultsAfterLanguageChangesAndRecycling() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en"); List<Runnable> tasks = new ArrayList<>();
            ui.syntax = new NativeSyntax(tasks::add, (language, source, dark) -> List.of(new NativeSyntaxEngine.Line(0, source.length(),
                    language.equals("text") ? List.of() : List.of(new NativeSyntaxEngine.Run(1, 6, language.equals("js") ? 0xff112233 : 0xff445566, 0)))), () -> {});
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            controller.get().setContentView(block); block.bind("js", "\tconst value = 1;\n"); layout(block, 320);
            Spannable document = (Spannable) block.code.getText(); Selection.setSelection(document, 1, 6);
            int height = block.getHeight(); Object[] tabs = document.getSpans(0, document.length(), android.text.style.TabStopSpan.class);
            tasks.get(0).run();
            block.bind("ts", "\tconst value = 1;\n");
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(0, document.getSpans(0, document.length(), android.text.style.ForegroundColorSpan.class).length);
            tasks.get(1).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); layout(block, 320);
            assertSame(document, block.code.getText()); assertEquals(1, block.code.getSelectionStart()); assertEquals(6, block.code.getSelectionEnd());
            assertEquals(height, block.getHeight()); assertArrayEquals(tabs, document.getSpans(0, document.length(), android.text.style.TabStopSpan.class));
            assertEquals(0xff445566, document.getSpans(0, document.length(), android.text.style.ForegroundColorSpan.class)[0].getForegroundColor());
            block.bind("ts", "\tconst value = 1;\n"); assertEquals("An unchanged holder retains its tokens", 2, tasks.size());
            block.bind("text", "\tconst value = 1;\n");
            assertSame(document, block.code.getText()); assertEquals(0, document.getSpans(0, document.length(), android.text.style.ForegroundColorSpan.class).length);
            tasks.get(2).run(); block.bind("", ""); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("", block.code.getText().toString()); assertFalse(block.copy.isEnabled());
        }
    }

    @Test public void headerStaysVisibleWhileLongCodeScrollsAndCopiesOnlyTheLatestLiteral() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark, "en");
            String literal = "\tconst value = '<tag>';  \n\n" + "long_line ".repeat(60);
            LinearLayout root = NativeMarkdown.view(ui, "```javascript file.js\n" + literal + "\n```", false, ignored -> {});
            controller.get().setContentView(root); layout(root, 320);
            NativeCodeBlock block = (NativeCodeBlock) root.getChildAt(0);
            assertEquals("javascript", block.language.getText().toString());
            assertTrue(block.code.isTextSelectable()); assertEquals(literal, block.code.getText().toString());
            assertTrue(block.code.getWidth() > block.scroll.getWidth());
            int copyX = block.copy.getLeft(); block.scroll.scrollTo(500, 0);
            assertEquals(copyX, block.copy.getLeft()); assertTrue(block.copy.getRight() <= 320);
            block.copy.performClick();
            ClipboardManager clipboard = (ClipboardManager) ui.context.getSystemService(Context.CLIPBOARD_SERVICE);
            assertEquals(literal, clipboard.getPrimaryClip().getItemAt(0).getText().toString());
            assertEquals("Code copied", block.copy.getContentDescription());
            NativeMarkdown.bind(root, "```typescript file.ts\n" + literal + "\n```"); layout(root, 320);
            assertSame(block, root.getChildAt(0)); assertEquals("typescript", block.language.getText().toString());
            NativeMarkdown.bind(root, "```typescript\n\tupdated  \n\n\n```"); layout(root, 320);
            assertSame(block, root.getChildAt(0)); assertEquals("Copy code", block.copy.getContentDescription());
            block.copy.performClick(); assertEquals("\tupdated  \n\n", clipboard.getPrimaryClip().getItemAt(0).getText().toString());
            // Keep a compact native rendering of both short and horizontally scrolling blocks.
            NativeMarkdown.bind(root, "```\nfix: add code block headers\n\n- Keep indentation\n- Copy only code\n```\n\n```javascript\n" + literal + "\n```"); layout(root, 320);
            Bitmap bitmap = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
            root.setBackgroundColor(ui.elevated); root.draw(new Canvas(bitmap));
            try (java.io.OutputStream output = Files.newOutputStream(Path.of(System.getProperty("spool.ui.artifacts"), "native-code-blocks-api" + android.os.Build.VERSION.SDK_INT + (dark ? "-dark" : "-light") + ".png"))) {
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, output);
            } finally { bitmap.recycle(); }
        }
    }

    @Test public void infoOnlyChangesRetainSelectionAndCopyFeedbackResetsAfterSourceChangesOrDetach() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en"); List<String> copied = new ArrayList<>();
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14, copied::add); controller.get().setContentView(block);
            block.bind("js", "  const x = 1;\n"); layout(block, 240);
            Object document = block.code.getText(); Selection.setSelection((Spannable) document, 2, 7);
            block.bind("ts metadata", "  const x = 1;\n"); layout(block, 240);
            assertSame(document, block.code.getText()); assertEquals(2, block.code.getSelectionStart()); assertEquals(7, block.code.getSelectionEnd());
            block.copy.performClick(); assertEquals("  const x = 1;", copied.get(0));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1999)); assertEquals("Code copied", block.copy.getContentDescription());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1)); assertEquals("Copy code", block.copy.getContentDescription());
            block.copy.performClick(); block.bind("ts", "latest\n"); assertEquals("Copy code", block.copy.getContentDescription());
            block.copy.performClick(); assertEquals("latest", copied.get(copied.size() - 1));
            controller.get().setContentView(new View(ui.context)); assertEquals("Copy code", block.copy.getContentDescription());
        }
    }

    @Test public void copyFailureCanBeRetriedAndPlainTextHeadersAreLocalizedIncludingIndentedCode() {
        for (String[] locale : new String[][] {{"en", "Plain text", "Copy code", "Could not copy code"}, {"zh-CN", "纯文本", "复制代码", "无法复制代码"}, {"ja", "プレーンテキスト", "コードをコピー", "コードをコピーできませんでした"}}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale[0]); AtomicBoolean fail = new AtomicBoolean(true); List<String> copied = new ArrayList<>();
                NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14, value -> { if (fail.get()) throw new IllegalStateException("unavailable"); copied.add(value); });
                controller.get().setContentView(block);
                for (String info : new String[] {"", "txt", "text", "Plaintext"}) { block.bind(info, "literal\n"); assertEquals(locale[1], block.language.getText().toString()); }
                block.copy.performClick(); assertEquals(locale[3], block.copy.getContentDescription()); assertTrue(copied.isEmpty());
                fail.set(false); block.copy.performClick(); assertEquals(List.of("literal"), copied);
                block.bind("", ""); assertFalse(block.copy.isEnabled()); assertEquals(locale[2], block.copy.getContentDescription());
                LinearLayout markdown = NativeMarkdown.view(ui, "    indented code", false, ignored -> {});
                assertEquals(locale[1], ((NativeCodeBlock) markdown.getChildAt(0)).language.getText().toString());
            }
        }
    }
}
