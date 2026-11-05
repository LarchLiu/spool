package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import org.json.JSONObject;

/** SessionWorkspacePopover's native, selectable directory and checkout information. */
final class NativeSessionWorkspace {
    final NativePopover popover;
    private final NativeUi ui;
    private String signature;

    NativeSessionWorkspace(NativeUi ui) {
        this.ui = ui;
        popover = NativePopover.workspace(ui);
        popover.content.setPadding(ui.dp(15), ui.dp(13), ui.dp(15), ui.dp(13));
        popover.content.setContentDescription(ui.t("Session directory"));
    }

    void bind(JSONObject workspace, String error) {
        String next = String.valueOf(workspace) + "\n" + error;
        if (next.equals(signature)) return;
        signature = next;
        LinearLayout content = popover.content;
        content.removeAllViews();
        if (workspace == null) {
            TextView notice = ui.text(error.isEmpty() ? ui.t("Loading session directory…") : error, 12, ui.muted);
            notice.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
            content.addView(notice);
            return;
        }
        LinearLayout location = ui.row(); location.setGravity(Gravity.TOP);
        location.addView(ui.decorativeIcon("folder-git", 14));
        String kind = workspace.optString("kind");
        TextView type = ui.text(kind.equals("worktree") ? ui.t("Worktree") : kind.equals("main") ? ui.t("Main checkout") : ui.t("Directory"), 11, ui.muted);
        LinearLayout.LayoutParams typeParams = new LinearLayout.LayoutParams(-2, -2); typeParams.setMarginStart(ui.dp(6)); location.addView(type, typeParams);
        TextView path = ui.text(workspace.optString("path"), 11, ui.muted); path.setTextIsSelectable(true);
        LinearLayout.LayoutParams pathParams = new LinearLayout.LayoutParams(0, -2, 1); pathParams.setMarginStart(ui.dp(6)); location.addView(path, pathParams);
        content.addView(location);
        String branch = workspace.optString("branch"), commit = workspace.optString("commit");
        if (!branch.isEmpty() || !commit.isEmpty()) {
            LinearLayout checkout = ui.row();
            checkout.addView(ui.decorativeIcon(branch.isEmpty() ? "git-commit" : "git-branch", 14));
            TextView label = ui.text(branch.isEmpty() ? ui.t("Detached HEAD · {commit}", "commit", commit) : branch, 11, ui.text);
            label.setSingleLine(true); label.setEllipsize(android.text.TextUtils.TruncateAt.END);
            label.setContentDescription(label.getText());
            LinearLayout.LayoutParams labelParams = new LinearLayout.LayoutParams(0, -2, 1); labelParams.setMarginStart(ui.dp(6)); checkout.addView(label, labelParams);
            LinearLayout.LayoutParams checkoutParams = new LinearLayout.LayoutParams(-1, -2); checkoutParams.topMargin = ui.dp(6); content.addView(checkout, checkoutParams);
        }
        if (!workspace.optBoolean("available")) content.addView(NativeFilePreview.notice(ui, ui.t("Session directory is unavailable")));
    }
}
