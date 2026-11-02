package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Original SlashCommandMenu row, using the shared Lucide Crosshair geometry. */
final class NativeSlashCommandMenu extends LinearLayout {
    final TextView title, detail, shortcut;
    final android.widget.ImageView symbol;
    final LinearLayout copy;
    private int maximumCopyWidth = -1;

    NativeSlashCommandMenu(NativeUi ui, Runnable select) {
        super(ui.context);
        setGravity(Gravity.CENTER_VERTICAL);
        setPadding(ui.dp(12), ui.dp(12), ui.dp(12), ui.dp(12));
        setBackground(ui.shape(ui.hover, 8, 0));
        symbol = ui.decorativeIcon("crosshair", 18);
        symbol.setImageTintList(android.content.res.ColorStateList.valueOf(ui.text));
        addView(symbol, new LayoutParams(ui.dp(18), ui.dp(18)));
        copy = ui.column();
        title = ui.bold("/goal", 16); ui.typography(title, 16, ui.text, 700); ui.preciseText(title);
        detail = ui.text(ui.t("Set a native agent goal"), 11, ui.subtle); ui.preciseText(detail); ui.normalFallbackLineSpacing(detail);
        copy.addView(title);
        LayoutParams subtitle = new LayoutParams(-1, -2); subtitle.topMargin = ui.dp(4); copy.addView(detail, subtitle);
        LayoutParams description = new LayoutParams(-2, -2); description.leftMargin = ui.dp(12); description.rightMargin = ui.dp(12);
        addView(copy, description);
        addView(new android.view.View(ui.context), new LayoutParams(0, 0, 1));
        shortcut = ui.text("Tab", 11, ui.subtle); shortcut.setTypeface(android.graphics.Typeface.MONOSPACE); ui.preciseText(shortcut);
        addView(shortcut);
        setFocusable(true); setSelected(true);
        setContentDescription("/goal: " + detail.getText());
        setOnClickListener(view -> select.run()); ui.buttonKeys(this);
        setAccessibilityDelegate(new android.view.View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(android.view.View view, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(view, info); info.setClassName("android.widget.Button"); info.setSelected(true);
            }
        });
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        if (MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED) {
            shortcut.measure(MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            LayoutParams description = (LayoutParams) copy.getLayoutParams();
            int maximum = Math.max(0, MeasureSpec.getSize(widthSpec) - getPaddingLeft() - getPaddingRight()
                    - symbol.getLayoutParams().width - description.leftMargin - description.rightMargin - shortcut.getMeasuredWidth());
            if (maximumCopyWidth != maximum) {
                maximumCopyWidth = maximum;
                title.setMaxWidth(maximum); detail.setMaxWidth(maximum);
            }
        }
        super.onMeasure(widthSpec, heightSpec);
    }
}
