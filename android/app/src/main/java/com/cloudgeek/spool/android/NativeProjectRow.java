package com.cloudgeek.spool.android;

import android.os.Build;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

/** Web's 31px project row and its independent, permission-aware new-chat action. */
final class NativeProjectRow extends LinearLayout {
    final LinearLayout toggle;
    final TextView title;
    final ImageView folder;
    final FrameLayout create;
    final LinearLayout status;
    private final NativeUi ui;
    private String projectID = "", statusSignature = "";
    private boolean expanded, pointerHovered;

    NativeProjectRow(NativeUi ui, Runnable expand, Runnable newChat) {
        super(ui.context); this.ui = ui; setGravity(Gravity.CENTER_VERTICAL); setAddStatesFromChildren(true); setBackground(ui.states(0, 9, 0, ui.hover, 0));
        toggle = ui.row(); toggle.setPadding(ui.dp(8), ui.dp(5), ui.dp(8), ui.dp(5)); toggle.setFocusable(true); toggle.setOnClickListener(view -> expand.run());
        folder = ui.decorativeIcon("project-folder", 15); folder.setColorFilter(ui.text); toggle.addView(folder);
        title = ui.text("", 13, ui.text); ui.fixedLineHeight(title, 15f / 13); ui.preciseText(title); title.setSingleLine(true); title.setEllipsize(android.text.TextUtils.TruncateAt.END);
        LayoutParams name = new LayoutParams(0, -2, 1); name.setMarginStart(ui.dp(9)); toggle.addView(title, name); addView(toggle, new LayoutParams(0, ui.dp(25), 1));
        status = ui.row(); status.setTag("project-chat-status"); status.setVisibility(GONE); status.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS); addView(status, new LayoutParams(-2, ui.dp(16)));
        create = new FrameLayout(ui.context) { @Override public void setEnabled(boolean enabled) { super.setEnabled(enabled); setAlpha(enabled ? 1 : .45f); } }; create.setFocusable(true); create.setBackground(ui.states(0, 99, 0, ui.controlHover, 0)); create.setOnClickListener(view -> newChat.run());
        create.addView(ui.decorativeIcon("plus", 14), new FrameLayout.LayoutParams(ui.dp(14), ui.dp(14), Gravity.CENTER)); LayoutParams action = new LayoutParams(ui.dp(27), ui.dp(27)); action.setMarginEnd(ui.dp(4)); addView(create, action);
    }

    @Override public boolean dispatchHoverEvent(MotionEvent event) {
        pointerHovered = event.getActionMasked() != MotionEvent.ACTION_HOVER_EXIT;
        updateStatusVisibility();
        return super.dispatchHoverEvent(event);
    }

    private void updateStatusVisibility() {
        status.setVisibility(status.getChildCount() == 0 ? GONE : VISIBLE);
        status.setAlpha(pointerHovered ? 0 : 1);
    }
    void bind(JSONObject project, boolean open, boolean posting) {
        projectID = project.optString("id"); expanded = open; statusSignature = ""; status.removeAllViews(); status.setVisibility(GONE);
        String name = project.optString("name"); title.setText(name); toggle.setContentDescription(name);
        folder.setImageDrawable(NativeIcons.drawable(ui.context, open ? "project-folder-open" : "project-folder", ui.text, ui.dp(15)));
        create.setEnabled(!posting && project.optBoolean("can_create", true)); create.setContentDescription(ui.t("New chat in {project}", "project", name));
        toggle.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button");
                if (Build.VERSION.SDK_INT >= 30) info.setStateDescription(ui.t(open ? "Expanded" : "Collapsed"));
                info.addAction(open ? android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_COLLAPSE : android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_EXPAND);
            }
            @Override public boolean performAccessibilityAction(View view, int action, android.os.Bundle args) {
                if (action == android.view.accessibility.AccessibilityNodeInfo.ACTION_EXPAND && !open || action == android.view.accessibility.AccessibilityNodeInfo.ACTION_COLLAPSE && open) return toggle.performClick();
                return super.performAccessibilityAction(view, action, args);
            }
        });
    }

    void status(JSONArray chats, NativeSessionNotifications notifications) {
        boolean running = false;
        Set<String> kinds = new HashSet<>();
        if (!expanded) for (int i = 0; i < chats.length(); i++) {
            JSONObject chat = chats.optJSONObject(i);
            if (chat == null || !projectID.equals(chat.optString("project_id"))) continue;
            running |= chat.optString("status").equals("running");
            kinds.add(notifications.kind(chat.optString("id")));
        }
        List<String> pending = new ArrayList<>();
        for (String kind : new String[] {"approval", "failed", "completed"}) if (kinds.contains(kind)) pending.add(kind);
        String next = running + ":" + pending;
        if (statusSignature.equals(next)) return;
        statusSignature = next; status.removeAllViews();
        StringBuilder description = new StringBuilder(title.getText());
        if (running) {
            String label = ui.t("Working");
            NativeSpinner spinner = new NativeSpinner(ui, 11, label, true); spinner.setTag("project-status:running"); spinner.setTooltipText(label);
            LayoutParams at = new LayoutParams(ui.dp(11), ui.dp(11)); at.setMarginStart(ui.dp(4)); at.setMarginEnd(ui.dp(4)); status.addView(spinner, at);
            description.append(", ").append(label);
        }
        for (String kind : pending) {
            String icon = kind.equals("completed") ? "circle-check" : kind.equals("failed") ? "circle-x" : "shield-question";
            String label = ui.t(kind.equals("completed") ? "Completed" : kind.equals("failed") ? "Failed" : "Approval required");
            int color = kind.equals("completed") ? ui.notificationCompleted : kind.equals("failed") ? ui.notificationFailed : ui.notificationApproval;
            ImageView view = new ImageView(ui.context); view.setImageDrawable(NativeIcons.drawable(ui.context, icon, color, ui.dp(14)));
            view.setTag("session-notification:" + kind); view.setContentDescription(label); view.setTooltipText(label);
            LayoutParams at = new LayoutParams(ui.dp(16), ui.dp(16)); if (status.getChildCount() > 0) at.setMarginStart(ui.dp(4)); status.addView(view, at);
            description.append(", ").append(label);
        }
        updateStatusVisibility();
        toggle.setContentDescription(description.toString());
    }
}
