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
    final ScrollView scroll;
    final LinearLayout remoteArea, commitArea;
    final NativeAutosizeInput commitInput;
    final NativeLastCommit lastCommit;
    NativeFileBrowser.Write write;
    private final NativeUi ui;
    private final NativeFileModel state;
    private final NativeFileBrowser.Read read;
    private final Set<String> collapsed = new HashSet<>();
    private final Runnable poll = () -> reload(true);
    private JSONObject data, diff;
    private JSONObject renderedDiff;
    private ScrollView diffScroll;
    private HorizontalScrollView diffHorizontal;
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
    private boolean active, pending, diffPending;
    private NativeDialog discardDialog;
    private JSONObject historyCache;
    private String historyMoreError = "";
    private boolean historyMorePending;
    private final NativeKeyedViews historyViews;
    private final Runnable checkHistoryEnd = this::loadHistoryAtBottom;

    NativeGitPanel(NativeUi ui, NativeFileModel state, NativeFileBrowser.Read read, DoubleConsumer resize) {
        super(ui.context); this.ui = ui; this.state = state; this.read = read; setOrientation(VERTICAL); setBackgroundColor(ui.elevated);
        setContentDescription(ui.t("Git panel"));
        tabs = new NativePanelTabs(ui, ui.t("Git views"), false);
        tabs.add("changes", ui.t("Changes"), "", () -> chooseMode("changes"));
        tabs.add("history", ui.t("History"), "", () -> chooseMode("history")); tabs.select(mode); addView(tabs);
        content = ui.column(); historyViews = new NativeKeyedViews(content);
        scroll = new ScrollView(ui.context); scroll.setFillViewport(true); scroll.addView(content);
        scroll.setOnScrollChangeListener((view, x, y, oldX, oldY) -> loadHistoryAtBottom());
        content.addOnLayoutChangeListener((view, left, top, right, bottom, oldLeft, oldTop, oldRight, oldBottom) -> {
            removeCallbacks(checkHistoryEnd); post(checkHistoryEnd);
        });
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
        } else { ++request; ++diffRequest; pending = diffPending = historyMorePending = false; removeCallbacks(checkHistoryEnd); dismissDiscard(); dismissRemote(); }
    }
    void scopeChanged() {
        dismissDiscard(); dismissRemote();
        ++request; ++diffRequest; ++operationRequest; pending = diffPending = false;
        data = diff = null; selected = side = commit = error = diffError = operation = operationError = "";
        historyCache = null; historyMoreError = ""; historyMorePending = false; scroll.scrollTo(0, 0);
        composer = false; remoteAction = "push"; commitInput.setText(""); render();
        if (active) reload();
    }
    private String path(String view) {
        return state.apiBase() + "/git?view=" + view
            + (state.workspacePath().isEmpty() ? "" : "&workspace=" + android.net.Uri.encode(state.workspacePath()))
            + (commit.isEmpty() ? "" : "&commit=" + android.net.Uri.encode(commit));
    }
    void reload() {
        reload(false);
    }
    void reload(boolean updatePreview) {
        if (!active || pending) return;
        removeCallbacks(poll);
        if (!state.ready()) { data = diff = null; selected = ""; render(); return; }
        pending = true; int token = ++request, epoch = state.epoch;
        if (data == null) render();
        NativeFileBrowser.Completion done = (value, failure) -> {
            if (!active || token != request || epoch != state.epoch) return;
            pending = false; data = failure == null ? value : null; error = failure == null ? "" : failure.getMessage();
            boolean found = false;
            JSONArray files = data == null ? null : data.optJSONArray("files");
            if (files != null) for (int i = 0; i < files.length(); i++) {
                JSONObject file = files.optJSONObject(i);
                if (selected.equals(file.optString("path")) && (side.isEmpty() || !commit.isEmpty() || side.equals("staged") && file.optBoolean("staged") || side.equals("unstaged") && unstaged(file))) found = true;
            }
            if (!found && failure == null) { selected = ""; diff = null; ++diffRequest; diffPending = false; }
            render();
            if (failure == null && found && updatePreview) loadDiff(true);
            postDelayed(poll, 10_000);
        };
        if (historyList()) {
            historyMoreError = "";
            readHistoryPages("", null, 0, token, epoch, (value, failure) -> {
                if (failure == null) historyCache = value;
                done.done(value, failure);
            });
        } else read.read(path("changes"), done);
    }
    private boolean historyList() { return mode.equals("history") && commit.isEmpty(); }
    private void readHistoryPages(String cursor, JSONObject combined, int desired, int token, int epoch, NativeFileBrowser.Completion done) {
        read.read(path("history") + (cursor.isEmpty() ? "" : "&cursor=" + android.net.Uri.encode(cursor)), (value, failure) -> {
            if (!active || token != request || epoch != state.epoch) return;
            if (failure != null) { done.done(null, failure); return; }
            try {
                JSONObject next = combined == null ? new JSONObject(value.toString()) : appendHistory(combined, value);
                int count = desired;
                if (combined == null && historyCache != null && historyCache.optString("commit").equals(next.optString("commit"))) count = historyCache.getJSONArray("commits").length();
                String more = next.optString("next_cursor");
                if (!more.isEmpty() && next.getJSONArray("commits").length() < count) readHistoryPages(more, next, count, token, epoch, done);
                else done.done(next, null);
            } catch (org.json.JSONException error) { done.done(null, error); }
        });
    }
    private static JSONObject appendHistory(JSONObject first, JSONObject page) throws org.json.JSONException {
        JSONObject next = new JSONObject(first.toString()); JSONArray commits = next.getJSONArray("commits"); Set<String> ids = new HashSet<>();
        for (int i = 0; i < commits.length(); i++) ids.add(commits.getJSONObject(i).getString("id"));
        JSONArray more = page.getJSONArray("commits");
        for (int i = 0; i < more.length(); i++) if (ids.add(more.getJSONObject(i).getString("id"))) commits.put(more.getJSONObject(i));
        next.put("next_cursor", page.optString("next_cursor")); return next;
    }
    private void loadHistoryAtBottom() {
        if (!active || pending || !historyList() || data == null || data.optString("next_cursor").isEmpty() || !historyMoreError.isEmpty()) return;
        if (scroll.getHeight() > 0 && scroll.getScrollY() + scroll.getHeight() >= content.getHeight() - ui.dp(80)) loadMoreHistory();
    }
    private void loadMoreHistory() {
        if (!active || pending || !historyList() || data == null || data.optString("next_cursor").isEmpty()) return;
        pending = historyMorePending = true; historyMoreError = ""; removeCallbacks(poll);
        int token = ++request, epoch = state.epoch; JSONObject previous = data; render();
        read.read(path("history") + "&cursor=" + android.net.Uri.encode(previous.optString("next_cursor")), (value, failure) -> {
            if (!active || token != request || epoch != state.epoch) return;
            pending = historyMorePending = false;
            if (failure == null) {
                try { data = historyCache = appendHistory(previous, value); }
                catch (org.json.JSONException invalid) { failure = invalid; }
            }
            if (failure != null) historyMoreError = failure.getMessage() == null ? ui.t("Request failed") : failure.getMessage();
            render(); postDelayed(poll, 10_000);
        });
    }
    private void chooseMode(String next) {
        dismissDiscard(); dismissRemote();
        mode = next; commit = selected = side = ""; data = diff = null; error = diffError = historyMoreError = ""; ++request; ++diffRequest; pending = diffPending = historyMorePending = false;
        scroll.scrollTo(0, 0);
        tabs.select(mode); reload();
    }
    void select(String file) { select(file, ""); }
    private void select(String file, String selectedSide) { selected = file; side = selectedSide; diff = null; diffError = ""; render(); loadDiff(); }
    private void loadDiff() {
        loadDiff(false);
    }
    private void loadDiff(boolean background) {
        if (!active || selected.isEmpty() || !state.ready() || background && diffPending) return;
        diffPending = true;
        int token = ++diffRequest, epoch = state.epoch;
        read.read(path("diff") + "&path=" + android.net.Uri.encode(selected) + (commit.isEmpty() && !side.isEmpty() ? "&side=" + side : ""), (value, failure) -> {
            if (!active || token != diffRequest || epoch != state.epoch) return;
            diffPending = false;
            if (failure == null) diff = value;
            diffError = failure == null ? "" : failure.getMessage(); renderPreview();
        });
    }
    private NativePanelAction action(String label, Runnable click) { return new NativePanelAction(ui, label, click); }
    private NativeTextView noticeView(String value) {
        NativeTextView text = new NativeTextView(ui.context); ui.typography(text, 12, ui.muted, 400);
        text.setText(value); text.setPadding(ui.dp(14), ui.dp(12), ui.dp(14), ui.dp(12)); return text;
    }
    private void notice(String value) { content.addView(noticeView(value)); }
    private String branchLabel() {
        String branch = data.optString("branch"), head = data.optString("commit");
        return !branch.isEmpty() ? branch : !head.isEmpty() ? head.substring(0, Math.min(7, head.length())) : "HEAD";
    }
    private View summaryView(boolean showStats) {
        LinearLayout summary = ui.row(); summary.setPadding(ui.dp(12), ui.dp(4), ui.dp(12), ui.dp(8));
        NativeTextView branch = new NativeTextView(ui.context); ui.typography(branch, 11, ui.muted, 400);
        branch.setText(branchLabel()); branch.setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, "git_branch", ui.muted, ui.dp(13)), null, null, null);
        branch.setCompoundDrawablePadding(ui.dp(6)); summary.addView(branch, new LayoutParams(0, -2, 1));
        if (showStats) {
            JSONArray files = data.optJSONArray("files"); int count = files == null ? 0 : files.length(), additions = 0, deletions = 0;
            for (int i = 0; i < count; i++) { additions += files.optJSONObject(i).optInt("additions"); deletions += files.optJSONObject(i).optInt("deletions"); }
            NativeDiffPalette palette = new NativeDiffPalette(ui.dark);
            LinearLayout stats = ui.row();
            String[] labels = {String.valueOf(count), "+" + additions, "−" + deletions};
            int[] colors = {ui.muted, palette.added, palette.deleted};
            for (int i = 0; i < labels.length; i++) {
                NativeTextView text = new NativeTextView(ui.context); ui.typography(text, 11, colors[i], 400); text.setText(labels[i]);
                LayoutParams at = new LayoutParams(-2, -2); if (i > 0) at.setMarginStart(ui.dp(6)); stats.addView(text, at);
            }
            LayoutParams at = new LayoutParams(-2, -2); at.setMarginStart(ui.dp(8)); summary.addView(stats, at);
        }
        return summary;
    }
    private View operationErrorView() {
        LinearLayout row = ui.row(); row.setGravity(Gravity.TOP); row.setPadding(ui.dp(14), ui.dp(6), ui.dp(8), ui.dp(6));
        NativeTextView text = new NativeTextView(ui.context); ui.typography(text, 11, ui.error, 400); ui.fixedLineHeight(text, 1.6f);
        text.setText(operationError); text.setPadding(0, ui.dp(4), 0, ui.dp(4)); text.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_ASSERTIVE);
        row.addView(text, new LayoutParams(0, -2, 1));
        NativeUi.IconButton close = ui.icon("close", 14, ui.t("Dismiss"), () -> { operationError = ""; content.removeView(row); });
        close.setPadding(ui.dp(7), ui.dp(7), ui.dp(7), ui.dp(7));
        LayoutParams button = new LayoutParams(ui.dp(28), ui.dp(28)); button.setMarginStart(ui.dp(6)); row.addView(close, button); return row;
    }
    private void render() {
        if (historyList() && state.ready() && (error == null || error.isEmpty()) && data != null && data.optBoolean("repository")) {
            renderHistory(); renderPreview(); renderFooter(); return;
        }
        historyViews.begin(); historyViews.end();
        content.removeAllViews();
        if (!operationError.isEmpty()) content.addView(operationErrorView());
        if (!state.ready()) notice(ui.t("Session directory is unavailable"));
        else if (error != null && !error.isEmpty()) { notice(error); content.addView(action(ui.t("Retry"), this::reload)); }
        else if (data == null) notice(ui.t("Loading Git information…"));
        else if (!data.optBoolean("repository")) notice(ui.t("This directory is not a Git repository."));
        else {
            JSONArray files = data.optJSONArray("files"); if (files == null) files = new JSONArray();
            content.addView(summaryView(mode.equals("changes") || !commit.isEmpty()));
            if (!commit.isEmpty()) renderCommitDetail();
            if (!commit.isEmpty()) group(ui.t("Tracked"), files, false, "");
            else { group(ui.t("Staged"), files, false, "staged"); group(ui.t("Unstaged"), files, false, "unstaged"); }
            group(ui.t("Untracked"), files, true, commit.isEmpty() ? "unstaged" : "");
            if (files.length() == 0) notice(ui.t("No changes."));
            if (data.optBoolean("truncated")) notice(ui.t("Large directories show up to 2,000 entries."));
        }
        renderPreview();
        renderFooter();
    }
    private void renderHistory() {
        // Keep mounted commits and their viewport positions while appending pages,
        // showing a retry, or refreshing unchanged history in the background.
        historyViews.begin();
        if (!operationError.isEmpty()) historyViews.use("operation-error", operationError, this::operationErrorView);
        historyViews.use("branch", branchLabel(), () -> summaryView(false));
        JSONArray commits = data.optJSONArray("commits");
        if (commits == null || commits.length() == 0) historyViews.use("empty", "", () -> noticeView(ui.t("No commits yet.")));
        else for (int i = 0; i < commits.length(); i++) {
            JSONObject item = commits.optJSONObject(i); String id = item.optString("id");
            historyViews.use("commit:" + id, item, () -> {
                NativeGitHistoryRow row = new NativeGitHistoryRow(ui, item, () -> {
                    commit = id; selected = side = ""; data = diff = null; ++request; pending = historyMorePending = false; reload();
                });
                row.setLayoutParams(new LayoutParams(-1, -2)); return row;
            });
        }
        if (!historyMoreError.isEmpty()) {
            historyViews.use("page-error", historyMoreError, () -> noticeView(historyMoreError));
            historyViews.use("retry", "", () -> action(ui.t("Retry"), () -> { historyMoreError = ""; loadMoreHistory(); }));
        } else if (historyMorePending) historyViews.use("loading", "", () -> noticeView(ui.t("Loading Git information…")));
        historyViews.end();
    }
    private void renderCommitDetail() {
        LinearLayout detail = ui.column(); detail.setPadding(ui.dp(12), 0, ui.dp(12), ui.dp(12));
        LinearLayout heading = ui.row();
        NativePanelAction back = action(ui.t("History") + " · " + commit.substring(0, 7), () -> chooseMode("history"));
        back.setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, "chevron_left", ui.text, ui.dp(14)), null, null, null); back.setCompoundDrawablePadding(ui.dp(6));
        back.setPadding(ui.dp(6), 0, ui.dp(6), 0);
        LayoutParams backLayout = new LayoutParams(-2, ui.dp(28)); backLayout.setMarginStart(-ui.dp(6)); heading.addView(back, backLayout);
        JSONObject item = data.optJSONObject("selected_commit");
        if (item != null) {
            NativeTextView badges = NativeGitHistoryRow.heading(ui, item, "");
            LayoutParams badgeLayout = new LayoutParams(0, -2, 1); badgeLayout.setMarginStart(ui.dp(5)); heading.addView(badges, badgeLayout);
        }
        detail.addView(heading, new LayoutParams(-1, -2));
        LinearLayout shaRow = ui.row();
        NativeTextView sha = new NativeTextView(ui.context); ui.typography(sha, 11, ui.muted, 400); ui.fixedLineHeight(sha, 1.6f);
        sha.setTypeface(android.graphics.Typeface.MONOSPACE); sha.setText(commit);
        shaRow.addView(sha, new LayoutParams(0, -2, 1));
        NativeUi.IconButton copy = ui.icon("copy", 13, ui.t("Copy SHA"), () -> {}); ui.buttonKeys(copy);
        Runnable copyPadding = () -> { int gap = ui.dp(28) - ui.dp(13); copy.setPadding(gap / 2, gap / 2, gap - gap / 2, gap - gap / 2); }; copyPadding.run();
        String fullSHA = commit;
        Runnable resetCopy = () -> { copy.setKind("copy"); copyPadding.run(); copy.setContentDescription(ui.t("Copy SHA")); copy.setTooltipText(ui.t("Copy SHA")); };
        copy.setTooltipText(ui.t("Copy SHA"));
        copy.setOnClickListener(view -> {
            String label;
            try {
                android.content.ClipboardManager clipboard = (android.content.ClipboardManager) ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);
                if (clipboard == null) throw new IllegalStateException("Clipboard unavailable");
                clipboard.setPrimaryClip(android.content.ClipData.newPlainText("SHA", fullSHA)); copy.setKind("check"); label = ui.t("Copied");
            } catch (RuntimeException failure) { copy.setKind("circle_alert"); label = ui.t("Could not copy SHA"); }
            copyPadding.run(); copy.setContentDescription(label); copy.setTooltipText(label); copy.announceForAccessibility(label);
            copy.removeCallbacks(resetCopy); copy.postDelayed(resetCopy, 2000);
        });
        copy.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
            @Override public void onViewAttachedToWindow(View view) {}
            @Override public void onViewDetachedFromWindow(View view) { copy.removeCallbacks(resetCopy); }
        });
        shaRow.addView(copy, new LayoutParams(ui.dp(28), ui.dp(28))); detail.addView(shaRow, new LayoutParams(-1, -2));
        if (item != null) {
            long time = item.optLong("time"); String stamp = time == 0 ? "" : java.text.DateFormat.getDateTimeInstance(java.text.DateFormat.MEDIUM, java.text.DateFormat.MEDIUM).format(new java.util.Date(time * 1000));
            NativeTextView metadata = new NativeTextView(ui.context); ui.typography(metadata, 11, ui.muted, 400); ui.fixedLineHeight(metadata, 1.6f);
            metadata.setText(item.optString("author") + (stamp.isEmpty() ? "" : " · " + stamp)); metadata.setPadding(0, ui.dp(6), 0, 0); detail.addView(metadata, new LayoutParams(-1, -2));
            NativeTextView message = new NativeTextView(ui.context); ui.typography(message, 12, ui.text, 400); ui.fixedLineHeight(message, 1.6f);
            message.setText(item.optString("message", item.optString("subject"))); message.setPadding(0, ui.dp(8), 0, 0); detail.addView(message, new LayoutParams(-1, -2));
        }
        content.addView(detail, new LayoutParams(-1, -2));
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
            remoteTarget = new NativeTextView(ui.context); ui.typography(remoteTarget, 10, ui.muted, 400);
            remoteTarget.setSingleLine(true); remoteTarget.setEllipsize(android.text.TextUtils.TruncateAt.END); remoteTarget.setPadding(0, 0, ui.dp(6), 0);
            LinearLayout targetRow = ui.row(); targetRow.addView(remoteTarget, new LayoutParams(-2, -2));
            remoteBehind = new NativeTextView(ui.context); ui.typography(remoteBehind, 10, ui.muted, 400);
            remoteBehind.setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, "arrow_down", ui.muted, ui.dp(12)), null, null, null); remoteBehind.setCompoundDrawablePadding(ui.dp(2));
            targetRow.addView(remoteBehind, new LayoutParams(-2, -2));
            targetRow.addOnLayoutChangeListener((view, left, top, right, bottom, oldLeft, oldTop, oldRight, oldBottom) -> {
                int available = right - left - (remoteBehind.getVisibility() == VISIBLE ? remoteBehind.getMeasuredWidth() : 0);
                int maximum = Math.max(0, available);
                if (remoteTarget.getMaxWidth() != maximum) remoteTarget.setMaxWidth(maximum);
            });
            LayoutParams targetLayout = new LayoutParams(0, -2, 1); targetLayout.setMarginEnd(ui.dp(8)); remoteRow.addView(targetRow, targetLayout);
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
            boolean fetch = remoteAction.equals("fetch");
            String remote = fetch ? data.optString("remote") : pushRemote();
            String branch = fetch ? data.optString("remote_branch") : data.optString("push_remote_branch", data.optString("remote_branch"));
            String target = remote.isEmpty() ? ui.t("No Git remote configured") : remote + "/" + branch;
            remoteTarget.setText(target);
            remoteTarget.setContentDescription(remoteTarget.getText());
            boolean upstream = !(fetch ? data.optString("upstream") : pushUpstream()).isEmpty();
            remoteBehind.setVisibility(!data.optString("upstream").isEmpty() ? VISIBLE : GONE); remoteBehind.setText(String.valueOf(data.optInt("behind")));
            String label = ui.t(fetch ? "Fetch" : upstream ? "Push" : "Publish branch");
            remoteButton.setText((upstream ? (fetch ? data.optInt("behind") : data.optInt("push_ahead", data.optInt("ahead"))) + " " : "") + label); remoteButton.setContentDescription(label);
            remoteButton.setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, fetch ? "arrow_down" : "arrow_up", ui.text, ui.dp(12)), null, null, null);
            remoteButton.setEnabled(fetch ? canFetch() : canPush()); remoteToggle.setEnabled(canRemote());
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
    private String pushRemote() { return data == null ? "" : data.optString("push_remote", data.optString("remote")); }
    private String pushUpstream() { return data == null ? "" : data.optString("push_upstream", data.optString("upstream")); }
    private boolean canRemote() { return active && writable() && data.optBoolean("remote_allowed") && (!data.optString("remote").isEmpty() || !pushRemote().isEmpty()); }
    private boolean canFetch() { return canRemote() && !data.optString("remote").isEmpty(); }
    private boolean canPush() { return canRemote() && !pushRemote().isEmpty() && !data.optString("branch").isEmpty() && !data.optString("commit").isEmpty(); }
    private void dismissRemote() { if (remoteMenu != null) { remoteMenu.dismiss(); remoteMenu = null; } }
    private void showRemote() {
        if (!canRemote()) return;
        if (remoteMenu != null && remoteMenu.isShowing()) { dismissRemote(); return; }
        NativePopover menu = new NativePopover(ui, 180, 10, 5); remoteMenu = menu;
        remoteOption(menu, "push", ui.t(pushUpstream().isEmpty() ? "Publish branch" : "Push"), "arrow_up", canPush());
        remoteOption(menu, "fetch", ui.t("Fetch"), "arrow_down", canFetch());
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
                .put("remote", action.equals("push") ? snapshot.optString("push_remote", snapshot.optString("remote")) : snapshot.optString("remote"))
                .put("remote_branch", action.equals("push") ? snapshot.optString("push_remote_branch", snapshot.optString("remote_branch")) : snapshot.optString("remote_branch"));
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
        heading.addView(new NativeGitGroupToggle(ui, name, count, collapsed.contains(name), () -> { if (!collapsed.remove(name)) collapsed.add(name); render(); }), new LayoutParams(0, ui.dp(28), 1));
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
        boolean sameSelection = selected.equals(renderedSelected) && side.equals(renderedSide) && commit.equals(renderedCommit);
        // Polling identical content must preserve the canvas and both scroll positions.
        if (sameSelection && sameDiff(diff, renderedDiff) && java.util.Objects.equals(diffError, renderedDiffError)) return;
        ScrollView previousScroll = sameSelection ? diffScroll : null;
        HorizontalScrollView previousHorizontal = sameSelection ? diffHorizontal : null;
        diffScroll = null; diffHorizontal = null;
        renderedDiff = diff; renderedSelected = selected; renderedSide = side; renderedCommit = commit; renderedDiffError = diffError;
        preview.removeAllViews();
        if (selected.isEmpty()) { split.preview(null); return; }
        LinearLayout header = ui.row();
        NativeTextView title = new NativeTextView(ui.context); ui.typography(title, 12, ui.text, 400); title.setSingleLine(true); title.setEllipsize(android.text.TextUtils.TruncateAt.START);
        header.setBackgroundColor(ui.soft);
        title.setGravity(Gravity.CENTER_VERTICAL); title.setText(selected + (side.isEmpty() || !commit.isEmpty() ? "" : " · " + ui.t(side.equals("staged") ? "Staged" : "Unstaged"))); title.setPadding(ui.dp(12), 0, 0, 0); header.addView(title, new LayoutParams(0, ui.dp(36), 1));
        header.addView(ui.icon("refresh", 14, ui.t("Refresh diff preview"), this::loadDiff), new LayoutParams(ui.dp(32), ui.dp(32)));
        header.addView(ui.icon("close", 15, ui.t("Close diff"), () -> { selected = ""; ++diffRequest; diffPending = false; render(); }), new LayoutParams(ui.dp(32), ui.dp(32))); preview.addView(header);
        boolean failed = diffError != null && !diffError.isEmpty();
        if (failed && diff != null) preview.addView(action(diffError + "  " + ui.t("Retry"), this::loadDiff), new LayoutParams(-1, ui.dp(60)));
        String message = diff == null ? failed ? diffError : ui.t("Loading diff…") : diff.optBoolean("binary") ? ui.t("Binary file")
            : diff.optString("patch").isEmpty() ? ui.t("No textual changes.") : "";
        if (!message.isEmpty()) {
            NativePanelAction note = action(message + (diffError != null && !diffError.isEmpty() ? "  " + ui.t("Retry") : ""), this::loadDiff);
            note.setSingleLine(false); preview.addView(note, new LayoutParams(-1, ui.dp(60)));
        } else {
            ScrollView vertical = previousScroll == null ? new ScrollView(ui.context) : previousScroll;
            HorizontalScrollView horizontal = previousHorizontal == null ? new HorizontalScrollView(ui.context) : previousHorizontal; horizontal.setFillViewport(true);
            horizontal.removeAllViews();
            NativeUnifiedDiff code = new NativeUnifiedDiff(ui, diff.optString("patch"));
            if (code.rows.isEmpty()) preview.addView(action(ui.t("No textual changes."), () -> {}));
            else horizontal.addView(code, new android.widget.FrameLayout.LayoutParams(-2, -2));
            if (previousScroll == null) vertical.addView(horizontal, new android.widget.FrameLayout.LayoutParams(-1, -2));
            preview.addView(vertical, new LayoutParams(-1, 0, 1)); diffScroll = vertical; diffHorizontal = horizontal;
        }
        if (diff != null && diff.optBoolean("truncated")) preview.addView(action(ui.t("Diff preview is truncated."), () -> {}));
        if (split.preview != preview) split.preview(preview);
    }
    private static boolean sameDiff(JSONObject first, JSONObject second) {
        return first == second || first != null && second != null && first.optString("patch").equals(second.optString("patch"))
            && first.optBoolean("binary") == second.optBoolean("binary") && first.optBoolean("truncated") == second.optBoolean("truncated");
    }

    private static final class NativeGitGroupToggle extends NativePanelAction {
        private final Paint ink = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final String count;
        NativeGitGroupToggle(NativeUi ui, String name, int count, boolean collapsed, Runnable click) {
            super(ui, name, click); this.count = String.valueOf(count);
            ui.typography(this, 11, ui.muted, 400); ink.setTextSize(ui.dp(10)); ink.setColor(ui.muted);
            setPadding(ui.dp(12), 0, ui.dp(18) + (int) Math.ceil(ink.measureText(this.count)), 0);
            setCompoundDrawablesRelative(NativeIcons.drawable(ui.context, collapsed ? "chevron_right" : "chevron_down", ui.muted, ui.dp(12)), null, null, null);
            setCompoundDrawablePadding(ui.dp(6)); setContentDescription(name + " " + count);
        }
        @Override protected void onDraw(Canvas canvas) {
            super.onDraw(canvas);
            canvas.drawText(count, getWidth() - ui.dp(12) - ink.measureText(count), (getHeight() - ink.ascent() - ink.descent()) / 2, ink);
        }
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
