package com.cloudgeek.spool.android;

import android.text.TextUtils;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.ForegroundColorSpan;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.TextView;

/** CSS flex disclosure row: shrink the label, not the icon/caret/stats, without stretching the label. */
final class NativeDisclosureRow extends FrameLayout {
    private static final java.util.regex.Pattern CHANGE_STATS = java.util.regex.Pattern.compile("(?:^|\\s)(\\+\\d+)\\s+(-\\d+)$");
    final TextView label;
    private final NativeUi ui;
    private final ImageView icon, caret;
    private TextView stats;
    private String symbol;
    private final boolean work, group;
    private final java.util.function.LongSupplier now;
    private final int horizontal, vertical, gap, iconSize;
    private boolean expanded;
    private boolean disclosureEnabled = true;
    private boolean expandedInitialized;
    private CharSequence boundTitle;
    private int outcomeColor;
    private TaskPreview taskPreview;

    private final class TaskPreview implements Runnable {
        final TextView summary = secondaryLabel();
        final TextView metadata = secondaryLabel();
        final TextView badge = secondaryLabel();
        NativeActivityModel.Activity activity;

        TaskPreview() { metadata.setFontFeatureSettings("tnum"); }

        private TextView secondaryLabel() {
            TextView text = ui.text("", 11, ui.subtle);
            ui.lineHeight(text, 1.5f);
            text.setSingleLine(true);
            text.setEllipsize(TextUtils.TruncateAt.END);
            text.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
            addView(text);
            return text;
        }

        void bind(NativeActivityModel.Activity next) {
            activity = next;
            org.json.JSONObject task = NativeActivityModel.task(next);
            update(summary, task == null ? "" : NativeActivityModel.taskSummary(task));
            update(badge, task != null && task.optBoolean("background") ? ui.t("Background") : "");
            run();
        }

        void clear() {
            if (activity == null) return;
            activity = null;
            removeCallbacks(this);
            update(summary, ""); update(metadata, ""); update(badge, "");
            setContentDescription(boundTitle);
        }

        @Override public void run() {
            removeCallbacks(this);
            if (activity == null) return;
            update(metadata, NativeActivityModel.taskMetadata(ui, activity, false, now.getAsLong()));
            StringBuilder description = new StringBuilder(activity.label);
            for (CharSequence value : new CharSequence[] {summary.getText(), metadata.getText(), badge.getText(), activity.outcome})
                if (value.length() != 0) description.append(" · ").append(value);
            String text = description.toString();
            if (!TextUtils.equals(getContentDescription(), text)) setContentDescription(text);
            if (isAttachedToWindow() && getWindowVisibility() == VISIBLE && NativeActivityModel.taskStartedAt(activity) > 0)
                postDelayed(this, 1000);
        }

        private void update(TextView view, String text) {
            if (!TextUtils.equals(view.getText(), text)) view.setText(text);
            view.setVisibility(text.isEmpty() ? GONE : VISIBLE);
        }
    }

    NativeDisclosureRow(NativeUi ui, CharSequence title, String symbol, String statistics,
            boolean work, boolean group, boolean expanded, Runnable toggle) {
        this(ui, title, symbol, statistics, work, group, expanded, toggle, System::currentTimeMillis);
    }

    NativeDisclosureRow(NativeUi ui, CharSequence title, String symbol, String statistics,
            boolean work, boolean group, boolean expanded, Runnable toggle, java.util.function.LongSupplier now) {
        super(ui.context);
        this.ui = ui;
        this.now = now;
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
        outcomeColor = ui.error;
        stats = statistics.isEmpty() ? null : createStats(statistics);
        if (stats != null) {
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
        if (taskPreview != null) taskPreview.clear();
        bind(title, symbol, statistics, preservePlainFallback, ui.error);
    }

    void bindTask(NativeActivityModel.Activity activity, boolean preservePlainFallback) {
        if (taskPreview == null) taskPreview = new TaskPreview();
        String status = NativeActivityModel.status(activity);
        bind(activity.label, NativeActivityModel.icon(activity.kind), activity.outcome, preservePlainFallback,
                status.equals("failed") || status.equals("declined") ? ui.error : ui.muted);
        taskPreview.bind(activity);
    }

    private void bind(CharSequence title, String symbol, String statistics, boolean preservePlainFallback, int nextOutcomeColor) {
        boolean changed = false;
        boolean colorChanged = outcomeColor != nextOutcomeColor;
        outcomeColor = nextOutcomeColor;
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
                stats = createStats(statistics);
                addView(stats, new LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.WRAP_CONTENT));
                changed = true;
            } else if (colorChanged || !TextUtils.equals(stats.getText(), statistics)) { stats.setText(statisticsText(statistics)); changed = true; }
        }
        if (changed) requestLayout();
    }

    private TextView createStats(String statistics) {
        TextView view = ui.text("", 11, ui.subtle);
        view.setText(statisticsText(statistics));
        view.setSingleLine(true);
        view.setFontFeatureSettings("tnum");
        view.setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        return view;
    }

    private CharSequence statisticsText(String statistics) {
        java.util.regex.Matcher match = CHANGE_STATS.matcher(statistics);
        boolean changes = match.find();
        SpannableString text = new SpannableString(statistics);
        // Tool rows put the unsuccessful outcome before optional change counts.
        int outcomeEnd = changes ? match.start() : statistics.length();
        while (outcomeEnd > 0 && Character.isWhitespace(statistics.charAt(outcomeEnd - 1))) outcomeEnd--;
        if (outcomeEnd > 0) text.setSpan(new ForegroundColorSpan(outcomeColor), 0, outcomeEnd, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        if (changes) {
            NativeDiffPalette palette = new NativeDiffPalette(ui.dark);
            text.setSpan(new ForegroundColorSpan(palette.added), match.start(1), match.end(1), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            text.setSpan(new ForegroundColorSpan(palette.deleted), match.start(2), match.end(2), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        }
        return text;
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

    @Override protected void onAttachedToWindow() {
        super.onAttachedToWindow();
        if (taskPreview != null) taskPreview.run();
    }

    @Override protected void onDetachedFromWindow() {
        if (taskPreview != null) removeCallbacks(taskPreview);
        super.onDetachedFromWindow();
    }

    @Override protected void onWindowVisibilityChanged(int visibility) {
        super.onWindowVisibilityChanged(visibility);
        if (taskPreview != null) {
            if (visibility == VISIBLE) taskPreview.run();
            else removeCallbacks(taskPreview);
        }
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
        boolean task = taskPreview != null && taskPreview.activity != null;
        if (task && taskPreview.badge.getVisibility() == VISIBLE) {
            taskPreview.badge.measure(MeasureSpec.makeMeasureSpec(width / 3, MeasureSpec.AT_MOST), unlimited);
            reserve += taskPreview.badge.getMeasuredWidth() + gap;
        }
        int available = Math.max(0, width - reserve);
        label.measure(MeasureSpec.makeMeasureSpec(available, task ? MeasureSpec.EXACTLY : MeasureSpec.AT_MOST), unlimited);
        int height = label.getMeasuredHeight();
        if (task) {
            height += measureSecondary(taskPreview.summary, available, unlimited);
            height += measureSecondary(taskPreview.metadata, available, unlimited);
        }
        height = Math.max(height, icon.getMeasuredHeight());
        if (stats != null) height = Math.max(height, stats.getMeasuredHeight());
        if (task && taskPreview.badge.getVisibility() == VISIBLE) height = Math.max(height, taskPreview.badge.getMeasuredHeight());
        setMeasuredDimension(resolveSize(width, widthSpec), resolveSize(height + vertical * 2, heightSpec));
    }

    private int measureSecondary(View view, int width, int heightSpec) {
        if (view.getVisibility() != VISIBLE) return 0;
        view.measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), heightSpec);
        return view.getMeasuredHeight();
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int x = position(icon, horizontal) + gap;
        if (taskPreview != null && taskPreview.activity != null) {
            int y = vertical;
            y = position(label, x, y);
            if (taskPreview.summary.getVisibility() == VISIBLE) y = position(taskPreview.summary, x, y);
            if (taskPreview.metadata.getVisibility() == VISIBLE) position(taskPreview.metadata, x, y);
            x += label.getMeasuredWidth() + gap;
            if (taskPreview.badge.getVisibility() == VISIBLE) x = position(taskPreview.badge, x) + gap;
        } else x = position(label, x) + gap;
        if (stats != null) x = position(stats, x) + gap;
        if (caret != null && disclosureEnabled) position(caret, x);
    }

    private int position(View view, int x, int y) {
        int bottom = y + view.getMeasuredHeight();
        view.layout(x, y, x + view.getMeasuredWidth(), bottom);
        return bottom;
    }

    private int position(View view, int x) {
        int y = (getMeasuredHeight() - view.getMeasuredHeight()) / 2;
        view.layout(x, y, x + view.getMeasuredWidth(), y + view.getMeasuredHeight());
        return x + view.getMeasuredWidth();
    }
}
