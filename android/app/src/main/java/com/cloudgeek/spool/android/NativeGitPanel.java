package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.Gravity;
import android.view.View;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import java.util.HashSet;
import java.util.Set;
import java.util.function.DoubleConsumer;
import org.json.JSONArray;
import org.json.JSONObject;

/** Directory-scoped Git reads, custom actions and a resizable unified diff. */
final class NativeGitPanel extends LinearLayout {
    final NativePanelTabs tabs;
    final NativeFileSplit split;
    final LinearLayout content, preview;
    private final NativeUi ui;
    private final NativeFileModel state;
    private final NativeFileBrowser.Read read;
    private final Set<String> collapsed = new HashSet<>();
    private final Runnable poll = this::reload;
    private JSONObject data, diff;
    private String mode = "changes", commit = "", selected = "", error = "", diffError = "";
    private int request, diffRequest;
    private boolean active, pending;

    NativeGitPanel(NativeUi ui, NativeFileModel state, NativeFileBrowser.Read read, DoubleConsumer resize) {
        super(ui.context); this.ui = ui; this.state = state; this.read = read; setOrientation(VERTICAL);
        setContentDescription(ui.t("Git panel"));
        tabs = new NativePanelTabs(ui, ui.t("Git views"), false);
        tabs.add("changes", ui.t("Changes"), "", () -> chooseMode("changes"));
        tabs.add("history", ui.t("History"), "", () -> chooseMode("history")); tabs.select(mode); addView(tabs);
        content = ui.column(); ScrollView scroll = new ScrollView(ui.context); scroll.setFillViewport(true); scroll.addView(content);
        preview = ui.column();
        split = new NativeFileSplit(ui, scroll, state.split, value -> { state.split = value; resize.accept(value); });
        addView(split, new LayoutParams(-1, 0, 1));
    }

    void activate(boolean value) {
        active = value; removeCallbacks(poll);
        if (value) {
            reload();
            if (!selected.isEmpty() && diff == null && diffError.isEmpty()) loadDiff();
        } else { ++request; ++diffRequest; pending = false; }
    }
    void scopeChanged() {
        ++request; ++diffRequest; pending = false;
        data = diff = null; selected = commit = error = diffError = ""; render();
        if (active) reload();
    }
    private String path(String view) {
        return state.apiBase() + "/git?view=" + view
            + (state.workspacePath().isEmpty() ? "" : "&workspace=" + android.net.Uri.encode(state.workspacePath()))
            + (commit.isEmpty() ? "" : "&commit=" + android.net.Uri.encode(commit));
    }
    void reload() {
        if (!active || pending) return;
        removeCallbacks(poll);
        if (!state.ready()) { data = diff = null; selected = ""; render(); return; }
        pending = true; int token = ++request, epoch = state.epoch;
        if (data == null) render();
        read.read(path(mode.equals("history") && commit.isEmpty() ? "history" : "changes"), (value, failure) -> {
            if (!active || token != request || epoch != state.epoch) return;
            pending = false; data = failure == null ? value : null; error = failure == null ? "" : failure.getMessage();
            boolean found = false;
            JSONArray files = data == null ? null : data.optJSONArray("files");
            if (files != null) for (int i = 0; i < files.length(); i++) if (selected.equals(files.optJSONObject(i).optString("path"))) found = true;
            if (!found && failure == null) { selected = ""; diff = null; ++diffRequest; }
            render();
            postDelayed(poll, 10_000);
        });
    }
    private void chooseMode(String next) {
        mode = next; commit = selected = ""; data = diff = null; error = diffError = ""; ++request; ++diffRequest; pending = false;
        tabs.select(mode); reload();
    }
    void select(String file) { selected = file; diff = null; diffError = ""; render(); loadDiff(); }
    private void loadDiff() {
        if (!active || selected.isEmpty() || !state.ready()) return;
        int token = ++diffRequest, epoch = state.epoch;
        read.read(path("diff") + "&path=" + android.net.Uri.encode(selected), (value, failure) -> {
            if (!active || token != diffRequest || epoch != state.epoch) return;
            diff = failure == null ? value : null; diffError = failure == null ? "" : failure.getMessage(); renderPreview();
        });
    }
    private NativePanelAction action(String label, Runnable click) { return new NativePanelAction(ui, label, click); }
    private void notice(String value) {
        NativeTextView text = new NativeTextView(ui.context); ui.typography(text, 12, ui.muted, 400);
        text.setText(value); text.setPadding(ui.dp(14), ui.dp(12), ui.dp(14), ui.dp(12)); content.addView(text);
    }
    private void render() {
        content.removeAllViews();
        if (!state.ready()) notice(ui.t("Session directory is unavailable"));
        else if (error != null && !error.isEmpty()) { notice(error); content.addView(action(ui.t("Retry"), this::reload)); }
        else if (data == null) notice(ui.t("Loading Git information…"));
        else if (!data.optBoolean("repository")) notice(ui.t("This directory is not a Git repository."));
        else {
            JSONArray files = data.optJSONArray("files"); if (files == null) files = new JSONArray();
            String branch = data.optString("branch", data.optString("commit")); if (branch.length() > 40) branch = branch.substring(0, 7);
            int add = 0, del = 0;
            for (int i = 0; i < files.length(); i++) { add += files.optJSONObject(i).optInt("additions"); del += files.optJSONObject(i).optInt("deletions"); }
            notice(branch + (mode.equals("changes") || !commit.isEmpty() ? "    " + files.length() + "   +" + add + "  −" + del : ""));
            if (!commit.isEmpty()) content.addView(action("‹ " + ui.t("History") + " · " + commit.substring(0, 7), () -> chooseMode("history")));
            if (mode.equals("history") && commit.isEmpty()) {
                JSONArray commits = data.optJSONArray("commits");
                if (commits == null || commits.length() == 0) notice(ui.t("No commits yet."));
                else for (int i = 0; i < commits.length(); i++) {
                    JSONObject item = commits.optJSONObject(i); String id = item.optString("id");
                    NativePanelAction row = action(item.optString("subject") + "\n" + id.substring(0, Math.min(7, id.length())) + " · " + item.optString("author"), () -> {
                        commit = id; selected = ""; data = diff = null; ++request; pending = false; reload();
                    });
                    row.setSingleLine(false); row.setMaxLines(2); row.setPadding(ui.dp(12), ui.dp(6), ui.dp(12), ui.dp(6)); content.addView(row, new LayoutParams(-1, ui.dp(54)));
                }
            } else {
                group(ui.t("Tracked"), files, false); group(ui.t("Untracked"), files, true);
                if (files.length() == 0) notice(ui.t("No changes."));
                if (data.optBoolean("truncated")) notice(ui.t("Large directories show up to 2,000 entries."));
            }
        }
        renderPreview();
    }
    private void group(String name, JSONArray files, boolean untracked) {
        int count = 0; for (int i = 0; i < files.length(); i++) if (files.optJSONObject(i).optBoolean("untracked") == untracked) count++;
        if (count == 0) return;
        content.addView(action((collapsed.contains(name) ? "› " : "⌄ ") + name + "  " + count, () -> { if (!collapsed.remove(name)) collapsed.add(name); render(); }), new LayoutParams(-1, ui.dp(26)));
        if (collapsed.contains(name)) return;
        for (int i = 0; i < files.length(); i++) {
            JSONObject file = files.optJSONObject(i); if (file.optBoolean("untracked") != untracked) continue;
            String path = file.optString("path"); NativeGitFileRow row = new NativeGitFileRow(ui, file, () -> select(path)); row.setSelected(path.equals(selected));
            content.addView(row, new LayoutParams(-1, ui.dp(30)));
        }
    }
    private void renderPreview() {
        preview.removeAllViews();
        if (selected.isEmpty()) { split.preview(null); return; }
        LinearLayout header = ui.row();
        NativeTextView title = new NativeTextView(ui.context); ui.typography(title, 12, ui.text, 400); title.setSingleLine(true); title.setEllipsize(android.text.TextUtils.TruncateAt.START);
        header.setBackgroundColor(ui.soft);
        title.setGravity(Gravity.CENTER_VERTICAL); title.setText(selected); title.setPadding(ui.dp(12), 0, 0, 0); header.addView(title, new LayoutParams(0, ui.dp(36), 1));
        header.addView(ui.icon("refresh", 14, ui.t("Refresh diff preview"), this::loadDiff), new LayoutParams(ui.dp(32), ui.dp(32)));
        header.addView(ui.icon("close", 15, ui.t("Close diff"), () -> { selected = ""; ++diffRequest; render(); }), new LayoutParams(ui.dp(32), ui.dp(32))); preview.addView(header);
        String message = diffError != null && !diffError.isEmpty() ? diffError : diff == null ? ui.t("Loading diff…") : diff.optBoolean("binary") ? ui.t("Binary file")
            : diff.optString("patch").isEmpty() ? ui.t("No textual changes.") : "";
        if (!message.isEmpty()) {
            NativePanelAction note = action(message + (diffError != null && !diffError.isEmpty() ? "  " + ui.t("Retry") : ""), this::loadDiff);
            note.setSingleLine(false); preview.addView(note, new LayoutParams(-1, ui.dp(60)));
        } else {
            ScrollView vertical = new ScrollView(ui.context); HorizontalScrollView horizontal = new HorizontalScrollView(ui.context); horizontal.setFillViewport(true);
            NativeUnifiedDiff code = new NativeUnifiedDiff(ui, diff.optString("patch"));
            if (code.rows.isEmpty()) preview.addView(action(ui.t("No textual changes."), () -> {}));
            else horizontal.addView(code, new android.widget.FrameLayout.LayoutParams(-2, -2));
            vertical.addView(horizontal, new android.widget.FrameLayout.LayoutParams(-1, -2)); preview.addView(vertical, new LayoutParams(-1, 0, 1));
        }
        if (diff != null && diff.optBoolean("truncated")) preview.addView(action(ui.t("Diff preview is truncated."), () -> {}));
        if (split.preview != preview) split.preview(preview);
    }

    private static final class NativeGitFileRow extends NativePanelAction {
        final String stats; final Paint ink = new Paint(Paint.ANTI_ALIAS_FLAG); final NativeDiffPalette palette;
        final int addWidth;
        final String additions, deletions;
        NativeGitFileRow(NativeUi ui, JSONObject file, Runnable click) {
            super(ui, "", click); palette = new NativeDiffPalette(ui.dark);
            String path = file.optString("path"); int slash = path.lastIndexOf('/');
            String name = slash < 0 ? path : path.substring(slash + 1), directory = slash < 0 ? "" : path.substring(0, slash);
            setText((file.optBoolean("untracked") ? "+" : file.optString("status")) + "  " + name + "  " + directory);
            setContentDescription(path + " · " + file.optString("status"));
            additions = "+" + file.optInt("additions"); deletions = "−" + file.optInt("deletions");
            ink.setTextSize(10 * getResources().getDisplayMetrics().scaledDensity); addWidth = Math.round(ink.measureText(additions));
            stats = file.optBoolean("untracked") ? "" : file.optBoolean("binary") ? ui.t("Binary file") : additions + "  " + deletions;
            setPadding(ui.dp(12), 0, Math.round(ink.measureText(stats)) + ui.dp(18), 0);
        }
        @Override protected void onDraw(Canvas canvas) {
            super.onDraw(canvas); float x = getWidth() - ui.dp(12) - ink.measureText(stats), y = (getHeight() - ink.ascent() - ink.descent()) / 2;
            ink.setColor(palette.added); canvas.drawText(stats, x, y, ink);
            if (stats.startsWith("+")) { ink.setColor(palette.deleted); canvas.drawText(deletions, getWidth() - ui.dp(12) - ink.measureText(deletions), y, ink); }
        }
    }
}
