package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ImageView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;
import org.json.JSONArray;
import org.json.JSONObject;

/** Shared recent/project chat row, with independent operator tooltip and open-chat hit targets. */
final class NativeChatLink extends LinearLayout {
    final LinearLayout open;
    final TextView title;
    final NativeChatOperators operators;
    final NativeSpinner spinner;
    final FrameLayout agent;
    final TextView age;
    final String chatID;
    private final NativeUi ui;
    private final String description;
    private final long updatedAt;
    private String updatedTitle = "", notificationLabel = "";
    private boolean pointerHovered;
    ImageView notification;
    private String notificationKind = "";

    NativeChatLink(NativeUi ui, JSONObject chat, boolean nested, boolean selected, String activeBackend, Function<String, JSONObject> person, Runnable select) {
        super(ui.context); setGravity(Gravity.CENTER_VERTICAL); setPadding(ui.dp(nested ? 32 : 9), ui.dp(5), ui.dp(9), ui.dp(5)); setSelected(selected); setAddStatesFromChildren(true); setBackground(ui.states(selected ? ui.selected : 0, 10, 0, ui.hover, selected ? ui.selected : 0));
        this.ui = ui; chatID = chat.optString("id"); updatedAt = chat.optLong("updated_at");
        List<JSONObject> members = new ArrayList<>(); JSONArray participants = chat.optJSONArray("participants"); if (participants != null) for (int i = 0; i < participants.length(); i++) members.add(person.apply(participants.optJSONObject(i).optString("id")));
        operators = members.isEmpty() ? null : new NativeChatOperators(ui, members);
        if (operators != null) { addView(operators); }
        open = ui.row(); open.setFocusable(true); open.setOnClickListener(view -> select.run());
        LayoutParams button = new LayoutParams(0, -1, 1); if (operators != null) button.setMarginStart(ui.dp(9)); addView(open, button);
        if (operators == null) {
            agent = new FrameLayout(ui.context); String backend = selected && !activeBackend.isEmpty() ? activeBackend : chat.optString("backend"); android.widget.ImageView icon = ui.decorativeIcon(backend.equals("claude") ? "claude" : "codex", 14); icon.setColorFilter(ui.text); agent.addView(icon, new FrameLayout.LayoutParams(ui.dp(14), ui.dp(14), Gravity.CENTER)); open.addView(agent, new LayoutParams(ui.dp(16), ui.dp(16)));
        } else agent = null;
        String label = chat.optString("title"); if (label.equals("New chat")) label = ui.t("New chat");
        title = ui.text(label, 13, ui.text); ui.fixedLineHeight(title, 21f / 13); ui.preciseText(title); title.setSingleLine(true); title.setEllipsize(android.text.TextUtils.TruncateAt.END);
        LayoutParams copy = new LayoutParams(0, -1, 1); if (operators == null) copy.setMarginStart(ui.dp(9)); open.addView(title, copy);
        spinner = chat.optString("status").equals("running") ? new NativeSpinner(ui, 11, ui.t("Working"), true) : null;
        if (spinner != null) { LayoutParams at = new LayoutParams(ui.dp(11), ui.dp(11)); at.setMarginStart(ui.dp(13)); at.setMarginEnd(ui.dp(4)); open.addView(spinner, at); }
        description = label + (chat.optBoolean("local_session") ? ", " + ui.t("Local session") : "") + (spinner == null ? "" : ", " + ui.t("Working"));
        age = NativeMessageTime.sidebarAge(updatedAt, System.currentTimeMillis()) == null ? null : ui.text("", 11, ui.muted);
        if (age != null) {
            age.setTag("chat-updated-at"); age.setSingleLine(true); age.setGravity(Gravity.CENTER_VERTICAL); age.setFontFeatureSettings("tnum");
            ui.fixedLineHeight(age, 13f / 11); ui.preciseText(age); age.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
            LayoutParams at = new LayoutParams(-2, -1); at.setMarginStart(ui.dp(9)); open.addView(age, at);
        }
        refreshTime(System.currentTimeMillis());
        open.setAccessibilityDelegate(new View.AccessibilityDelegate() { @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) { super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button"); info.setSelected(selected); } });
    }

    void refreshTime(long now) {
        if (age != null) {
            String label = NativeMessageTime.sidebarAge(updatedAt, now);
            if (!age.getText().toString().equals(label)) age.setText(label);
            NativeMessageTime time = NativeMessageTime.format(updatedAt, ui.locale, now, java.util.TimeZone.getDefault());
            updatedTitle = time == null ? "" : time.title;
            age.setTooltipText(updatedTitle);
        }
        updateAgeVisibility();
    }

    @Override public boolean dispatchHoverEvent(MotionEvent event) {
        pointerHovered = event.getActionMasked() != MotionEvent.ACTION_HOVER_EXIT;
        updateAgeVisibility();
        return super.dispatchHoverEvent(event);
    }

    private void updateAgeVisibility() {
        boolean idle = spinner == null && notificationKind.isEmpty();
        if (age != null) age.setVisibility(idle && !pointerHovered ? VISIBLE : GONE);
        String detail = notificationLabel.isEmpty() ? idle ? updatedTitle : "" : notificationLabel;
        open.setContentDescription(description + (detail.isEmpty() ? "" : ", " + detail));
    }

    void notification(String kind) {
        if (!NativeSessionNotifications.validKind(kind)) kind = "";
        if (notificationKind.equals(kind)) return;
        notificationKind = kind;
        if (kind.isEmpty()) {
            if (notification != null) open.removeView(notification);
            notification = null; notificationLabel = ""; updateAgeVisibility(); return;
        }
        if (notification == null) {
            notification = new ImageView(ui.context);
            notification.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
            LayoutParams at = new LayoutParams(ui.dp(16), ui.dp(16)); at.setMarginStart(ui.dp(8));
            open.addView(notification, at);
        }
        String icon = kind.equals("completed") ? "circle-check" : kind.equals("failed") ? "circle-x" : "shield-question";
        String label = ui.t(kind.equals("completed") ? "Completed" : kind.equals("failed") ? "Failed" : "Approval required");
        notificationLabel = label;
        int color = kind.equals("completed") ? ui.notificationCompleted : kind.equals("failed") ? ui.notificationFailed : ui.notificationApproval;
        notification.setImageDrawable(NativeIcons.drawable(ui.context, icon, color, ui.dp(14)));
        notification.setTag("session-notification:" + kind); notification.setContentDescription(label);
        updateAgeVisibility();
    }
}
