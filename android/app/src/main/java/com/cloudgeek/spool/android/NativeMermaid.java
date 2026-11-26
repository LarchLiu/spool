package com.cloudgeek.spool.android;

import android.annotation.SuppressLint;
import android.graphics.Color;
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

/** An offline diagram document, isolated from app transport, files and navigation. */
final class NativeMermaid extends FrameLayout {
    enum State { LOADING, READY, ERROR }
    interface Result { void rendered(State state, int height); }
    private static final String DOCUMENT = "https://mermaid.spool.invalid/index.html";
    private final NativeUi ui;
    private final Result result;
    private final boolean expanded;
    private WebView browser;
    private String source = "";
    private int generation;
    private boolean loaded, rendererFailed;
    private final Runnable render = this::render;

    NativeMermaid(NativeUi ui, Result result) {
        this(ui, false, result);
    }

    NativeMermaid(NativeUi ui, boolean expanded, Result result) {
        super(ui.context); this.ui = ui; this.result = result; this.expanded = expanded;
        setAlpha(0);
    }

    void bind(String value) {
        if (source.equals(value)) return;
        source = value; generation++;
        result.rendered(rendererFailed ? State.ERROR : State.LOADING, 0); setAlpha(0);
        removeCallbacks(render);
        if (loaded) postDelayed(render, 120);
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void start() {
        if (browser != null) return;
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
                    setAlpha(0); result.rendered(State.ERROR, 0);
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
        if (loaded && browser != null) browser.evaluateJavascript("window.spoolZoomMermaid(" + JSONObject.quote(action) + ")", null);
    }

    private void failed(WebView owner) {
        post(() -> {
            if (browser != owner) return;
            setAlpha(0); result.rendered(State.ERROR, 0);
        });
    }

    private final class Bridge {
        private final WebView owner;
        Bridge(WebView owner) { this.owner = owner; }
        @JavascriptInterface public void rendered(int revision, boolean ready, int height) {
            post(() -> {
                if (browser != owner || revision != generation) return;
                int pixels = ready ? ui.dp(Math.max(1, Math.min(20000, height))) : 0;
                if (!expanded) owner.setLayoutParams(new LayoutParams(-1, Math.max(ui.dp(1), pixels)));
                owner.setImportantForAccessibility(ready ? View.IMPORTANT_FOR_ACCESSIBILITY_AUTO : View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
                setAlpha(ready ? 1 : 0);
                result.rendered(ready ? State.READY : State.ERROR, pixels);
            });
        }
    }

    @Override protected void onAttachedToWindow() {
        super.onAttachedToWindow();
        start();
    }

    @Override protected void onDetachedFromWindow() {
        generation++; loaded = false; removeCallbacks(render);
        if (browser != null) {
            WebView view = browser; browser = null;
            view.removeJavascriptInterface("SpoolMermaid");
            view.stopLoading(); removeView(view); view.destroy();
        }
        result.rendered(State.LOADING, 0); setAlpha(0);
        super.onDetachedFromWindow();
    }
}
