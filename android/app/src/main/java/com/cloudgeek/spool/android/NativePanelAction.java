package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.accessibility.AccessibilityNodeInfo;

/** Panel action with application-painted feedback, without a platform button skin. */
class NativePanelAction extends NativeTextView {
    final NativeUi ui;
    private final Paint background = new Paint(Paint.ANTI_ALIAS_FLAG);
    boolean tab, underline = true;

    NativePanelAction(NativeUi ui, String label, Runnable click) {
        super(ui.context); this.ui = ui;
        ui.typography(this, 12, ui.text, 400); ui.cssLineHeight(this, 20f / 12);
        setGravity(Gravity.CENTER_VERTICAL); setPadding(ui.dp(12), 0, ui.dp(12), 0);
        setSingleLine(true); setEllipsize(TextUtils.TruncateAt.END);
        setText(label); setContentDescription(label); setBackground(null);
        setOnClickListener(view -> click.run()); ui.focusOnPress(this); ui.buttonKeys(this);
    }
    @Override protected void onDraw(Canvas canvas) {
        if (isPressed() || isHovered() || (!tab || !underline) && isSelected()) {
            background.setColor(isSelected() ? tab && !underline ? ui.soft : ui.selected : ui.hover);
            canvas.drawRoundRect(0, 0, getWidth(), getHeight(), ui.dp(tab && underline ? 0 : 5), ui.dp(tab && underline ? 0 : 5), background);
        }
        if (tab && underline && isSelected()) {
            background.setColor(ui.primary); canvas.drawRect(0, getHeight() - ui.dp(2), getWidth(), getHeight(), background);
        }
        super.onDraw(canvas);
        if (isFocused()) {
            background.setColor(ui.primary); background.setStyle(Paint.Style.STROKE); background.setStrokeWidth(ui.dp(1));
            canvas.drawRect(ui.dp(1), ui.dp(1), getWidth() - ui.dp(1), getHeight() - ui.dp(1), background);
            background.setStyle(Paint.Style.FILL);
        }
    }
    @Override protected void drawableStateChanged() { super.drawableStateChanged(); invalidate(); }
    @Override public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info); info.setClassName(tab ? "android.widget.TabWidget" : "android.widget.Button");
        info.setSelected(isSelected());
    }
}
