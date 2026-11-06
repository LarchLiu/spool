package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.FrameLayout;
import android.widget.TextView;

import java.util.function.Consumer;

/** Reusable UserMessage + MessageFooter; no HTML renderer or platform dialog styling. */
final class NativeMessage extends LinearLayout {
    private final boolean mine;
    private final NativeUi ui;
    private final Consumer<String> open;
    private String source;
    ClipContent content;
    Button toggle;
    LinearLayout bubble;
    final NativeMessageFooter footer;
    private NativeTurnActions actions;
    private boolean mountedToggle;
    float maximumFraction = .9f;

    NativeMessage(NativeUi ui, String source, boolean mine, String author, long timestamp,
            boolean chinese, Consumer<String> open) {
        super(ui.context);
        this.ui = ui; this.open = open;
        this.mine = mine;
        this.source = source;
        setClipChildren(false);
        setOrientation(VERTICAL);
        setGravity(mine ? Gravity.END : Gravity.START);
        createBubble(source);
        if (!mine || !source.isEmpty()) addView(bubble, new LinearLayout.LayoutParams(mine ? -2 : -1, -2));
        footer = new NativeMessageFooter(ui, mine, author, source, timestamp);
        LinearLayout.LayoutParams footerParams = new LinearLayout.LayoutParams(mine ? -2 : -1, ui.dp(28));
        footerParams.topMargin = mine && source.isEmpty() ? 0 : ui.dp(9);
        footerParams.gravity = mine ? Gravity.END : Gravity.START;
        addView(footer, footerParams);
    }

    private void createBubble(String source) {
        mountedToggle = false;
        bubble = new LinearLayout(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                int maximum = Math.min(ui.dp(680), Math.round(MeasureSpec.getSize(widthSpec) * maximumFraction));
                int children = getChildCount(), width = mine ? MeasureSpec.makeMeasureSpec(maximum, MeasureSpec.AT_MOST) : widthSpec;
                super.onMeasure(width, heightSpec);
                // Overflow observation conditionally mounts its control during content measurement.
                if (children != getChildCount()) super.onMeasure(width, heightSpec);
            }
        };
        bubble.setOrientation(VERTICAL);
        content = new ClipContent(ui, mine);
        content.addView(NativeMarkdown.view(ui, source, mine, open), new FrameLayout.LayoutParams(mine ? -2 : -1, -2));
        bubble.addView(content, new LinearLayout.LayoutParams(mine ? -2 : -1, -2));
        toggle = ui.quietButton(ui.t("Show more"), () -> {});
        toggle.setVisibility(GONE);
        if (mine) {
            android.graphics.drawable.GradientDrawable shape = ui.shape(ui.soft, 19, 0);
            shape.setCornerRadii(new float[] {ui.dp(19),ui.dp(19),ui.dp(19),ui.dp(19),ui.dp(5),ui.dp(5),ui.dp(19),ui.dp(19)});
            bubble.setBackground(shape);
            bubble.setPadding(ui.dp(17), ui.dp(13), ui.dp(17), ui.dp(13));
            configureToggle();
            content.overflow = value -> {
                if (value && toggle.getParent() == null) {
                    if (mountedToggle) {
                        toggle = ui.quietButton("", () -> {}); configureToggle();
                    }
                    mountedToggle = true; toggle.setVisibility(VISIBLE);
                    LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-2, -2);
                    params.topMargin = ui.dp(8); bubble.addView(toggle, params);
                } else if (!value && toggle.getParent() != null) {
                    bubble.removeView(toggle); toggle.setVisibility(GONE);
                }
            };
        }
        if (mine && source.isEmpty()) bubble.setVisibility(GONE);
    }

    private void configureToggle() {
        toggle.setText(ui.t(content.expanded ? "Show less" : "Show more"));
        toggle.setTextSize(12);
        toggle.setGravity(Gravity.START | Gravity.CENTER_VERTICAL);
        toggle.setPadding(0, ui.dp(2), 0, ui.dp(2));
        ui.preciseText(toggle);
        ui.fixedLineHeight(toggle, 1.5f);
        toggle.setMinimumHeight(Math.round(toggle.getTextSize() * 1.5f) + ui.dp(4));
        toggle.setVisibility(GONE);
        ui.setButtonIcon(toggle, content.expanded ? "chevron-up" : "chevron-down", 14, true);
        toggle.setCompoundDrawablePadding(ui.dp(4));
        if (android.os.Build.VERSION.SDK_INT >= 30) toggle.setStateDescription(content.expanded ? ui.label("Expanded", "已展开") : ui.label("Collapsed", "已收起"));
        toggle.setOnClickListener(view -> {
            if (view != toggle || toggle.getParent() != bubble || bubble.getParent() != this) return;
            boolean expanded = !content.expanded;
            content.expanded = expanded;
            content.requestLayout();
            toggle.setText(ui.t(expanded ? "Show less" : "Show more"));
            ui.setButtonIcon(toggle, expanded ? "chevron-up" : "chevron-down", 14, true);
            toggle.setCompoundDrawablePadding(ui.dp(4));
            if (android.os.Build.VERSION.SDK_INT >= 30) toggle.setStateDescription(expanded ? ui.label("Expanded", "已展开") : ui.label("Collapsed", "已收起"));
        });
    }

    void restoreExpanded(boolean value) {
        if (!mine || content.expanded == value) return;
        content.expanded = value; configureToggle();
        if (toggle.getParent() == bubble) toggle.setVisibility(VISIBLE);
        content.requestLayout();
    }

    void bindSource(String value) {
        if (source.equals(value)) return;
        boolean wasEmpty = source.isEmpty(); source = value;
        if (mine && value.isEmpty()) {
            removeView(bubble); bubble.setVisibility(GONE);
            content.overflow = null; content.expanded = false;
            content.removeAllViews(); bubble.removeAllViews();
        }
        else if (mine && wasEmpty) {
            // UserMessage is conditionally mounted in Web, independently of its footer.
            createBubble(value); addView(bubble, 0, new LinearLayout.LayoutParams(-2, -2));
        } else NativeMarkdown.bind(content.getChildAt(0), value);
        footer.bindText(value);
        bubble.setVisibility(mine && value.isEmpty() ? GONE : VISIBLE);
        ((LinearLayout.LayoutParams) footer.getLayoutParams()).topMargin = mine && value.isEmpty() ? 0 : Math.round(9 * getResources().getDisplayMetrics().density);
        requestLayout();
    }

    void bindActions(org.json.JSONArray values) {
        if (values == null || values.length() == 0) {
            if (actions != null) removeView(actions);
            return;
        }
        if (actions == null) actions = new NativeTurnActions(ui);
        actions.bind(values);
        if (actions.getParent() == null) {
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2); params.topMargin = ui.dp(10);
            addView(actions, indexOfChild(footer), params);
        }
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        if (mine) {
            // Web's end-aligned footer keeps its intrinsic width, including long-author overflow.
            footer.measure(MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED), heightSpec);
            footer.getLayoutParams().width = footer.getMeasuredWidth();
        }
        super.onMeasure(widthSpec, heightSpec);
    }

    /** CSS 8lh clips the complete block layout, not eight lines of one TextView. */
    static final class ClipContent extends FrameLayout {
        private final int maximum;
        private final boolean collapsible;
        boolean expanded;
        Consumer<Boolean> overflow;
        ClipContent(NativeUi ui, boolean mine) {
            super(ui.context);
            maximum = Math.round(14 * 1.75f * 8 * getResources().getDisplayMetrics().scaledDensity);
            collapsible = mine;
        }
        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            int full = getMeasuredHeight();
            if (overflow != null) overflow.accept(full > maximum + 1);
            if (collapsible && !expanded && full > maximum) setMeasuredDimension(getMeasuredWidth(), maximum);
        }
    }

}
