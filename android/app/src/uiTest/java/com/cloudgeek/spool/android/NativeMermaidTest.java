package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.content.ClipboardManager;
import android.content.Context;
import android.os.Looper;
import android.view.View;
import android.webkit.WebView;
import android.widget.LinearLayout;
import java.lang.reflect.Method;
import java.time.Duration;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowWebView;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeMermaidTest {
    private void complete(Object bridge, int generation, boolean ready, int height) throws Exception {
        Method method = bridge.getClass().getDeclaredMethod("rendered", int.class, boolean.class, int.class);
        method.setAccessible(true); method.invoke(bridge, generation, ready, height);
        Shadows.shadowOf(Looper.getMainLooper()).idle();
    }
    private WebView browser(NativeCodeBlock block) {
        return (WebView) ((NativeMermaid) block.getChildAt(2)).getChildAt(0);
    }

    @Test public void sourceAndEnlargedFallbackUseActualMermaidSyntaxWithoutChangingSelectionOrBytes() throws Exception {
        for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup();
                NativeSyntax syntax = new NativeSyntax(Runnable::run, NativeSyntaxEngineTest.engine())) {
            NativeUi ui = new NativeUi(controller.get(), dark, "en"); ui.syntax = syntax;
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            String literal = "flowchart LR\n %% comment\n A[\"开始\"] --> B{Ready?}";
            block.bind("mermaid", literal + "\n"); controller.get().setContentView(block);
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            android.text.Spannable text = (android.text.Spannable) block.code.getText();
            android.text.style.ForegroundColorSpan[] spans = text.getSpans(0, text.length(), android.text.style.ForegroundColorSpan.class);
            assertTrue(java.util.Arrays.stream(spans).map(android.text.style.ForegroundColorSpan::getForegroundColor).distinct().count() > 1);
            assertEquals(literal, text.toString()); android.text.Selection.setSelection(text, 2, 8);
            block.sourceToggle.performClick(); block.sourceToggle.performClick();
            assertSame(text, block.code.getText()); assertEquals(2, block.code.getSelectionStart()); assertEquals(8, block.code.getSelectionEnd());
            block.enlarge.performClick(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            NativeMermaidPreview preview = block.preview;
            assertEquals(View.VISIBLE, preview.status.getVisibility());
            assertEquals(View.GONE, preview.fallback.getVisibility());
            WebView fullBrowser = (WebView) preview.diagram.getChildAt(0);
            complete(Shadows.shadowOf(fullBrowser).getJavascriptInterface("SpoolMermaid"), 1, false, 0);
            assertEquals(View.GONE, preview.status.getVisibility());
            assertEquals(View.VISIBLE, preview.fallback.getVisibility());
            android.text.Spanned full = (android.text.Spanned) preview.source.getText();
            assertTrue(full.getSpans(0, full.length(), android.text.style.ForegroundColorSpan.class).length > 1);
            assertEquals(literal, full.toString());
            preview.close.performClick();
        }
    }

    @Test public void markdownRendersOfflineDiagramsCopiesSourceAndRecoversFromIncompleteStreams() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            String source = "flowchart LR\n A[开始] --> B[Finish]";
            LinearLayout root = NativeMarkdown.view(ui, "```MERMAID\n" + source + "\n```", false, ignored -> {});
            controller.get().setContentView(root);
            NativeCodeBlock block = (NativeCodeBlock) root.getChildAt(0);
            WebView browser = browser(block); ShadowWebView shadow = Shadows.shadowOf(browser);
            assertTrue(browser.getSettings().getJavaScriptEnabled());
            assertTrue(browser.getSettings().getBlockNetworkLoads());
            assertFalse(browser.getSettings().getAllowFileAccess());
            assertFalse(browser.getSettings().getAllowContentAccess());
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            String url = shadow.getLastLoadedUrl();
            assertEquals("https://mermaid.spool.invalid/index.html", url);
            shadow.getWebViewClient().onPageFinished(browser, url);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(120));
            assertEquals("window.spoolRenderMermaid(" + JSONObject.quote(source) + ",false,1,false)", shadow.getLastEvaluatedJavascript());
            Object bridge = shadow.getJavascriptInterface("SpoolMermaid");
            complete(bridge, 1, true, 180);
            assertEquals(View.GONE, block.scroll.getVisibility());
            assertEquals(ui.dp(180), block.getChildAt(2).getLayoutParams().height);
            LinearLayout header = (LinearLayout) block.getChildAt(0);
            assertTrue(header.indexOfChild(block.sourceToggle) < header.indexOfChild(block.enlarge));
            assertTrue(header.indexOfChild(block.enlarge) < header.indexOfChild(block.copy));
            block.sourceToggle.performClick();
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            assertEquals("Preview", block.sourceToggle.getContentDescription());
            block.sourceToggle.performClick();
            assertEquals(View.GONE, block.scroll.getVisibility());
            assertSame(browser, browser(block));
            block.enlarge.performClick();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            NativeMermaidPreview preview = block.preview;
            assertTrue(preview.isShowing()); assertEquals(source, preview.source.getText().toString());
            assertEquals(View.VISIBLE, preview.status.getVisibility());
            assertEquals(View.GONE, preview.fallback.getVisibility());
            assertFalse(preview.zoomIn.isEnabled());
            WebView full = (WebView) preview.diagram.getChildAt(0);
            ShadowWebView fullShadow = Shadows.shadowOf(full);
            fullShadow.getWebViewClient().onPageFinished(full, fullShadow.getLastLoadedUrl());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(120));
            assertTrue(fullShadow.getLastEvaluatedJavascript().endsWith(",false,1,true)"));
            Object fullBridge = fullShadow.getJavascriptInterface("SpoolMermaid");
            complete(fullBridge, 1, true, 500);
            assertEquals(View.GONE, preview.status.getVisibility());
            assertEquals(View.GONE, preview.fallback.getVisibility());
            assertTrue(preview.zoomIn.isEnabled());
            preview.zoomIn.performClick(); assertEquals("window.spoolZoomMermaid(\"in\")", fullShadow.getLastEvaluatedJavascript());
            preview.zoomOut.performClick(); assertEquals("window.spoolZoomMermaid(\"out\")", fullShadow.getLastEvaluatedJavascript());
            preview.fit.performClick(); assertEquals("window.spoolZoomMermaid(\"fit\")", fullShadow.getLastEvaluatedJavascript());
            block.copy.performClick();
            ClipboardManager clipboard = (ClipboardManager) ui.context.getSystemService(Context.CLIPBOARD_SERVICE);
            assertEquals(source, clipboard.getPrimaryClip().getItemAt(0).getText().toString());
            NativeMarkdown.bind(root, "```mermaid\nflowchart LR\n A[Incomplete");
            assertEquals("flowchart LR\n A[Incomplete", preview.source.getText().toString());
            assertEquals(View.VISIBLE, preview.status.getVisibility());
            assertEquals(View.GONE, preview.fallback.getVisibility());
            complete(fullBridge, 1, false, 0);
            assertEquals(View.VISIBLE, preview.status.getVisibility());
            assertEquals(View.GONE, preview.fallback.getVisibility());
            complete(fullBridge, 2, false, 0);
            assertEquals(View.GONE, preview.status.getVisibility());
            assertEquals(View.VISIBLE, preview.fallback.getVisibility());
            preview.close.performClick(); assertFalse(preview.isShowing()); assertTrue(fullShadow.wasDestroyCalled());
            assertSame(block, root.getChildAt(0)); assertSame(browser, browser(block));
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            complete(bridge, 1, true, 999); // Completion for old source cannot hide the current fallback.
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            complete(bridge, 2, false, 0);
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            NativeMarkdown.bind(root, "```mermaid\nflowchart LR\n New --> Result\n```");
            complete(bridge, 3, true, 200);
            assertEquals(View.GONE, block.scroll.getVisibility());
            NativeMarkdown.bind(root, "```text\nplain source\n```");
            assertSame(block, root.getChildAt(0)); assertEquals(2, block.getChildCount());
            assertTrue(shadow.wasDestroyCalled());
            complete(bridge, 3, true, 1000);
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
        }
    }

    @Test public void detachedDiagramsDestroyBrowserAndReattachWithFreshGeneration() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), true, "zh-CN");
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            block.bind("mermaid", "sequenceDiagram\n A->>B: hello\n");
            controller.get().setContentView(block);
            WebView first = browser(block); ShadowWebView shadow = Shadows.shadowOf(first);
            Object old = shadow.getJavascriptInterface("SpoolMermaid");
            controller.get().setContentView(new View(ui.context));
            assertTrue(shadow.wasDestroyCalled());
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            controller.get().setContentView(block);
            WebView next = browser(block); assertNotSame(first, next);
            complete(old, 1, true, 500);
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            ShadowWebView current = Shadows.shadowOf(next);
            current.getWebViewClient().onPageFinished(next, current.getLastLoadedUrl());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(120));
            assertTrue(current.getLastEvaluatedJavascript().endsWith(",true,2,false)"));
            complete(current.getJavascriptInterface("SpoolMermaid"), 2, true, 120);
            assertEquals(View.GONE, block.scroll.getVisibility());
        }
    }
}
