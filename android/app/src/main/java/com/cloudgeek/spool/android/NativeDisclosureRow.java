package com.cloudgeek.spool.android;

import android.text.TextUtils;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.TextView;

/** CSS flex disclosure row: shrink the label, not the icon/caret/stats, without stretching the label. */
final class NativeDisclosureRow extends FrameLayout {
    final TextView label;
    private final NativeUi ui;
    private final ImageView icon, caret;
    private TextView stats;
    private String symbol;
    private final boolean work, group;
    private final int horizontal, vertical, gap, iconSize;
    private boolean expanded;
    private boolean disclosureEnabled = true;
    private boolean expandedInitialized;
    private CharSequence boundTitle;

    NativeDisclosureRow(NativeUi ui, CharSequence title, String symbol, String statistics,
            boolean work, boolean group, boolean expanded, Runnable toggle) {
        super(ui.context);
        this.ui = ui;
        this.work = work;
        this.group = group;
        this.symbol = symbol;
        horizontal = ui.dp(work ? 4 : 5);
        vertical = ui.dp(work ? 2 : group ? 4 : 3);
        gap = ui.dp(work ? 4 : group ? 8 : 7);
        int size = work || !group ? 12 : 13;
        iconSize = ui.dp(size);
        icon = ui.decorativeIcon(work ? expanded ? "chevron-down" : "chevron-right" : symbol, size);
        addView(icon);
        label = ui.text("", work || group ? 13 : 12, ui.muted);
        label.setText(title);
        boundTitle = title;
        ui.lineHeight(label, work ? 1.2f : 1.5f);
        if (!work) {
            label.setSingleLine(true);
            label.setEllipsize(TextUtils.TruncateAt.END);
        }
        // The row measures this label with AT_MOST. MATCH_PARENT makes TextView
        // synchronously reshape new text at the previous row's width during bind,
        // only to reshape it again in onMeasure at the new available width.
        addView(label, new LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.WRAP_CONTENT));
        caret = work ? null : ui.decorativeIcon(expanded ? "chevron-down" : "chevron-right", size);
        if (caret != null) addView(caret);
        stats = statistics.isEmpty() ? null : ui.text(statistics, 11, ui.subtle);
        if (stats != null) {
            stats.setSingleLine(true);
            stats.setFontFeatureSettings("tnum");
            addView(stats, new LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.WRAP_CONTENT));
        }
        this.expanded = expanded;
        setBackground(ui.states(0, work || group ? 6 : 5, 0, ui.hover, 0));
        setFocusable(true);
        setContentDescription(title);
        setOnClickListener(view -> toggle.run());
        for (int i = 0; i < getChildCount(); i++) getChildAt(i).setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        setAccessibilityDelegate(new AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View host, AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(host, info);
                info.setClassName(disclosureEnabled ? Button.class.getName() : TextView.class.getName());
                if (!disclosureEnabled) return;
                if (android.os.Build.VERSION.SDK_INT >= 30)
                    info.setStateDescription(NativeDisclosureRow.this.expanded ? ui.label("Expanded", "已展开") : ui.label("Collapsed", "已折叠"));
                info.addAction(NativeDisclosureRow.this.expanded ? AccessibilityNodeInfo.AccessibilityAction.ACTION_COLLAPSE : AccessibilityNodeInfo.AccessibilityAction.ACTION_EXPAND);
            }
            @Override public boolean performAccessibilityAction(View host, int action, android.os.Bundle args) {
                if (action == AccessibilityNodeInfo.ACTION_EXPAND || action == AccessibilityNodeInfo.ACTION_COLLAPSE) return disclosureEnabled && performClick();
                return super.performAccessibilityAction(host, action, args);
            }
        });
        setExpanded(expanded);
    }

    @Override public boolean performClick() {
        if (disclosureEnabled) ui.beforeDisclosure.accept(this);
        return super.performClick();
    }

    void setDisclosureEnabled(boolean value) {
        if (disclosureEnabled == value) return;
        disclosureEnabled = value;
        setClickable(value); setFocusable(value);
        if (caret != null) caret.setVisibility(value ? VISIBLE : GONE);
        setBackground(value ? ui.states(0, work || group ? 6 : 5, 0, ui.hover, 0) : null);
        requestLayout();
    }

    void bind(CharSequence title, String symbol, String statistics) {
        bind(title, symbol, statistics, true);
    }

    void bind(CharSequence title, String symbol, String statistics, boolean preservePlainFallback) {
        boolean changed = false;
        NativeTextView text = (NativeTextView) label;
        boolean modeChanged = text.preservePlainFallback != preservePlainFallback;
        text.preservePlainFallback = preservePlainFallback;
        if (modeChanged || !java.util.Objects.equals(boundTitle, title)) {
            boundTitle = title; label.setText(title);
            setContentDescription(title);
            changed = true;
        }
        if (!work && !this.symbol.equals(symbol)) {
            this.symbol = symbol;
            icon.setImageDrawable(NativeIcons.drawable(ui.context, symbol, ui.muted, iconSize));
            changed = true;
        }
        if (statistics.isEmpty()) {
            if (stats != null) { removeView(stats); changed = true; }
            stats = null;
        } else {
            if (stats == null) {
                stats = ui.text(statistics, 11, ui.subtle); stats.setSingleLine(true);
                stats.setFontFeatureSettings("tnum"); stats.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
                addView(stats, new LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.WRAP_CONTENT));
                changed = true;
            } else if (!TextUtils.equals(stats.getText(), statistics)) { stats.setText(statistics); changed = true; }
        }
        if (changed) requestLayout();
    }

    void setExpanded(boolean value) {
        if (expandedInitialized && expanded == value) return;
        expandedInitialized = true;
        expanded = value;
        ImageView image = work ? icon : caret;
        image.setImageDrawable(NativeIcons.drawable(ui.context, value ? "chevron-down" : "chevron-right",
                work ? ui.text : ui.subtle, ui.dp(work || !group ? 12 : 13)));
        setSelected(value);
        updateCaret();
    }

    private void updateCaret() {
        if (caret != null) caret.setAlpha(expanded || isHovered() || isFocused() ? 1f : 0f);
        label.setTextColor(isHovered() || isFocused() ? ui.text : ui.muted);
    }

    @Override protected void drawableStateChanged() {
        super.drawableStateChanged();
        // Hover/focus listeners run before View commits its new drawable state.
        if (label != null) updateCaret();
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int width = MeasureSpec.getSize(widthSpec);
        int unlimited = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
        int fixed = MeasureSpec.makeMeasureSpec(iconSize, MeasureSpec.EXACTLY);
        icon.measure(fixed, fixed);
        if (caret != null && disclosureEnabled) caret.measure(fixed, fixed);
        int reserve = 2 * horizontal + icon.getMeasuredWidth() + gap;
        if (caret != null && disclosureEnabled) reserve += caret.getMeasuredWidth() + gap;
        if (stats != null) {
            stats.measure(MeasureSpec.makeMeasureSpec(Math.max(0, width / 2), MeasureSpec.AT_MOST), unlimited);
            reserve += stats.getMeasuredWidth() + gap;
        }
        label.measure(MeasureSpec.makeMeasureSpec(Math.max(0, width - reserve), MeasureSpec.AT_MOST), unlimited);
        setMeasuredDimension(resolveSize(width, widthSpec), resolveSize(Math.max(label.getMeasuredHeight(), icon.getMeasuredHeight()) + vertical * 2, heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int x = horizontal;
        x = position(icon, x) + gap;
        x = position(label, x) + gap;
        if (caret != null && disclosureEnabled) x = position(caret, x) + gap;
        if (stats != null) position(stats, x);
    }

    private int position(View view, int x) {
        int y = (getMeasuredHeight() - view.getMeasuredHeight()) / 2;
        view.layout(x, y, x + view.getMeasuredWidth(), y + view.getMeasuredHeight());
        return x + view.getMeasuredWidth();
    }
}
