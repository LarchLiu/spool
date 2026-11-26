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
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowWebView;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
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
            WebView recovered = browser(block);
            complete(Shadows.shadowOf(recovered).getJavascriptInterface("SpoolMermaid"), 3, true, 200);
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

    @Test public void rendererExitKeepsWorkspaceAndSourceUsableAndIgnoresLateDiagramCallbacks() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            LinearLayout workspace = ui.column();
            android.widget.EditText draft = new android.widget.EditText(ui.context);
            draft.setText("Keep my unsent draft"); workspace.addView(draft);
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            String source = "flowchart LR\n A --> B";
            block.bind("mermaid", source); workspace.addView(block);
            controller.get().setContentView(workspace);
            WebView first = browser(block); ShadowWebView shadow = Shadows.shadowOf(first);
            Object oldBridge = shadow.getJavascriptInterface("SpoolMermaid");
            complete(oldBridge, 1, true, 180);
            assertEquals(View.GONE, block.scroll.getVisibility());
            block.enlarge.performClick(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            NativeMermaidPreview preview = block.preview;
            WebView full = (WebView) preview.diagram.getChildAt(0);
            ShadowWebView fullShadow = Shadows.shadowOf(full);
            Object fullBridge = fullShadow.getJavascriptInterface("SpoolMermaid");
            complete(fullBridge, 1, true, 400);

            // Every WebView sharing a terminated renderer must handle its own exit.
            assertTrue(shadow.getWebViewClient().onRenderProcessGone(first, null));
            assertTrue(fullShadow.getWebViewClient().onRenderProcessGone(full, null));
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertTrue(shadow.wasDestroyCalled()); assertTrue(fullShadow.wasDestroyCalled());
            assertEquals(0, ((NativeMermaid) block.getChildAt(2)).getChildCount());
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            assertEquals(source, block.code.getText().toString());
            assertEquals(View.VISIBLE, preview.fallback.getVisibility());
            assertFalse(preview.zoomIn.isEnabled());
            assertSame(workspace, block.getParent());
            assertEquals("Keep my unsent draft", draft.getText().toString());
            complete(oldBridge, 1, true, 180); complete(fullBridge, 1, true, 400);
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            assertEquals(View.VISIBLE, preview.fallback.getVisibility());
            preview.close.performClick();
            block.copy.performClick();
            ClipboardManager clipboard = (ClipboardManager) ui.context.getSystemService(Context.CLIPBOARD_SERVICE);
            assertEquals(source, clipboard.getPrimaryClip().getItemAt(0).getText().toString());

            String nextSource = "flowchart LR\n B --> C";
            block.bind("mermaid", nextSource);
            assertEquals(nextSource, block.code.getText().toString());
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            assertEquals(0, ((NativeMermaid) block.getChildAt(2)).getChildCount());
            workspace.removeView(block); workspace.addView(block);
            WebView next = browser(block); assertNotSame(first, next);
            ShadowWebView current = Shadows.shadowOf(next);
            current.getWebViewClient().onPageFinished(next, current.getLastLoadedUrl());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(120));
            assertTrue(current.getLastEvaluatedJavascript().endsWith(",false,4,false)"));
            complete(current.getJavascriptInterface("SpoolMermaid"), 4, true, 180);
            assertEquals(View.GONE, block.scroll.getVisibility());
        }
    }

    private static final String VECTOR = "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 200 100' width='200' height='100'>"
            + "<rect x='0' y='0' width='200' height='100' fill='#ff0000'/><text x='12' y='50' font-size='18' fill='#ffffff'>A → B</text></svg>";

    private void snapshot(Object bridge, int generation, String svg) throws Exception {
        Method method = bridge.getClass().getDeclaredMethod("snapshot", int.class, String.class);
        method.setAccessible(true); method.invoke(bridge, generation, svg);
        Shadows.shadowOf(Looper.getMainLooper()).idle();
    }

    @Test public void completedVectorsSurviveScrollingAndRecycledRowsAndOpenNativePreviewWithoutCompilingAgain() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            LinearLayout workspace = ui.column();
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            String source = "flowchart LR\n A --> B";
            block.bind("mermaid", source); workspace.addView(block); controller.get().setContentView(workspace);
            NativeMermaid diagram = (NativeMermaid) block.getChildAt(2);
            WebView compiler = browser(block); ShadowWebView shadow = Shadows.shadowOf(compiler);
            Object bridge = shadow.getJavascriptInterface("SpoolMermaid");
            snapshot(bridge, 1, VECTOR);
            assertTrue("A finished diagram releases its browser", shadow.wasDestroyCalled());
            assertTrue(diagram.getChildAt(0) instanceof NativeMermaidImage);
            assertEquals(View.GONE, block.scroll.getVisibility());
            View picture = diagram.getChildAt(0); int height = diagram.getLayoutParams().height;
            assertTrue(height > 0);
            for (int i = 0; i < 5; i++) {
                workspace.removeView(block); workspace.addView(block);
                assertSame("Scrolling restores the native picture immediately", picture, diagram.getChildAt(0));
                assertEquals(height, diagram.getLayoutParams().height);
                assertEquals(View.GONE, block.scroll.getVisibility());
            }
            block.sourceToggle.performClick(); block.sourceToggle.performClick();
            assertSame(picture, diagram.getChildAt(0));
            block.enlarge.performClick(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            NativeMermaidPreview preview = block.preview;
            assertEquals(View.GONE, preview.status.getVisibility());
            assertEquals(View.GONE, preview.fallback.getVisibility());
            assertTrue(preview.zoomIn.isEnabled());
            assertTrue(preview.diagram.getChildAt(0) instanceof NativeMermaidImage);
            preview.zoomIn.performClick(); preview.zoomOut.performClick(); preview.fit.performClick();
            preview.close.performClick();
            block.bind("text", "Other recycled content");
            block.bind("mermaid", source);
            assertTrue("A new row reuses the shared vector cache", ((NativeMermaid) block.getChildAt(2)).getChildAt(0) instanceof NativeMermaidImage);
            complete(bridge, 1, false, 0);
            assertEquals("Late compiler callbacks cannot hide a cached picture", View.GONE, block.scroll.getVisibility());
        }
    }

    @Test public void cacheSeparatesThemesAndSourceAndNativePicturesResizeAndZoomWithoutABrowser() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            NativeMermaidCache.Image vector = NativeMermaidCache.decode(VECTOR);
            ui.mermaidCache.put(ui.mermaidCache.key("flowchart LR\n A --> B", false), vector);
            assertNull(ui.mermaidCache.get(ui.mermaidCache.key("flowchart LR\n A --> B", true)));
            assertNull(ui.mermaidCache.get(ui.mermaidCache.key("flowchart LR\n A --> C", false)));
            NativeMermaidImage image = new NativeMermaidImage(ui, true); image.bind(vector);
            controller.get().setContentView(image);
            image.layout(0, 0, 400, 300);
            android.graphics.Bitmap before = android.graphics.Bitmap.createBitmap(400, 300, android.graphics.Bitmap.Config.ARGB_8888);
            image.draw(new android.graphics.Canvas(before));
            assertEquals(android.graphics.Color.RED, before.getPixel(40, 80));
            image.zoom("in");
            android.graphics.Bitmap after = android.graphics.Bitmap.createBitmap(400, 300, android.graphics.Bitmap.Config.ARGB_8888);
            image.draw(new android.graphics.Canvas(after));
            assertEquals("Native zoom enlarges the vector outside its fit bounds", android.graphics.Color.RED, after.getPixel(5, 50));
            image.zoom("fit");
            android.graphics.Bitmap fit = android.graphics.Bitmap.createBitmap(400, 300, android.graphics.Bitmap.Config.ARGB_8888);
            image.draw(new android.graphics.Canvas(fit));
            assertTrue(before.sameAs(fit)); before.recycle(); after.recycle(); fit.recycle();
            assertTrue(image.inlineHeight(160) < image.inlineHeight(400));
        }
    }

    @Test public void queuedVectorsCannotReplaceNewSourceAndCachedSyntaxErrorsDoNotRestartACompiler() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            java.util.List<Runnable> jobs = new java.util.ArrayList<>(); ui.codeWorker = jobs::add;
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            block.bind("mermaid", "flowchart LR\n A --> B"); controller.get().setContentView(block);
            WebView compiler = browser(block); Object bridge = Shadows.shadowOf(compiler).getJavascriptInterface("SpoolMermaid");
            snapshot(bridge, 1, VECTOR); assertEquals(1, jobs.size());
            block.bind("mermaid", "flowchart LR\n New --> Result");
            jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            assertSame(compiler, browser(block));
            snapshot(bridge, 2, VECTOR); jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(View.GONE, block.scroll.getVisibility());
            block.bind("mermaid", "invalid source");
            WebView invalid = browser(block); Object invalidBridge = Shadows.shadowOf(invalid).getJavascriptInterface("SpoolMermaid");
            complete(invalidBridge, 3, false, 0);
            assertTrue(Shadows.shadowOf(invalid).wasDestroyCalled());
            NativeMermaid diagram = (NativeMermaid) block.getChildAt(2);
            assertEquals(0, diagram.getChildCount());
            controller.get().setContentView(new View(ui.context)); controller.get().setContentView(block);
            assertEquals("Scrolling past an unchanged syntax error does not rerun Mermaid", 0, diagram.getChildCount());
            assertEquals(View.VISIBLE, block.scroll.getVisibility());
            block.bind("mermaid", "flowchart LR\n A --> B");
            assertTrue(diagram.getChildAt(0) instanceof NativeMermaidImage);
            assertEquals(View.GONE, block.scroll.getVisibility());
        }
    }

    @Test public void nativeVectorsRejectExternalResourcesAndCacheMemoryIsBounded() throws Exception {
        for (String unsafe : new String[] {"<image href='https://example.com/track.png'/>", "<use href='https://example.com/x.svg#x'/>",
                "<rect fill='url(https://example.com/x.svg#x)'/>", "<foreignObject>HTML</foreignObject>", "<!DOCTYPE svg>", "<!ENTITY secret SYSTEM 'file:///private/file'>"}) {
            try { NativeMermaidCache.decode(VECTOR.replace("</svg>", unsafe + "</svg>")); fail(unsafe); }
            catch (Exception expected) {}
        }
        try { NativeMermaidCache.decode("x".repeat(NativeMermaidCache.MAX_SVG + 1)); fail("Unbounded SVG accepted"); }
        catch (Exception expected) {}
        NativeMermaidCache cache = new NativeMermaidCache();
        for (int i = 0; i < 33; i++) cache.put(cache.key("diagram-" + i, false), NativeMermaidCache.ERROR);
        assertNull("Old diagrams are evicted", cache.get(cache.key("diagram-0", false)));
        assertSame(NativeMermaidCache.ERROR, cache.get(cache.key("diagram-32", false)));
        assertNotNull("URL-like label text is ordinary diagram content", NativeMermaidCache.decode(VECTOR.replace("A → B", "href='https://example.com' url(https://example.com) @import")));
    }

    @Test public void nativePicturesExposeCachedTitlesAndDescriptionsToAccessibility() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            NativeMermaidCache.Image vector = NativeMermaidCache.decode(VECTOR.replace("<rect", "<title>Workflow</title><desc>Validate before shipping.</desc><rect"));
            ui.mermaidCache.put(ui.mermaidCache.key("accessible", false), vector);
            NativeMermaidImage image = new NativeMermaidImage(ui, false);
            image.bind(ui.mermaidCache.get(ui.mermaidCache.key("accessible", false)));
            controller.get().setContentView(image);
            android.view.accessibility.AccessibilityNodeInfo info = image.createAccessibilityNodeInfo();
            assertEquals("Workflow\nValidate before shipping.", info.getContentDescription());
            assertEquals("android.widget.ImageView", info.getClassName());
            assertEquals(View.IMPORTANT_FOR_ACCESSIBILITY_YES, image.getImportantForAccessibility());
            image.bind(NativeMermaidCache.decode(VECTOR));
            assertEquals("Mermaid", image.getContentDescription());
            image.bind(null);
            assertNull(image.getContentDescription());
            assertEquals(View.IMPORTANT_FOR_ACCESSIBILITY_NO, image.getImportantForAccessibility());
        }
    }

    @Test public void finishingAVectorAfterDetachStillWarmsTheCacheForANewRow() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            java.util.List<Runnable> jobs = new java.util.ArrayList<>(); ui.codeWorker = jobs::add;
            NativeCodeBlock block = new NativeCodeBlock(ui, ui.text, 14);
            String source = "flowchart LR\n A --> B";
            block.bind("mermaid", source); controller.get().setContentView(block);
            WebView compiler = browser(block); Object bridge = Shadows.shadowOf(compiler).getJavascriptInterface("SpoolMermaid");
            snapshot(bridge, 1, VECTOR);
            controller.get().setContentView(new View(ui.context));
            jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("An obsolete result must not attach a picture to the detached row", 0, ((NativeMermaid) block.getChildAt(2)).getChildCount());
            NativeCodeBlock replacement = new NativeCodeBlock(ui, ui.text, 14);
            replacement.bind("mermaid", source); controller.get().setContentView(replacement);
            assertTrue("Another row reuses completed work without waiting for the old row to reattach", ((NativeMermaid) replacement.getChildAt(2)).getChildAt(0) instanceof NativeMermaidImage);
            assertEquals(View.GONE, replacement.scroll.getVisibility());
        }
    }

    @Test public void browserGeneratedDiagramTypesPaintWithTheNativeVectorEngine() throws Exception {
        String directory = System.getenv("SPOOL_MERMAID_VECTOR_DIR");
        org.junit.Assume.assumeTrue("Run the Mermaid browser smoke to prepare actual renderer output", directory != null);
        java.nio.file.Path fixtures = java.nio.file.Path.of(directory);
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String engine : new String[] {"chromium", "webkit"}) for (String name : new String[] {"flowchart", "sequence", "class", "state", "pie", "gantt", "treemap", "flowchart-dark"}) {
                NativeUi ui = new NativeUi(controller.get(), name.endsWith("-dark"), "en");
                NativeMermaidCache.Image vector = NativeMermaidCache.decode(java.nio.file.Files.readString(fixtures.resolve(engine + "-" + name + ".svg")));
                NativeMermaidImage image = new NativeMermaidImage(ui, false); image.bind(vector);
                int height = image.inlineHeight(360);
                image.layout(0, 0, 360, height);
                android.graphics.Bitmap pixels = android.graphics.Bitmap.createBitmap(360, height, android.graphics.Bitmap.Config.ARGB_8888);
                try {
                    image.draw(new android.graphics.Canvas(pixels));
                    int painted = 0;
                    for (int y = 0; y < height; y++) for (int x = 0; x < 360; x++) if (android.graphics.Color.alpha(pixels.getPixel(x, y)) != 0) painted++;
                    assertTrue(engine + "/" + name + " must paint native paths and labels", painted > 100);
                    if (android.os.Build.VERSION.SDK_INT == 35) try (java.io.OutputStream output = java.nio.file.Files.newOutputStream(fixtures.resolve(engine + "-" + name + "-native.png"))) {
                        assertTrue(pixels.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
                    }
                } finally { pixels.recycle(); }
            }
        }
    }
}
