package com.cloudgeek.spool.android;

import android.app.Activity;
import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.view.WindowInsets;
import android.widget.FrameLayout;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/** Isolated, network-permission-free device fixtures; never instantiates MainActivity or tsnet. */
public final class NativeVisualProbeActivity extends Activity {
    private final Map<String, View> measured = new LinkedHashMap<>();
    private FrameLayout root;
    private NativeUi ui;
    private String screen;
    private NativeSettings settings;
    private NativeAvatars avatars;
    private boolean destroyed;
    private boolean imeRequested;
    private java.util.concurrent.ExecutorService codeWorker;
    private NativeSyntax syntax;
    private NativeFileSource source;
    private volatile String syntaxError;
    private NativeFileBrowser pdfBrowser;
    private java.util.concurrent.ExecutorService pdfWorker;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        boolean dark = getIntent().getBooleanExtra("dark", false);
        setTheme(dark ? R.style.SpoolDarkTheme : R.style.SpoolTheme);
        String locale = getIntent().getStringExtra("locale");
        ui = new NativeUi(this, dark, NativePreferences.language(locale == null ? "en" : locale, new String[] {"en"}));
        root = NativeWindow.content(this, ui);
        screen = getIntent().getStringExtra("screen"); if (screen == null) screen = "login";
        try {
            if (screen.equals("markdown")) markdown();
            else if (screen.equals("messages")) messages();
            else if (screen.equals("workspace")) workspace();
            else if (screen.equals("source")) source();
            else if (screen.equals("pdf")) pdf();
            else login();
        } catch (Exception error) { root.addView(ui.text(error.getClass().getSimpleName() + ": " + error.getMessage(), 14, ui.error)); }
        setContentView(root); root.requestApplyInsets();
        root.getViewTreeObserver().addOnGlobalLayoutListener(this::report);
        root.post(this::report);
        if (screen.equals("settings") || screen.equals("language")) root.postDelayed(() -> {
            settings();
            if (screen.equals("language")) settings.content.appearance.language.post(() -> {
                settings.content.appearance.language.performClick(); root.post(this::report);
            });
        }, 350);
    }

    private JSONObject fixture() throws Exception {
        return new JSONObject(asset("web-behavior.json"));
    }

    private String asset(String name) throws Exception {
        try (java.io.InputStream input = getAssets().open(name); java.io.ByteArrayOutputStream bytes = new java.io.ByteArrayOutputStream()) {
            byte[] buffer = new byte[8192]; int count;
            while ((count = input.read(buffer)) != -1) bytes.write(buffer, 0, count);
            return new String(bytes.toByteArray(), StandardCharsets.UTF_8);
        }
    }

    private void login() {
        NativeLogin login = new NativeLogin(ui, "", false, () -> {}, this::settings);
        root.addView(login, new FrameLayout.LayoutParams(-1, -1));
        measured.put("login", login); measured.put("username", login.username); measured.put("password", login.password);
        measured.put("submit", login.submit); measured.put("preferences", login.preferences);
    }

    private void settings() {
        settings = new NativeSettings(ui, ui.dark ? "dark" : "light", ui.locale, "native-verify", (theme, language) -> {}, () -> {
            if (settings != null) settings.dismiss();
        });
        if (screen.equals("settings")) {
            NativeConnectionSettings connection = new NativeConnectionSettings(ui,
                    new ConnectionPlan("https://spool.example.com", "https://spool.example.ts.net", "", ConnectionMode.TAILSCALE, false),
                    ignored -> {}, (plan, mode) -> {}, () -> {}, () -> {}, () -> {});
            connection.bind(ConnectionMode.TAILSCALE, false, false, false, true, java.util.Map.of(), "Synthetic offline fixture");
            settings.content.additions.addView(connection);
        }
        settings.show();
        root.post(this::report);
    }

    private LinearLayout scrollPage() {
        ScrollView scroll = new ScrollView(this); scroll.setFillViewport(true);
        LinearLayout page = ui.padded(18, 25); page.setBackgroundColor(ui.elevated);
        scroll.addView(page, new ScrollView.LayoutParams(-1, -2)); root.addView(scroll, new FrameLayout.LayoutParams(-1, -1));
        measured.put("scroll", scroll); measured.put("page", page); return page;
    }

    private void markdown() throws Exception {
        JSONArray cases = fixture().getJSONArray("markdownCases");
        LinearLayout page = scrollPage();
        String selected = getIntent().getStringExtra("case");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i);
            if (selected != null && !selected.equals(item.getString("id"))) continue;
            NativeUi localeUi = new NativeUi(this, ui.dark, item.optString("locale", ui.locale));
            View markdown = NativeMarkdown.view(localeUi, item.getString("text"), false, ignored -> {});
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.bottomMargin = ui.dp(22);
            page.addView(markdown, params); measured.put(item.getString("id"), markdown);
        }
    }

    private void messages() throws Exception {
        JSONArray goldens = new JSONArray(asset("web-fixtures.json"));
        avatars = new NativeAvatars(Runnable::run, (style, seed) -> {
            for (int i = 0; i < goldens.length(); i++) if (goldens.getJSONObject(i).getString("name").equals(style)) return goldens.getJSONObject(i).getString("svg");
            throw new java.io.IOException("Unknown synthetic avatar style");
        });
        LinearLayout page = scrollPage(); JSONArray cases = fixture().getJSONArray("messageCases");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i); if (item.has("attachment")) continue;
            NativeUi localeUi = new NativeUi(this, ui.dark, item.getString("locale")); localeUi.avatars = avatars;
            View row = item.optString("role").equals("assistant") ? new NativeMessage(localeUi, item.getString("text"), false, "", item.optLong("timestamp"), false, ignored -> {})
                    : new NativeUserRow(localeUi, item.optJSONObject("operator"), item.getString("text"), item.optLong("timestamp"), null, ignored -> {});
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.bottomMargin = ui.dp(22); page.addView(row, params);
        }
    }

    private void workspace() {
        LinearLayout page = ui.workspace();
        NativeUi.Header header = ui.new Header("New chat", "Mobile project", this::settings, () -> {}, () -> {});
        page.addView(header); page.addView(ui.divider());
        ScrollView history = new ScrollView(this); history.setFillViewport(true);
        LinearLayout body = ui.padded(18, 25);
        body.addView(ui.welcome(ui.t("What shall we build today?"), ui.t("Describe your idea. Your agent takes it from here."), "Mobile project", ui.t("Run locally. Pick up anytime.")));
        history.addView(body); page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
        NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
        composer.model.bind("gpt-6.1-codex", "codex", "Choose model"); composer.permission.bind("Ask for approval", "shield-question", "Permissions");
        page.addView(composer); root.addView(page, new FrameLayout.LayoutParams(-1, -1));
        measured.put("header", header); measured.put("history", history); measured.put("composer", composer); measured.put("input", composer.input);
    }

    private void source() {
        codeWorker = java.util.concurrent.Executors.newSingleThreadExecutor();
        NativeSyntaxEngine engine = new NativeSyntaxEngine(path -> new java.io.InputStreamReader(getAssets().open("syntax/" + path), StandardCharsets.UTF_8));
        syntax = new NativeSyntax(codeWorker, (path, contents, dark) -> {
            try { return engine.highlight(path, contents, dark); }
            catch (Exception error) { syntaxError = error.toString(); throw error; }
        }, engine::close);
        ui.syntax = syntax;
        LinearLayout page = scrollPage();
        android.widget.HorizontalScrollView scroll = new android.widget.HorizontalScrollView(this);
        source = new NativeFileSource(ui, "fixture.ts", "export const ready = true;\n\tconst label = 'Native source';\n// 中文と日本語\nfunction render() { return ready; }\n", 2);
        scroll.addView(source); page.addView(scroll, new LinearLayout.LayoutParams(-1, -2)); measured.put("source", source);
    }

    private void pdf() throws Exception {
        pdfWorker = java.util.concurrent.Executors.newSingleThreadExecutor();
        byte[] bytes;
        try (java.io.InputStream input = getAssets().open("pdf-preview.pdf")) { bytes = input.readAllBytes(); }
        NativeFileData data = new NativeFileData("application/octet-stream", getIntent().getBooleanExtra("invalidPdf", false) ? "%PDF-1.4\ninvalid".getBytes(StandardCharsets.UTF_8) : bytes);
        JSONObject metadata = new JSONObject().put("name", "PDF 预览测试.pdf").put("path", "/work/PDF 预览测试.pdf").put("kind", "binary")
                .put("media_type", "application/pdf").put("size", bytes.length).put("external", true);
        pdfBrowser = new NativeFileBrowser(ui, "PDF preview test", new NativeFileModel("pdf-test"), null,
                (owner, path) -> new NativePdfPreview(owner, pdfWorker, target -> data, path),
                (path, done) -> {
                    try { done.done(path.contains("/files?") ? new JSONObject("{\"path\":\".\",\"entries\":[]}") : metadata, null); }
                    catch (Exception error) { done.done(null, error); }
                }, file -> {}, address -> {}, () -> {}, value -> {});
        root.addView(pdfBrowser, new FrameLayout.LayoutParams(-1, -1));
        pdfBrowser.start(); pdfBrowser.select(new NativeFileTarget(metadata.getString("path"), 0, 0));
    }

    private JSONObject bounds(View view) throws Exception {
        int[] position = new int[2]; view.getLocationOnScreen(position);
        return new JSONObject().put("x", position[0]).put("y", position[1]).put("width", view.getWidth()).put("height", view.getHeight());
    }

    private JSONObject snapshot() throws Exception {
            JSONObject boxes = new JSONObject(); for (Map.Entry<String, View> entry : measured.entrySet()) boxes.put(entry.getKey(), bounds(entry.getValue()));
            WindowInsets insets = root.getRootWindowInsets();
            JSONObject data = new JSONObject().put("screen", screen).put("locale", ui.locale).put("dark", ui.dark).put("api", Build.VERSION.SDK_INT)
                    .put("density", getResources().getDisplayMetrics().density).put("scaledDensity", getResources().getDisplayMetrics().scaledDensity)
                    .put("root", bounds(root)).put("paddingTop", root.getPaddingTop()).put("paddingBottom", root.getPaddingBottom()).put("boxes", boxes);
            if (Build.VERSION.SDK_INT >= 30 && insets != null) data.put("imeVisible", insets.isVisible(WindowInsets.Type.ime()));
            if (source != null) {
                android.text.Spanned text = (android.text.Spanned) source.getText();
                int colors = text.getSpans(0, text.length(), android.text.style.ForegroundColorSpan.class).length;
                data.put("syntaxColorSpans", colors).put("syntaxCharacters", text.length());
                if (syntaxError != null) data.put("syntaxError", syntaxError);
            }
            if (pdfBrowser != null) {
                NativePdfPreview pdf = pdfBrowser.preview.pdf;
                data.put("pdfOpen", pdf != null);
                if (pdf != null) {
                    data.put("pdfPosition", pdf.position.getText()).put("pdfZoom", pdf.page.zoom()).put("pdfReady", pdf.status.getVisibility() == View.GONE)
                            .put("pdfStatus", pdf.status.getText()).put("pdfPage", pdf.page.getContentDescription())
                            .put("pdfPageWidth", pdf.page.pageWidth()).put("pdfPageHeight", pdf.page.pageHeight()).put("pdfBitmapPixels", pdf.page.bitmapPixels())
                            .put("pdfScroll", pdf.page.scrollOffset()).put("pdfCachedPages", pdf.page.cachedPages());
                    boxes.put("pdfNext", bounds(pdf.next)).put("pdfPrevious", bounds(pdf.previous)).put("pdfZoomIn", bounds(pdf.zoomIn))
                            .put("pdfFit", bounds(pdf.fit)).put("pdfClose", bounds(pdfBrowser.preview.close)).put("pdfViewport", bounds(pdf.page));
                }
            }
            data.put("settingsShowing", settings != null && settings.isShowing()).put("languageMenuShowing", ui.activePopover != null && ui.activePopover.isShowing());
            if (screen.equals("language") && ui.activePopover != null) {
                JSONArray options = new JSONArray();
                for (int i = 0; i < ui.activePopover.content.getChildCount(); i++) options.put(String.valueOf(ui.activePopover.content.getChildAt(i).getContentDescription()));
                data.put("languageOptions", options);
            }
            return data;
    }

    private void save(String name, JSONObject value) throws Exception {
        try (FileOutputStream output = new FileOutputStream(new File(getFilesDir(), name))) { output.write(value.toString(2).getBytes(StandardCharsets.UTF_8)); }
    }

    private void report() {
        if (root.getWidth() == 0) return;
        try {
            save("probe.json", snapshot());
            if (!imeRequested && screen.equals("workspace") && getIntent().getBooleanExtra("imeProbe", false)) {
                imeRequested = true; root.postDelayed(this::imeProbe, 500);
            }
            if (!destroyed && source != null && syntaxError == null && ((android.text.Spanned) source.getText()).getSpans(0, source.length(), android.text.style.ForegroundColorSpan.class).length == 0)
                root.postDelayed(this::report, 150);
            if (!destroyed && pdfBrowser != null) root.postDelayed(this::report, 150);
        } catch (Exception error) { android.util.Log.e("SpoolNativeVerify", "Cannot save synthetic layout report", error); }
    }

    private void imeProbe() {
        JSONObject result = new JSONObject();
        EditText input = (EditText) measured.get("input");
        android.view.inputmethod.InputMethodManager manager = getSystemService(android.view.inputmethod.InputMethodManager.class);
        try {
            result.put("before", snapshot()); input.requestFocus();
            manager.showSoftInput(input, android.view.inputmethod.InputMethodManager.SHOW_IMPLICIT);
            awaitIme(true, 100, () -> {
                try {
                    result.put("visible", snapshot()); input.setText("Native\nTest\nTest\nTest");
                    root.postDelayed(() -> {
                        try {
                            result.put("grown", snapshot()); manager.hideSoftInputFromWindow(input.getWindowToken(), 0);
                            awaitIme(false, 100, () -> {
                                try { result.put("hidden", snapshot()); save("ime.json", result); }
                                catch (Exception error) { imeError(result, error); }
                            }, result);
                        } catch (Exception error) { imeError(result, error); }
                    }, 500);
                } catch (Exception error) { imeError(result, error); }
            }, result);
        } catch (Exception error) { imeError(result, error); }
    }

    private void awaitIme(boolean visible, int attempts, Runnable ready, JSONObject result) {
        if (destroyed) return;
        WindowInsets insets = root.getRootWindowInsets();
        if (Build.VERSION.SDK_INT >= 30 && insets != null && insets.isVisible(WindowInsets.Type.ime()) == visible) {
            root.postDelayed(() -> { if (!destroyed) ready.run(); }, 350);
        } else if (attempts > 0) root.postDelayed(() -> awaitIme(visible, attempts - 1, ready, result), 100);
        else imeError(result, new IllegalStateException("IME did not become " + visible));
    }

    private void imeError(JSONObject result, Exception error) {
        try { result.put("error", error.toString()); save("ime.json", result); }
        catch (Exception failure) { android.util.Log.e("SpoolNativeVerify", "Cannot save IME result", failure); }
    }

    @Override protected void onDestroy() {
        destroyed = true;
        if (settings != null) settings.dismiss(); if (avatars != null) avatars.clear();
        if (syntax != null) syntax.close(); if (codeWorker != null) codeWorker.shutdown();
        if (pdfBrowser != null) pdfBrowser.deactivate(); if (pdfWorker != null) pdfWorker.shutdown();
        if (ui != null) ui.dismissMenus(); super.onDestroy();
    }
}
