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

/** Directory-scoped Git operations, custom actions and a resizable unified diff. */
final class NativeGitPanel extends LinearLayout {
    final NativePanelTabs tabs;
    final NativeFileSplit split;
    final LinearLayout content, preview;
    final LinearLayout remoteArea, commitArea;
    final NativeAutosizeInput commitInput;
    final NativeLastCommit lastCommit;
    NativeFileBrowser.Write write;
    private final NativeUi ui;
    private final NativeFileModel state;
    private final NativeFileBrowser.Read read;
    private final Set<String> collapsed = new HashSet<>();
    private final Runnable poll = this::reload;
    private JSONObject data, diff;
    private JSONObject renderedDiff;
    private String renderedSelected, renderedSide, renderedCommit, renderedDiffError;
    private String mode = "changes", commit = "", selected = "", side = "", error = "", diffError = "", operation = "", operationError = "";
    private boolean composer;
    private NativePanelAction commitButton;
    private LinearLayout commitCollapsed, commitComposer;
    private NativeTextView commitLabel, remoteTarget, remoteBehind, remoteNotice;
    private NativePanelAction remoteButton;
    private NativeUi.IconButton remoteToggle, commitExpand, commitCollapse;
    private String remoteAction = "push";
    private NativePopover remoteMenu;
    private int operationRequest;
    private int request, diffRequest;
    private boolean active, pending;
    private NativeDialog discardDialog;

    NativeGitPanel(NativeUi ui, NativeFileModel state, NativeFileBrowser.Read read, DoubleConsumer resize) {
        super(ui.context); this.ui = ui; this.state = state; this.read = read; setOrientation(VERTICAL); setBackgroundColor(ui.elevated);
        setContentDescription(ui.t("Git panel"));
        tabs = new NativePanelTabs(ui, ui.t("Git views"), false);
        tabs.add("changes", ui.t("Changes"), "", () -> chooseMode("changes"));
        tabs.add("history", ui.t("History"), "", () -> chooseMode("history")); tabs.select(mode); addView(tabs);
        content = ui.column(); ScrollView scroll = new ScrollView(ui.context); scroll.setFillViewport(true); scroll.addView(content);
        preview = ui.column();
        split = new NativeFileSplit(ui, scroll, state.split, value -> { state.split = value; resize.accept(value); });
        addView(split, new LayoutParams(-1, 0, 1));
        commitArea = ui.column(); addView(commitArea);
        remoteArea = ui.column(); remoteArea.setBackgroundColor(androidx.core.graphics.ColorUtils.blendARGB(ui.elevated, ui.soft, .4f)); addView(remoteArea);
        commitInput = new NativeAutosizeInput(ui, ui.t("Enter commit message"), 12, 1.5f, 6);
        commitInput.setContentDescription(ui.t("Commit message")); commitInput.setPadding(ui.dp(10), ui.dp(10), ui.dp(10), ui.dp(10));
        commitInput.setBackgroundColor(ui.elevated); commitInput.setDefaultFocusHighlightEnabled(false);
        commitInput.addTextChangedListener(new android.text.TextWatcher() {
            public void beforeTextChanged(CharSequence s, int start, int count, int after) {}
            public void onTextChanged(CharSequence s, int start, int before, int count) { updateCommitEnabled(); }
            public void afterTextChanged(android.text.Editable s) {}
        });
        lastCommit = new NativeLastCommit(ui); lastCommit.setClickable(false); lastCommit.setFocusable(false);
        lastCommit.setBackgroundColor(ui.soft); ui.typography(lastCommit, 11, ui.muted, 400);
        addView(lastCommit, new LayoutParams(-1, ui.dp(30))); renderFooter();
    }

    void activate(boolean value) {
        active = value; removeCallbacks(poll);
        if (value) {
            reload();
            if (!selected.isEmpty() && diff == null && diffError.isEmpty()) loadDiff();
        } else { ++request; ++diffRequest; pending = false; dismissDiscard(); dismissRemote(); }
    }
    void scopeChanged() {
        dismissDiscard(); dismissRemote();
        ++request; ++diffRequest; ++operationRequest; pending = false;
        data = diff = null; selected = side = commit = error = diffError = operation = operationError = "";
        composer = false; remoteAction = "push"; commitInput.setText(""); render();
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
            if (files != null) for (int i = 0; i < files.length(); i++) {
                JSONObject file = files.optJSONObject(i);
                if (selected.equals(file.optString("path")) && (side.isEmpty() || !commit.isEmpty() || side.equals("staged") && file.optBoolean("staged") || side.equals("unstaged") && unstaged(file))) found = true;
            }
            if (!found && failure == null) { selected = ""; diff = null; ++diffRequest; }
            render();
            postDelayed(poll, 10_000);
        });
    }
    private void chooseMode(String next) {
        dismissDiscard(); dismissRemote();
        mode = next; commit = selected = side = ""; data = diff = null; error = diffError = ""; ++request; ++diffRequest; pending = false;
        tabs.select(mode); reload();
    }
    void select(String file) { select(file, ""); }
    private void select(String file, String selectedSide) { selected = file; side = selectedSide; diff = null; diffError = ""; render(); loadDiff(); }
    private void loadDiff() {
        if (!active || selected.isEmpty() || !state.ready()) return;
        int token = ++diffRequest, epoch = state.epoch;
        read.read(path("diff") + "&path=" + android.net.Uri.encode(selected) + (commit.isEmpty() && !side.isEmpty() ? "&side=" + side : ""), (value, failure) -> {
            if (!active || token != diffRequest || epoch != state.epoch) return;
            diff = failure == null ? value : null; diffError = failure == null ? "" : failure.getMessage(); renderPreview();
        });
    }
    private NativePanelAction action(String label, Runnable click) { return new NativePanelAction(ui, label, click); }
    private void notice(String value) {
        NativeTextView text = new NativeTextView(ui.context); ui.typography(text, 12, ui.muted, 400);
        text.setText(value); text.setPadding(ui.dp(14), ui.dp(12), ui.dp(14), ui.dp(12)); content.addView(text);
    }
    private void renderOperationError() {
        LinearLayout row = ui.row(); row.setGravity(Gravity.TOP); row.setPadding(ui.dp(14), ui.dp(6), ui.dp(8), ui.dp(6));
        NativeTextView text = new NativeTextView(ui.context); ui.typography(text, 11, ui.error, 400); ui.fixedLineHeight(text, 1.6f);
        text.setText(operationError); text.setPadding(0, ui.dp(4), 0, ui.dp(4)); text.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_ASSERTIVE);
        row.addView(text, new LayoutParams(0, -2, 1));
        NativeUi.IconButton close = ui.icon("close", 14, ui.t("Dismiss"), () -> { operationError = ""; content.removeView(row); });
        close.setPadding(ui.dp(7), ui.dp(7), ui.dp(7), ui.dp(7));
        LayoutParams button = new LayoutParams(ui.dp(28), ui.dp(28)); button.setMarginStart(ui.dp(6)); row.addView(close, button); content.addView(row);
    }
    private void render() {
        content.removeAllViews();
        if (!operationError.isEmpty()) renderOperationError();
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
                        commit = id; selected = side = ""; data = diff = null; ++request; pending = false; reload();
                    });
                    row.setSingleLine(false); row.setMaxLines(2); row.setPadding(ui.dp(12), ui.dp(6), ui.dp(12), ui.dp(6)); content.addView(row, new LayoutParams(-1, ui.dp(54)));
                }
            } else {
                if (!commit.isEmpty()) group(ui.t("Tracked"), files, false, "");
                else { group(ui.t("Staged"), files, false, "staged"); group(ui.t("Unstaged"), files, false, "unstaged"); }
                group(ui.t("Untracked"), files, true, commit.isEmpty() ? "unstaged" : "");
                if (files.length() == 0) notice(ui.t("No changes."));
                if (data.optBoolean("truncated")) notice(ui.t("Large directories show up to 2,000 entries."));
            }
        }
        renderPreview();
        renderFooter();
    }
    private boolean unstaged(JSONObject file) { return file.optBoolean("unstaged", !file.optBoolean("staged") || file.optString("status").length() == 2); }
    private boolean matches(JSONObject file, boolean untracked, String groupSide) {
        if (groupSide.equals("staged")) return file.optBoolean("staged");
        return file.optBoolean("untracked") == untracked && (groupSide.isEmpty() || unstaged(file));
    }
    private boolean writable() { return write != null && data != null && data.optBoolean("writable") && operation.isEmpty(); }
    private int stagedCount() {
        JSONArray files = data == null ? null : data.optJSONArray("files"); int count = 0;
        if (files != null) for (int i = 0; i < files.length(); i++) if (files.optJSONObject(i).optBoolean("staged")) count++;
        return count;
    }
    private void updateCommitEnabled() {
        if (commitButton != null) commitButton.setEnabled(writable() && stagedCount() > 0 && !commitInput.getText().toString().trim().isEmpty());
    }
    private void renderFooter() {
        if (commitExpand == null) {
            remoteArea.addView(ui.divider());
            LinearLayout remoteRow = ui.row(); remoteRow.setPadding(ui.dp(8), ui.dp(4), ui.dp(8), ui.dp(4));
            remoteTarget = new NativeTextView(ui.context); ui.typography(remoteTarget, 11, ui.muted, 400);
            remoteTarget.setSingleLine(true); remoteTarget.setEllipsize(android.text.TextUtils.TruncateAt.END); remoteTarget.setPadding(ui.dp(4), 0, ui.dp(8), 0);
            LinearLayout targetRow = ui.row(); targetRow.addView(remoteTarget, new LayoutParams(-2, -2));
            remoteBehind = new NativeTextView(ui.context); ui.typography(remoteBehind, 10, ui.muted, 400);
            remoteBehind.setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, "arrow_down", ui.muted, ui.dp(12)), null, null, null); remoteBehind.setCompoundDrawablePadding(ui.dp(2));
            targetRow.addView(remoteBehind, new LayoutParams(-2, -2));
            targetRow.addOnLayoutChangeListener((view, left, top, right, bottom, oldLeft, oldTop, oldRight, oldBottom) -> {
                int available = right - left - (remoteBehind.getVisibility() == VISIBLE ? remoteBehind.getMeasuredWidth() : 0);
                int maximum = Math.max(0, available);
                if (remoteTarget.getMaxWidth() != maximum) remoteTarget.setMaxWidth(maximum);
            });
            remoteRow.addView(targetRow, new LayoutParams(0, -2, 1));
            LinearLayout split = ui.row(); split.setBackground(ui.shape(ui.input, 4, ui.borderStrong));
            remoteButton = action(ui.t("Push"), () -> mutate(remoteAction, null)); remoteButton.setPadding(ui.dp(6), 0, ui.dp(6), 0); ui.typography(remoteButton, 11, ui.text, 400);
            remoteButton.setCompoundDrawablePadding(ui.dp(3)); split.addView(remoteButton, new LayoutParams(-2, ui.dp(26)));
            View divider = ui.divider(); split.addView(divider, new LayoutParams(ui.dp(1), ui.dp(24)));
            remoteToggle = ui.icon("chevron_down", 12, ui.t("Git remote actions"), this::showRemote);
            remoteToggle.setPadding(ui.dp(7), ui.dp(7), ui.dp(7), ui.dp(7)); remoteToggle.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            split.addView(remoteToggle, new LayoutParams(ui.dp(26), ui.dp(26))); remoteRow.addView(split); remoteArea.addView(remoteRow);
            remoteNotice = new NativeTextView(ui.context); ui.typography(remoteNotice, 11, ui.muted, 400);
            remoteNotice.setText(ui.t("This project does not grant access to the repository root")); remoteNotice.setPadding(ui.dp(12), ui.dp(4), ui.dp(12), ui.dp(4));
            remoteArea.addView(remoteNotice);
            commitArea.addView(ui.divider());
            commitCollapsed = ui.row(); commitCollapsed.setPadding(ui.dp(8), ui.dp(4), ui.dp(8), ui.dp(4));
            commitExpand = ui.icon("chevron_up", 14, ui.t("Expand commit area"), () -> { composer = true; renderFooter(); });
            commitExpand.setPadding(ui.dp(7), ui.dp(7), ui.dp(7), ui.dp(7)); commitExpand.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            commitCollapsed.addView(commitExpand, new LayoutParams(ui.dp(28), ui.dp(28)));
            commitLabel = new NativeTextView(ui.context); ui.typography(commitLabel, 12, ui.text, 400);
            commitLabel.setGravity(Gravity.CENTER_VERTICAL); commitLabel.setPadding(ui.dp(12), 0, ui.dp(12), 0); commitLabel.setSingleLine(true); commitLabel.setEllipsize(android.text.TextUtils.TruncateAt.END);
            commitCollapsed.addView(new View(ui.context), new LayoutParams(0, 1, 1));
            commitCollapsed.addView(commitLabel, new LayoutParams(-2, ui.dp(32))); commitArea.addView(commitCollapsed);
            commitComposer = ui.column(); commitArea.addView(commitComposer);
            commitComposer.addView(commitInput, new LayoutParams(-1, -2));
            LinearLayout actions = ui.row(); actions.setPadding(ui.dp(8), ui.dp(4), ui.dp(8), ui.dp(4));
            commitCollapse = ui.icon("chevron_down", 14, ui.t("Collapse commit area"), () -> { composer = false; renderFooter(); });
            commitCollapse.setPadding(ui.dp(7), ui.dp(7), ui.dp(7), ui.dp(7)); commitCollapse.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
            actions.addView(commitCollapse, new LayoutParams(ui.dp(28), ui.dp(28)));
            actions.addView(new View(ui.context), new LayoutParams(0, 1, 1));
            commitButton = action(ui.t("Commit staged changes"), () -> mutate("commit", null));
            actions.addView(commitButton, new LayoutParams(-2, ui.dp(32))); commitComposer.addView(actions);
        }
        boolean changes = mode.equals("changes") && data != null && data.optBoolean("repository");
        remoteArea.setVisibility(changes ? VISIBLE : GONE);
        if (!changes) dismissRemote();
        if (changes) {
            String remote = data.optString("remote");
            String target = remote.isEmpty() ? ui.t("No Git remote configured") : remote + "/" + data.optString("remote_branch");
            remoteTarget.setText(target);
            remoteTarget.setContentDescription(remoteTarget.getText());
            boolean upstream = !data.optString("upstream").isEmpty(), fetch = remoteAction.equals("fetch");
            remoteBehind.setVisibility(upstream ? VISIBLE : GONE); remoteBehind.setText(String.valueOf(data.optInt("behind")));
            String label = ui.t(fetch ? "Fetch" : upstream ? "Push" : "Publish branch");
            remoteButton.setText((upstream ? data.optInt(fetch ? "behind" : "ahead") + " " : "") + label); remoteButton.setContentDescription(label);
            remoteButton.setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, fetch ? "arrow_down" : "arrow_up", ui.text, ui.dp(12)), null, null, null);
            remoteButton.setEnabled(fetch ? canRemote() : canPush()); remoteToggle.setEnabled(canRemote());
            if (!canRemote()) dismissRemote();
            remoteNotice.setVisibility(!remote.isEmpty() && !data.optBoolean("remote_allowed") ? VISIBLE : GONE);
        }
        commitArea.setVisibility(changes && selected.isEmpty() ? VISIBLE : GONE);
        commitCollapsed.setVisibility(composer ? GONE : VISIBLE); commitComposer.setVisibility(composer ? VISIBLE : GONE);
        String label = ui.t("Commit staged changes") + " (" + stagedCount() + ")";
        commitLabel.setText(label); commitExpand.setEnabled(writable());
        commitButton.setText(label); commitButton.setContentDescription(label); commitCollapse.setEnabled(operation.isEmpty());
        commitInput.setEnabled(operation.isEmpty()); updateCommitEnabled();
        lastCommit.setVisibility(changes ? VISIBLE : GONE);
        JSONObject latest = data == null ? null : data.optJSONObject("last_commit");
        lastCommit.bind(latest);
    }
    private boolean canRemote() { return active && writable() && data.optBoolean("remote_allowed") && !data.optString("remote").isEmpty(); }
    private boolean canPush() { return canRemote() && !data.optString("branch").isEmpty() && !data.optString("commit").isEmpty(); }
    private void dismissRemote() { if (remoteMenu != null) { remoteMenu.dismiss(); remoteMenu = null; } }
    private void showRemote() {
        if (!canRemote()) return;
        if (remoteMenu != null && remoteMenu.isShowing()) { dismissRemote(); return; }
        NativePopover menu = new NativePopover(ui, 180, 10, 5); remoteMenu = menu;
        remoteOption(menu, "push", ui.t(data.optString("upstream").isEmpty() ? "Publish branch" : "Push"), "arrow_up", canPush());
        remoteOption(menu, "fetch", ui.t("Fetch"), "arrow_down", true);
        menu.dismissed = () -> { if (remoteMenu == menu) remoteMenu = null; };
        menu.showSelect(remoteToggle, remoteAction.equals("fetch") ? 1 : 0);
    }
    private void remoteOption(NativePopover menu, String value, String label, String icon, boolean enabled) {
        menu.selectOption(label, remoteAction.equals(value), () -> { dismissRemote(); remoteAction = value; renderFooter(); });
        LinearLayout row = (LinearLayout) menu.content.getChildAt(menu.content.getChildCount() - 1); row.setEnabled(enabled); row.setAlpha(enabled ? 1 : .45f);
        LayoutParams at = new LayoutParams(ui.dp(14), ui.dp(14)); at.setMarginEnd(ui.dp(8)); row.addView(ui.decorativeIcon(icon, 14), 0, at);
    }
    private void mutate(String action, JSONArray paths) {
        mutate(action, paths, data);
    }
    private void mutate(String action, JSONArray paths, JSONObject snapshot) {
        if (!writable()) return;
        dismissRemote();
        JSONObject body = new JSONObject();
        try {
            body.put("action", action).put("paths", paths).put("message", action.equals("commit") ? commitInput.getText().toString() : "")
                .put("branch", snapshot.optString("branch")).put("head", snapshot.optString("commit")).put("index", snapshot.optString("index"))
                .put("remote", snapshot.optString("remote")).put("remote_branch", snapshot.optString("remote_branch"));
        } catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        operation = action; operationError = ""; int token = ++operationRequest, epoch = state.epoch;
        String address = path("changes"); render();
        write.write(address, body, (value, failure) -> {
            if (token != operationRequest || epoch != state.epoch) return;
            operation = ""; operationError = failure == null ? "" : failure.getMessage();
            if (operationError == null || failure != null && operationError.isEmpty()) operationError = ui.t("Request failed");
            if (failure == null && action.equals("commit")) { commitInput.setText(""); composer = false; }
            ++request; pending = false; render();
            if (active) { reload(); if (!selected.isEmpty()) loadDiff(); }
        });
    }
    private void dismissDiscard() {
        if (discardDialog != null) { discardDialog.dismiss(); discardDialog = null; }
    }
    @Override protected void onDetachedFromWindow() { dismissDiscard(); dismissRemote(); super.onDetachedFromWindow(); }
    private void confirmDiscard(JSONArray paths) {
        if (!writable()) return;
        dismissDiscard();
        JSONObject snapshot = data;
        NativeDialog dialog = new NativeDialog(ui, ui.t("Discard changes?"), ui.t("Close"));
        discardDialog = dialog;
        dialog.body.addView(ui.text(ui.t("Tracked files will be restored to their staged version. Untracked files will be deleted. Staged changes are kept. This cannot be undone."), 13, ui.text));
        dialog.body.addView(ui.space(12));
        for (int i = 0; i < paths.length(); i++) {
            NativeTextView path = new NativeTextView(ui.context); ui.typography(path, 12, ui.muted, 400);
            path.setText(paths.optString(i)); path.setPadding(0, ui.dp(3), 0, ui.dp(3)); dialog.body.addView(path);
        }
        dialog.body.addView(ui.space(16));
        dialog.actions.addView(action(ui.t("Cancel"), dialog::dismiss), new LayoutParams(-2, ui.dp(36)));
        NativePanelAction discard = action(ui.t("Discard changes"), () -> { dialog.dismiss(); mutate("discard", paths, snapshot); });
        discard.setTextColor(ui.error); dialog.actions.addView(discard, new LayoutParams(-2, ui.dp(36)));
        dialog.setOnDismissListener(ignored -> { if (discardDialog == dialog) discardDialog = null; });
        dialog.show();
    }
    private void addBox(LinearLayout parent, String label, boolean minus, Runnable click) {
        NativeStageBox box = new NativeStageBox(ui, label, minus, click);
        box.setEnabled(writable()); parent.addView(box, new LayoutParams(ui.dp(30), ui.dp(30)));
    }
    private void group(String name, JSONArray files, boolean untracked, String groupSide) {
        int count = 0; JSONArray paths = new JSONArray();
        for (int i = 0; i < files.length(); i++) if (matches(files.optJSONObject(i), untracked, groupSide)) { count++; paths.put(files.optJSONObject(i).optString("path")); }
        if (count == 0) return;
        LinearLayout heading = ui.row();
        heading.addView(action((collapsed.contains(name) ? "› " : "⌄ ") + name + "  " + count, () -> { if (!collapsed.remove(name)) collapsed.add(name); render(); }), new LayoutParams(0, ui.dp(28), 1));
        if (!groupSide.isEmpty()) {
            addBox(heading, ui.t(groupSide.equals("staged") ? "Unstage all" : "Stage all"), groupSide.equals("staged"), () -> mutate(groupSide.equals("staged") ? "unstage" : "stage", paths));
            if (groupSide.equals("unstaged")) addBox(heading, ui.t("Discard all changes"), true, () -> confirmDiscard(paths));
        }
        content.addView(heading);
        if (collapsed.contains(name)) return;
        for (int i = 0; i < files.length(); i++) {
            JSONObject file = files.optJSONObject(i); if (!matches(file, untracked, groupSide)) continue;
            String path = file.optString("path"); NativeGitFileRow row = new NativeGitFileRow(ui, file, groupSide, () -> select(path, groupSide)); row.setSelected(path.equals(selected) && side.equals(groupSide));
            LinearLayout entry = ui.row(); entry.addView(row, new LayoutParams(0, ui.dp(30), 1));
            if (!groupSide.isEmpty()) {
                addBox(entry, ui.t(groupSide.equals("staged") ? "Unstage {path}" : "Stage {path}", "path", path), groupSide.equals("staged"), () -> mutate(groupSide.equals("staged") ? "unstage" : "stage", new JSONArray().put(path)));
                if (groupSide.equals("unstaged")) addBox(entry, ui.t("Discard {path}", "path", path), true, () -> confirmDiscard(new JSONArray().put(path)));
            }
            content.addView(entry);
        }
    }
    private void renderPreview() {
        // List polling and operation controls must not replace an unchanged diff's scroll views.
        if (diff == renderedDiff && selected.equals(renderedSelected) && side.equals(renderedSide)
                && commit.equals(renderedCommit) && java.util.Objects.equals(diffError, renderedDiffError)) return;
        renderedDiff = diff; renderedSelected = selected; renderedSide = side; renderedCommit = commit; renderedDiffError = diffError;
        preview.removeAllViews();
        if (selected.isEmpty()) { split.preview(null); return; }
        LinearLayout header = ui.row();
        NativeTextView title = new NativeTextView(ui.context); ui.typography(title, 12, ui.text, 400); title.setSingleLine(true); title.setEllipsize(android.text.TextUtils.TruncateAt.START);
        header.setBackgroundColor(ui.soft);
        title.setGravity(Gravity.CENTER_VERTICAL); title.setText(selected + (side.isEmpty() || !commit.isEmpty() ? "" : " · " + ui.t(side.equals("staged") ? "Staged" : "Unstaged"))); title.setPadding(ui.dp(12), 0, 0, 0); header.addView(title, new LayoutParams(0, ui.dp(36), 1));
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
        NativeGitFileRow(NativeUi ui, JSONObject file, String side, Runnable click) {
            super(ui, "", click); palette = new NativeDiffPalette(ui.dark);
            String path = file.optString("path"); int slash = path.lastIndexOf('/');
            String name = slash < 0 ? path : path.substring(slash + 1), directory = slash < 0 ? "" : path.substring(0, slash);
            setText((file.optBoolean("untracked") ? "+" : file.optString("status")) + "  " + name + "  " + directory);
            setContentDescription(path + " · " + file.optString("status"));
            additions = "+" + file.optInt(side.isEmpty() ? "additions" : side + "_additions", file.optInt("additions")); deletions = "−" + file.optInt(side.isEmpty() ? "deletions" : side + "_deletions", file.optInt("deletions"));
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

    private static final class NativeStageBox extends NativePanelAction {
        final boolean unstage;
        final Paint ink = new Paint(Paint.ANTI_ALIAS_FLAG);
        NativeStageBox(NativeUi ui, String label, boolean unstage, Runnable click) {
            super(ui, "", click); this.unstage = unstage;
            setContentDescription(label);
        }
        @Override protected void onDraw(Canvas canvas) {
            super.onDraw(canvas); float x = (getWidth() - ui.dp(16)) / 2f, y = (getHeight() - ui.dp(16)) / 2f;
            ink.setColor(ui.borderStrong); ink.setAlpha(isEnabled() ? 255 : 100); ink.setStyle(Paint.Style.STROKE); ink.setStrokeWidth(ui.dp(1));
            canvas.drawRoundRect(x, y, x + ui.dp(16), y + ui.dp(16), ui.dp(3), ui.dp(3), ink);
            ink.setStrokeWidth(ui.dp(1.5f));
            ink.setColor(ui.text);
            canvas.drawLine(x + ui.dp(4), y + ui.dp(8), x + ui.dp(12), y + ui.dp(8), ink);
            if (!unstage) canvas.drawLine(x + ui.dp(8), y + ui.dp(4), x + ui.dp(8), y + ui.dp(12), ink);
            ink.setStyle(Paint.Style.FILL);
        }
    }

    static final class NativeLastCommit extends NativePanelAction {
        final Paint ink = new Paint(Paint.ANTI_ALIAS_FLAG);
        String sha = "";
        NativeLastCommit(NativeUi ui) { super(ui, "", () -> {}); ink.setTextSize(10 * getResources().getDisplayMetrics().scaledDensity); ink.setColor(ui.muted); }
        void bind(JSONObject commit) {
            String id = commit == null ? "" : commit.optString("id"); sha = id.substring(0, Math.min(7, id.length()));
            setPadding(ui.dp(12), 0, Math.round(ink.measureText(sha)) + ui.dp(20), 0);
            setText(commit == null ? ui.t("No commits yet.") : commit.optString("subject"));
            setContentDescription(getText() + (id.isEmpty() ? "" : " · " + id)); invalidate();
        }
        @Override protected void onDraw(Canvas canvas) {
            super.onDraw(canvas); canvas.drawText(sha, getWidth() - ui.dp(12) - ink.measureText(sha), (getHeight() - ink.ascent() - ink.descent()) / 2, ink);
        }
    }
}
