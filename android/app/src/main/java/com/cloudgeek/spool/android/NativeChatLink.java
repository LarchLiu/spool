package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
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
    final View local;
    final FrameLayout agent;

    NativeChatLink(NativeUi ui, JSONObject chat, boolean nested, boolean selected, String activeBackend, Function<String, JSONObject> person, Runnable select) {
        super(ui.context); setGravity(Gravity.CENTER_VERTICAL); setPadding(ui.dp(nested ? 32 : 9), ui.dp(5), ui.dp(9), ui.dp(5)); setSelected(selected); setAddStatesFromChildren(true); setBackground(ui.states(selected ? ui.selected : 0, 10, 0, ui.hover, selected ? ui.selected : 0));
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
        local = chat.optBoolean("local_session") ? ui.decorativeIcon("monitor", 13) : null;
        if (local != null) { LayoutParams at = new LayoutParams(ui.dp(13), ui.dp(13)); at.setMarginStart(ui.dp(9)); open.addView(local, at); local.setContentDescription(ui.t("Local session")); }
        spinner = chat.optString("status").equals("running") ? new NativeSpinner(ui, 11, ui.t("Working"), true) : null;
        if (spinner != null) { LayoutParams at = new LayoutParams(ui.dp(11), ui.dp(11)); at.setMarginStart(ui.dp(13)); at.setMarginEnd(ui.dp(4)); open.addView(spinner, at); }
        open.setContentDescription(label + (local == null ? "" : ", " + ui.t("Local session")) + (spinner == null ? "" : ", " + ui.t("Working")));
        open.setAccessibilityDelegate(new View.AccessibilityDelegate() { @Override public void onInitializeAccessibilityNodeInfo(View view, android.view.accessibility.AccessibilityNodeInfo info) { super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button"); info.setSelected(selected); } });
    }
}
