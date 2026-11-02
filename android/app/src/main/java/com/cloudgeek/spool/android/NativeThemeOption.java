package com.cloudgeek.spool.android;

import android.view.ViewGroup;
import android.widget.ImageView;
import android.widget.TextView;

/** CSS two-row theme tile; auto grid tracks share surplus height when a sibling label wraps. */
final class NativeThemeOption extends ViewGroup {
    private final NativeUi ui;
    private final ImageView icon, check;
    private final TextView label;

    NativeThemeOption(NativeUi ui, String title, String symbol, boolean selected, Runnable click) {
        super(ui.context);
        this.ui = ui;
        setBackground(ui.states(selected ? ui.soft : ui.input, 11, selected ? ui.primary : ui.line, ui.controlActive, ui.input));
        icon = ui.decorativeIcon(symbol, 21);
        icon.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
        addView(icon);
        label = ui.text(title, 11, ui.text);
        label.setGravity(android.view.Gravity.CENTER);
        ui.fixedLineHeight(label, 1.2f);
        addView(label);
        check = selected ? ui.decorativeIcon("check", 12) : null;
        if (check != null) {
            check.setImageTintList(android.content.res.ColorStateList.valueOf(ui.primary));
            addView(check);
        }
        setContentDescription(title);
        setSelected(selected);
        setFocusable(true);
        setAccessibilityDelegate(new android.view.View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(android.view.View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info);
                info.setClassName("android.widget.Button");
                info.setSelected(selected);
            }
        });
        setOnClickListener(view -> { if (isEnabled()) click.run(); });
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec);
        icon.measure(MeasureSpec.makeMeasureSpec(ui.dp(21), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(ui.dp(21), MeasureSpec.EXACTLY));
        label.measure(MeasureSpec.makeMeasureSpec(Math.max(0, width - ui.dp(12)), MeasureSpec.AT_MOST), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
        if (check != null) check.measure(MeasureSpec.makeMeasureSpec(ui.dp(12), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(ui.dp(12), MeasureSpec.EXACTLY));
        setMeasuredDimension(width, resolveSize(ui.dp(19 + 21 + 12 + 13) + label.getMeasuredHeight(), heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int width = right - left;
        float extra = Math.max(0, bottom - top - ui.dp(65) - label.getMeasuredHeight());
        int x = (width - icon.getMeasuredWidth()) / 2, y = ui.dp(19) + Math.round(extra / 4);
        icon.layout(x, y, x + icon.getMeasuredWidth(), y + icon.getMeasuredHeight());
        x = (width - label.getMeasuredWidth()) / 2;
        y = ui.dp(52) + Math.round(extra * .75f);
        label.layout(x, y, x + label.getMeasuredWidth(), y + label.getMeasuredHeight());
        if (check != null) check.layout(width - ui.dp(20), ui.dp(8), width - ui.dp(8), ui.dp(20));
    }
}
