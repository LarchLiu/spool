package com.cloudgeek.spool.android;

import android.annotation.SuppressLint;
import android.graphics.Color;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.webkit.JavascriptInterface;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import org.json.JSONObject;

/** Offline Mermaid compilation followed by cached native vector display. */
final class NativeMermaid extends FrameLayout {
    enum State { LOADING, READY, ERROR }
    interface Result { void rendered(State state, int height); }
    private static final String DOCUMENT = "https://mermaid.spool.invalid/index.html";
    private final NativeUi ui;
    private final Result result;
    private final boolean expanded;
    private final NativeMermaidImage image;
    private final Handler main = new Handler(Looper.getMainLooper());
    private WebView browser;
    private NativeMermaidCache.Image vector;
    private String source = "";
    private int generation;
    private boolean loaded, rendererFailed, decoding;
    private final Runnable render = this::render;

    NativeMermaid(NativeUi ui, Result result) {
        this(ui, false, result);
    }

    NativeMermaid(NativeUi ui, boolean expanded, Result result) {
        super(ui.context); this.ui = ui; this.result = result; this.expanded = expanded;
        image = new NativeMermaidImage(ui, expanded);
        setAlpha(0);
    }

    void bind(String value) {
        if (source.equals(value)) return;
        source = value; generation++; decoding = false;
        vector = null; removeView(image); image.bind(null);
        if (restore()) return;
        result.rendered(rendererFailed ? State.ERROR : State.LOADING, 0); setAlpha(0);
        removeCallbacks(render);
        if (loaded) postDelayed(render, 120);
        else if (isAttachedToWindow() && !rendererFailed) start();
    }

    private String key() { return ui.mermaidCache.key(source, ui.dark); }

    private boolean restore() {
        NativeMermaidCache.Image cached = ui.mermaidCache.get(key());
        if (cached == null) return false;
        if (cached == NativeMermaidCache.ERROR) { stopBrowser(); setAlpha(0); result.rendered(State.ERROR, 0); }
        else display(cached);
        return true;
    }

    private void display(NativeMermaidCache.Image value) {
        vector = value;
        stopBrowser();
        if (image.getParent() == null) addView(image, new LayoutParams(-1, -1));
        image.bind(value); setAlpha(1);
        int width = getWidth() > 0 ? getWidth() : getResources().getDisplayMetrics().widthPixels;
        result.rendered(State.READY, image.inlineHeight(width));
    }

    private void stopBrowser() {
        loaded = false; removeCallbacks(render);
        if (browser == null) return;
        WebView view = browser; browser = null;
        view.removeJavascriptInterface("SpoolMermaid"); view.stopLoading(); removeView(view); view.destroy();
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void start() {
        if (browser != null || vector != null || source.isEmpty() || restore()) return;
        rendererFailed = false;
        WebView view = new WebView(ui.context);
        browser = view;
        view.setBackgroundColor(Color.TRANSPARENT);
        view.getSettings().setJavaScriptEnabled(true);
        view.getSettings().setBlockNetworkLoads(true);
        view.getSettings().setAllowFileAccess(false);
        view.getSettings().setAllowContentAccess(false);
        view.getSettings().setSupportZoom(false);
        view.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
        view.addJavascriptInterface(new Bridge(view), "SpoolMermaid");
        view.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView ignored, WebResourceRequest request) { return true; }
            @Override public WebResourceResponse shouldInterceptRequest(WebView ignored, WebResourceRequest request) {
                if (DOCUMENT.equals(request.getUrl().toString())) {
                    try { return new WebResourceResponse("text/html", "UTF-8", ui.context.getAssets().open("mermaid/index.html")); }
                    catch (IOException error) { failed(view); }
                }
                return new WebResourceResponse("text/plain", "UTF-8", new ByteArrayInputStream(new byte[0]));
            }
            @Override public void onPageFinished(WebView ignored, String url) {
                if (browser != view || !DOCUMENT.equals(url)) return;
                loaded = true; removeCallbacks(render); postDelayed(render, 120);
            }
            @Override public void onReceivedError(WebView ignored, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) failed(view);
            }
            @Override public boolean onRenderProcessGone(WebView ignored, RenderProcessGoneDetail detail) {
                if (browser == view) {
                    // Android otherwise terminates the host process too. Keep
                    // the native workspace mounted and expose the diagram source.
                    browser = null; loaded = false; rendererFailed = true; generation++;
                    removeCallbacks(render); removeView(view); view.destroy();
                    decoding = false; setAlpha(0); result.rendered(State.ERROR, 0);
                }
                return true;
            }
        });
        addView(view, new LayoutParams(-1, expanded ? -1 : ui.dp(240)));
        view.loadUrl(DOCUMENT);
    }

    private void render() {
        if (!loaded || browser == null) return;
        browser.evaluateJavascript("window.spoolRenderMermaid(" + JSONObject.quote(source) + "," + ui.dark + "," + generation + "," + expanded + ")", null);
    }

    void zoom(String action) {
        if (vector != null) image.zoom(action);
        else if (loaded && browser != null) browser.evaluateJavascript("window.spoolZoomMermaid(" + JSONObject.quote(action) + ")", null);
    }

    private void failed(WebView owner) {
        main.post(() -> {
            if (browser != owner) return;
            setAlpha(0); result.rendered(State.ERROR, 0);
        });
    }

    private final class Bridge {
        private final WebView owner;
        Bridge(WebView owner) { this.owner = owner; }
        @JavascriptInterface public void rendered(int revision, boolean ready, int height) {
            main.post(() -> {
                if (browser != owner || revision != generation) return;
                if (!ready) {
                    ui.mermaidCache.put(key(), NativeMermaidCache.ERROR);
                    stopBrowser(); setAlpha(0); result.rendered(State.ERROR, 0); return;
                }
                int pixels = ready ? ui.dp(Math.max(1, Math.min(20000, height))) : 0;
                if (!expanded) owner.setLayoutParams(new LayoutParams(-1, Math.max(ui.dp(1), pixels)));
                owner.setImportantForAccessibility(ready ? View.IMPORTANT_FOR_ACCESSIBILITY_AUTO : View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
                setAlpha(ready ? 1 : 0);
                result.rendered(ready ? State.READY : State.ERROR, pixels);
            });
        }

        @JavascriptInterface public void snapshot(int revision, String svg) {
            // Copy only bounded output from the offline document. Parsing and
            // recording the vector run away from RecyclerView's layout thread.
            main.post(() -> {
                if (browser != owner || revision != generation || decoding) return;
                if (svg == null || svg.length() > NativeMermaidCache.MAX_SVG) {
                    ui.mermaidCache.put(key(), NativeMermaidCache.ERROR);
                    stopBrowser(); setAlpha(0); result.rendered(State.ERROR, 0); return;
                }
                decoding = true;
                String identity = key();
                Runnable task = () -> {
                    NativeMermaidCache.Image decoded = null;
                    try { decoded = NativeMermaidCache.decode(svg); }
                    catch (Exception ignored) { /* Unsupported SVG keeps the existing source fallback. */ }
                    NativeMermaidCache.Image value = decoded;
                    main.post(() -> {
                        ui.mermaidCache.put(identity, value == null ? NativeMermaidCache.ERROR : value);
                        if (browser != owner || revision != generation) return;
                        decoding = false;
                        if (value != null) display(value);
                        else { stopBrowser(); setAlpha(0); result.rendered(State.ERROR, 0); }
                    });
                };
                try {
                    if (ui.codeWorker != null) ui.codeWorker.execute(task);
                    else task.run();
                } catch (java.util.concurrent.RejectedExecutionException ignored) { decoding = false; }
            });
        }
    }

    @Override protected void onAttachedToWindow() {
        super.onAttachedToWindow();
        if (vector != null) display(vector);
        else start();
    }

    @Override protected void onDetachedFromWindow() {
        generation++; decoding = false; stopBrowser();
        // Native pictures and their measured height survive list recycling.
        // Pending compilation is canceled; late results cannot replace a row.
        if (vector == null) { result.rendered(State.LOADING, 0); setAlpha(0); }
        super.onDetachedFromWindow();
    }

    @Override protected void onSizeChanged(int width, int height, int oldWidth, int oldHeight) {
        super.onSizeChanged(width, height, oldWidth, oldHeight);
        if (vector != null && !expanded && width != oldWidth) result.rendered(State.READY, image.inlineHeight(width));
    }
}
