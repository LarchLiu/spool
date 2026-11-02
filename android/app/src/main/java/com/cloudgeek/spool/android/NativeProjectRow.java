package com.cloudgeek.spool.android;

import android.os.Build;
import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;
import org.json.JSONObject;

/** Web's 31px project row and its independent, permission-aware new-chat action. */
final class NativeProjectRow extends LinearLayout {
    final LinearLayout toggle;
    final TextView title;
    final ImageView folder;
    final FrameLayout create;
    private final NativeUi ui;

    NativeProjectRow(NativeUi ui, Runnable expand, Runnable newChat) {
        super(ui.context); this.ui = ui; setGravity(Gravity.CENTER_VERTICAL); setAddStatesFromChildren(true); setBackground(ui.states(0, 9, 0, ui.hover, 0));
        toggle = ui.row(); toggle.setPadding(ui.dp(8), ui.dp(5), ui.dp(8), ui.dp(5)); toggle.setFocusable(true); toggle.setOnClickListener(view -> expand.run());
        folder = ui.decorativeIcon("project-folder", 15); folder.setColorFilter(ui.text); toggle.addView(folder);
        title = ui.text("", 13, ui.text); ui.fixedLineHeight(title, 15f / 13); ui.preciseText(title); title.setSingleLine(true); title.setEllipsize(android.text.TextUtils.TruncateAt.END);
        LayoutParams name = new LayoutParams(0, -2, 1); name.setMarginStart(ui.dp(9)); toggle.addView(title, name); addView(toggle, new LayoutParams(0, ui.dp(25), 1));
        create = new FrameLayout(ui.context) { @Override public void setEnabled(boolean enabled) { super.setEnabled(enabled); setAlpha(enabled ? 1 : .45f); } }; create.setFocusable(true); create.setBackground(ui.states(0, 99, 0, ui.controlHover, 0)); create.setOnClickListener(view -> newChat.run());
        create.addView(ui.decorativeIcon("plus", 14), new FrameLayout.LayoutParams(ui.dp(14), ui.dp(14), Gravity.CENTER)); LayoutParams action = new LayoutParams(ui.dp(27), ui.dp(27)); action.setMarginEnd(ui.dp(4)); addView(create, action);
    }
    void bind(JSONObject project, boolean open, boolean posting) {
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
}
