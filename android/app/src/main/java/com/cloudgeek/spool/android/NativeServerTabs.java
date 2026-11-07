package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.StateListDrawable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.TextUtils;
import android.text.style.ReplacementSpan;
import android.view.Gravity;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import java.util.HashMap;
import java.util.Map;
import java.util.function.Consumer;

/** A shared rounded track with inset server tabs and live connection indicators. */
final class NativeServerTabs extends FrameLayout {
    final LinearLayout tabs;
    final NativeSurface track;
    final HorizontalScrollView scroll;
    final NativeUi.IconButton add;
    private final NativeUi ui;
    private final ServerProfiles profiles;
    private final Map<String, Button> buttons = new HashMap<>();
    private boolean revealSelection = true;

    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose) {
        this(ui, profiles, states, choose, false, null);
    }
    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose, boolean includeDisabled, Runnable create) {
        super(ui.context); this.ui = ui; this.profiles = profiles;
        setPadding(ui.dp(18), ui.dp(8), ui.dp(18), ui.dp(8));
        setBackgroundColor(ui.sidebar);
        track = new NativeSurface(ui, ui.soft, ui.line, 8, 0, 0, 0);
        addView(track, new FrameLayout.LayoutParams(-1, -2));
        scroll = new HorizontalScrollView(ui.context);
        scroll.setHorizontalScrollBarEnabled(false); scroll.setFillViewport(true);
        scroll.setOverScrollMode(OVER_SCROLL_NEVER); scroll.setContentDescription(ui.t("Servers"));
        track.addView(scroll, new LinearLayout.LayoutParams(-1, -2));
        tabs = ui.row(); tabs.setGravity(Gravity.CENTER_VERTICAL);
        tabs.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        scroll.addView(tabs, new HorizontalScrollView.LayoutParams(-2, -2));
        for (ServerProfiles.Server server : profiles.entries) if (includeDisabled || server.enabled) {
            boolean selected = profiles.selected.equals(server.id);
            Button tab = ui.button(server.name, false, () -> choose.accept(server.id));
            SpannableString label = new SpannableString("\ufffc" + server.name);
            label.setSpan(new Dot(ui.dp(8), ui.dp(8)), 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            tab.setText(label);
            tab.setBackground(buttonBackground(selected));
            ui.typography(tab, 12, selected && ui.dark ? ui.thumb : selected || !ui.dark ? ui.text : ui.muted, selected ? 600 : 400);
            tab.setTag(server.id); tab.setSelected(selected); tab.setMinimumWidth(ui.dp(80));
            tab.setMaxLines(1); tab.setHorizontallyScrolling(false);
            tab.setMaxWidth(ui.dp(200)); tab.setEllipsize(TextUtils.TruncateAt.END);
            buttons.put(server.id, tab);
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-2, ui.dp(40), 1); params.setMarginEnd(ui.dp(4));
            tabs.addView(tab, params);
        }
        if (create != null) {
            add = ui.icon("plus", 18, ui.t("Add server"), create);
            add.setBackground(buttonBackground(false));
            tabs.addView(add, new LinearLayout.LayoutParams(ui.dp(40), ui.dp(40)));
        } else add = null;
        status(states);
    }

    private Drawable buttonBackground(boolean selected) {
        int fill = selected ? ui.dark ? androidx.core.graphics.ColorUtils.blendARGB(ui.soft, ui.text, .28f) : ui.elevated : 0x00000000;
        int border = selected && ui.dark ? ui.borderStrong : 0;
        StateListDrawable background = new StateListDrawable();
        background.addState(new int[] {-android.R.attr.state_enabled}, ui.shape(ui.controlActive, 4, border));
        background.addState(new int[] {android.R.attr.state_pressed}, ui.shape(selected ? fill : ui.hover, 4, border));
        background.addState(new int[] {android.R.attr.state_hovered}, ui.shape(selected ? fill : ui.controlHover, 4, border));
        background.addState(new int[] {android.R.attr.state_focused}, ui.shape(selected ? fill : ui.hover, 4, ui.dark ? ui.borderStrong : ui.primary));
        background.addState(new int[0], ui.shape(fill, 4, border));
        return background;
    }

    @Override protected void onSizeChanged(int width, int height, int previousWidth, int previousHeight) {
        super.onSizeChanged(width, height, previousWidth, previousHeight);
        if (width != previousWidth) revealSelection = true;
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        if (!revealSelection) return;
        Button selected = buttons.get(profiles.selected);
        int viewport = scroll.getWidth();
        if (selected == null || viewport <= 0) return;
        int target = scroll.getScrollX();
        if (selected.getLeft() < target) target = selected.getLeft();
        else if (selected.getRight() > target + viewport) target = selected.getRight() - viewport;
        scroll.scrollTo(Math.max(0, Math.min(target, tabs.getWidth() - viewport)), 0);
        revealSelection = false;
    }

    void status(Map<String, String> states) {
        for (ServerProfiles.Server server : profiles.entries) {
            Button tab = buttons.get(server.id); if (tab == null) continue;
            boolean selected = profiles.selected.equals(server.id);
            if (tab.isSelected() != selected) {
                tab.setSelected(selected); tab.setBackground(buttonBackground(selected));
                ui.typography(tab, 12, selected && ui.dark ? ui.thumb : selected || !ui.dark ? ui.text : ui.muted, selected ? 600 : 400);
                revealSelection = true; requestLayout();
            }
            String state = server.enabled ? states.getOrDefault(server.id, "disconnected") : "disconnected";
            int color = statusColor(ui, state);
            Dot dot = ((Spanned) tab.getText()).getSpans(0, 1, Dot.class)[0];
            if (dot.color != color) { dot.color = color; tab.invalidate(); }
            String label = state.equals("connected") ? "Connected" : state.equals("connecting") ? "Connecting…"
                    : state.equals("login") ? "Sign-in required" : state.equals("error") ? "Connection failed" : "Disconnected";
            tab.setContentDescription(server.name + ", " + ui.t(label));
        }
    }

    static CharSequence statusText(NativeUi ui, String label, String state) {
        SpannableString text = new SpannableString("\ufffc" + label);
        Dot dot = new Dot(ui.dp(8), ui.dp(8)); dot.color = statusColor(ui, state);
        text.setSpan(dot, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        return text;
    }

    static int statusColor(NativeUi ui, String state) {
        return state.equals("connected") ? ui.dark ? 0xff4ade80 : 0xff15803d
                : state.equals("connecting") || state.equals("login") ? ui.dark ? 0xfffbbf24 : 0xffb45309
                : state.equals("error") ? ui.error : ui.muted;
    }

    /** An inline dot keeps the entire dot/name group centered, including when ellipsized. */
    static final class Dot extends ReplacementSpan {
        final int size, gap;
        int color;
        Dot(int size, int gap) { this.size = size; this.gap = gap; }
        @Override public int getSize(Paint paint, CharSequence text, int start, int end, Paint.FontMetricsInt metrics) { return size + gap; }
        @Override public void draw(Canvas canvas, CharSequence text, int start, int end, float x, int top, int baseline, int bottom, Paint paint) {
            int previous = paint.getColor();
            paint.setColor(color);
            Paint.FontMetrics metrics = paint.getFontMetrics();
            canvas.drawCircle(x + size / 2f, baseline + (metrics.ascent + metrics.descent) / 2f, size / 2f, paint);
            paint.setColor(previous);
        }
    }
}
