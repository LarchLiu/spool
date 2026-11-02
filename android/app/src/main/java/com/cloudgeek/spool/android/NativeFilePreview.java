package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.util.function.Consumer;
import org.json.JSONObject;

/** Inline project file preview; no system AlertDialog, remote HTML document or browser image loader. */
final class NativeFilePreview extends LinearLayout {
    final LinearLayout header, meta, mode;
    final ScrollView scroll;
    final NativeUi.IconButton download, close;
    final TextView name, path, size;
    final LinearLayout notices;
    private final NativeUi ui;
    private final NativeImages images;
    private final String project;
    private final Consumer<NativeFileTarget> open;
    private final Consumer<String> external;
    private JSONObject file;
    private NativeFileTarget target;
    private boolean rendered;

    NativeFilePreview(NativeUi ui, String project, NativeImages images, Consumer<NativeFileTarget> open, Consumer<String> external, Consumer<JSONObject> save, Runnable clear) {
        super(ui.context);
        this.ui = ui; this.project = project; this.images = images; this.open = open; this.external = external;
        setOrientation(VERTICAL);
        setContentDescription(ui.t("File preview"));
        header = ui.row();
        header.setPadding(ui.dp(14), ui.dp(6), ui.dp(8), ui.dp(6));
        header.setBackgroundColor(ui.soft);
        android.widget.ImageView icon = ui.decorativeIcon("file", 14);
        icon.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
        header.addView(icon);
        name = ui.text("", 12, ui.text); name.setSingleLine(true); name.setEllipsize(android.text.TextUtils.TruncateAt.END);
        LayoutParams label = new LayoutParams(0, -2, 1); label.setMarginStart(ui.dp(6)); label.setMarginEnd(ui.dp(6));
        header.addView(name, label);
        download = ui.icon("download", 14, ui.t("Download file"), () -> { if (file != null) save.accept(file); });
        close = ui.icon("close", 15, ui.t("Close file preview"), clear);
        header.addView(download); header.addView(close); addView(header);
        meta = ui.row(); meta.setPadding(ui.dp(14), ui.dp(9), ui.dp(14), ui.dp(9));
        path = ui.text("", 10, ui.muted); path.setSingleLine(true); path.setEllipsize(android.text.TextUtils.TruncateAt.START);
        LayoutParams location = new LayoutParams(0, -2, 1); location.setMarginEnd(ui.dp(10));
        meta.addView(path, location); size = ui.text("", 10, ui.muted); meta.addView(size); addView(meta);
        mode = ui.row(); mode.setPadding(ui.dp(12), 0, ui.dp(12), ui.dp(8)); addView(mode);
        scroll = new ScrollView(ui.context); scroll.setFillViewport(false); scroll.setVerticalScrollBarEnabled(false);
        addView(scroll, new LayoutParams(-1, 0, 1));
        notices = ui.column(); addView(notices);
    }

    void bind(JSONObject file, NativeFileTarget target, String error) {
        this.file = file; this.target = target; rendered = false;
        String title = file == null ? target == null ? "" : target.path.substring(target.path.lastIndexOf('/') + 1) : file.optString("name");
        name.setText(title); download.setVisibility(file == null ? GONE : VISIBLE);
        meta.setVisibility(file == null ? GONE : VISIBLE);
        if (file != null) {
            String location = file.optString("path") + (target != null && target.line > 0 ? ":" + target.line : "");
            path.setText(location); path.setContentDescription(location);
            size.setText(NativeFileModel.size(file.optLong("size")));
        }
        notices.removeAllViews();
        scroll.setVisibility(file == null ? GONE : VISIBLE);
        if (!error.isEmpty()) notices.addView(notice(ui, error));
        if (file != null && file.optString("kind").equals("text") && file.optBoolean("truncated"))
            notices.addView(notice(ui, ui.t("Preview truncated. Download the full file to see all content.")));
        renderContent();
    }

    private void renderContent() {
        mode.removeAllViews(); scroll.removeAllViews();
        mode.setVisibility(GONE);
        if (file == null) return;
        String preview = NativeFileModel.previewKind(file);
        // HTML source remains native/selectable; arbitrary CSS/HTML is never put in a WebView.
        if (preview.equals("markdown")) {
            mode.setVisibility(VISIBLE);
            String[] labels = {ui.t("Source"), ui.t("Preview")};
            for (int i = 0; i < labels.length; i++) {
                boolean next = i == 1;
                Button button = ui.button(labels[i], false, () -> { rendered = next; renderContent(); });
                ui.typography(button, 11, rendered == next ? ui.text : ui.muted, 400);
                button.setPadding(ui.dp(9), ui.dp(4), ui.dp(9), ui.dp(4));
                button.setBackground(ui.states(rendered == next ? ui.selected : 0, 5, 0, ui.controlHover, 0));
                button.setSelected(rendered == next);
                LayoutParams params = new LayoutParams(-2, -2); if (i > 0) params.setMarginStart(ui.dp(3));
                mode.addView(button, params);
            }
        }
        if (file.optString("kind").equals("text")) {
            if (rendered && preview.equals("markdown")) {
                View markdown = NativeMarkdown.filePreview(ui, file.optString("content"), address -> {
                    NativeFileTarget link = NativeFileTarget.parse(address, file.optString("path"));
                    if (link != null) open.accept(link);
                    else if (NativeExternalLink.allowed(address)) external.accept(address);
                });
                markdown.setPadding(ui.dp(18), ui.dp(8), ui.dp(18), ui.dp(28));
                scroll.addView(markdown, new ScrollView.LayoutParams(-1, -2));
            } else {
                NativeFileSource source = new NativeFileSource(ui, file.optString("path"), file.optString("content"), target == null ? 0 : target.line);
                HorizontalScrollView horizontal = new HorizontalScrollView(ui.context) {
                    @Override protected void onScrollChanged(int x, int y, int previousX, int previousY) { super.onScrollChanged(x, y, previousX, previousY); source.invalidate(); }
                };
                horizontal.setFillViewport(true); horizontal.setHorizontalScrollBarEnabled(false);
                horizontal.addView(source, new HorizontalScrollView.LayoutParams(-2, -2));
                scroll.addView(horizontal, new ScrollView.LayoutParams(-1, -2));
                source.post(() -> { int top = source.targetTop(); if (top >= 0) scroll.scrollTo(0, Math.max(0, top - (scroll.getHeight() - ui.dp(20)) / 2)); });
            }
        } else if (file.optString("kind").equals("image")) {
            LinearLayout wrapper = ui.column(); wrapper.setGravity(Gravity.CENTER_HORIZONTAL); wrapper.setPadding(ui.dp(14), ui.dp(14), ui.dp(14), ui.dp(14));
            NativeContainedImage image = new NativeContainedImage(ui, false); image.setContentDescription(file.optString("name"));
            wrapper.addView(image, new LayoutParams(-2, -2)); scroll.addView(wrapper, new ScrollView.LayoutParams(-1, -2));
            if (images != null) images.remote(image, NativeFileData.projectPath(project, file.optString("path"), "image"), 2048, error -> {
                if (error != null && image.isAttachedToWindow()) { wrapper.removeAllViews(); wrapper.addView(empty(ui, ui.t("This file cannot be previewed as an image"))); }
            });
        } else scroll.addView(empty(ui, ui.t("This file has no text preview. Download it to open locally.")));
        scroll.scrollTo(0, 0);
    }

    static TextView empty(NativeUi ui, String value) {
        TextView text = ui.text(value, 12, ui.muted); ui.fixedLineHeight(text, 1.7f);
        text.setPadding(ui.dp(16), ui.dp(16), ui.dp(16), ui.dp(16)); return text;
    }
    static TextView notice(NativeUi ui, String value) {
        TextView text = ui.text(value, 12, ui.noticeText); ui.fixedLineHeight(text, 1.7f);
        text.setPadding(ui.dp(14), ui.dp(9), ui.dp(14), ui.dp(9)); text.setBackgroundColor(ui.noticeBg);
        text.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE); return text;
    }
}
