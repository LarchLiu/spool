package com.cloudgeek.spool.android;

import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import org.json.JSONObject;

/** One visible detail page; follows live revisions only while attached/open. */
final class NativeToolBody extends LinearLayout {
    private final NativeUi ui;
    private final Consumer<String> open;
    private final boolean thinking, input;
    private JSONObject summary, page;
    private int offset, epoch;
    private String prefix = "", requested = "", error = "", source = "";
    private boolean loading;
    private boolean refreshQueued;
    private View content;
    private final Runnable refresh = this::load;
    NativeToolBody(NativeUi ui, JSONObject summary, boolean thinking, boolean input, Consumer<String> open) {
        super(ui.context); this.ui = ui; this.thinking = thinking; this.input = input; this.open = open;
        setOrientation(VERTICAL); bind(summary);
    }
    void bind(JSONObject next) {
        if (summary != null && summary.optString("ref").equals(next.optString("ref")) && summary.optString("version").equals(next.optString("version"))) return;
        if (summary != null && !summary.optString("ref").equals(next.optString("ref"))) {
            epoch++; loading = false; refreshQueued = false; removeCallbacks(refresh);
            page = null; offset = 0; prefix = ""; requested = ""; source = ""; content = null;
        }
        summary = next;
        if (!requested.equals(next.optString("version"))) {
            requested = next.optString("version");
            if (isAttachedToWindow() && !loading && !refreshQueued) { refreshQueued = true; postDelayed(refresh, 100); }
        }
        render();
    }
    private void load() {
        refreshQueued = false;
        if (loading) return;
        if (!isAttachedToWindow() || ui.toolDetails == null) return;
        final int token = ++epoch;
        final String version = summary.optString("version");
        int from = offset; String hash = prefix;
        if (page != null && page.optInt("next") == page.optInt("total") && page.optInt("next") - page.optInt("offset") < NativeToolDetails.PAGE_BYTES) {
            from = page.optInt("next"); hash = page.optString("prefix");
        }
        loading = true; error = ""; render();
        ui.toolDetails.load(summary, from, hash, new NativeToolDetails.Callback() {
            public void received(JSONObject next) {
                if (token != epoch || !isAttachedToWindow()) return;
                page = NativeToolDetails.merge(page, next); loading = false; render();
                follow(version);
            }
            public void failed(Exception failure) {
                if (token != epoch || !isAttachedToWindow()) return;
                loading = false; error = failure.getMessage() == null ? failure.toString() : failure.getMessage(); render();
                follow(version);
            }
        });
    }
    private void follow(String completedVersion) {
        if (!completedVersion.equals(summary.optString("version")) && !refreshQueued) { refreshQueued = true; postDelayed(refresh, 100); }
    }
    private void move(int next, String hash) {
        epoch++; loading = false; offset = next; prefix = hash; page = null; content = null; source = ""; load();
    }
    private void render() {
        List<View> views = new ArrayList<>();
        if (page != null) {
            String text = page.optString("text");
            boolean complete = page.optInt("offset") == 0 && page.optInt("next") == page.optInt("total");
            String signature = complete + "\n" + text;
            if (content == null || !signature.equals(source)) {
                long started = System.nanoTime();
                content = !complete ? NativeActivityList.raw(ui, text) : thinking ? NativeMarkdown.detail(ui, text, open)
                        : NativePatchView.bind(ui, null, text, input, new NativePatchView.State());
                source = signature;
                if (ui.toolDetails != null) ui.toolDetails.rendered(summary.optString("kind"), System.nanoTime()-started);
            }
            views.add(content);
            if (!complete) {
                TextView progress = ui.text(ui.t("Showing bytes {start}–{end} of {total}", "start", page.optInt("offset"), "end", page.optInt("next"), "total", page.optInt("total")), 11, ui.subtle);
                views.add(progress);
                if (page.optInt("offset") > 0) views.add(button(ui.t("Back to start"), () -> move(0, "")));
                if (page.optInt("next") < page.optInt("total")) views.add(button(ui.t("Load next section"), () -> move(page.optInt("next"), page.optString("prefix"))));
            }
        } else if (loading || error.isEmpty()) views.add(ui.text(ui.t("Loading tool details…"), 12, ui.muted));
        if (!error.isEmpty()) {
            views.add(ui.text(error, 12, ui.error)); views.add(button(ui.t("Retry"), this::load));
        }
        NativeKeyedViews.sync(this, views);
    }
    private View button(String label, Runnable run) {
        TextView button = ui.text(label, 12, ui.muted); button.setPadding(0, ui.dp(8), 0, ui.dp(8));
        button.setOnClickListener(view -> { ui.beforeDisclosure.accept(this); run.run(); }); return button;
    }
    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); removeCallbacks(refresh); post(refresh); }
    @Override protected void onDetachedFromWindow() { epoch++; loading = false; refreshQueued = false; removeCallbacks(refresh); super.onDetachedFromWindow(); }
}
