package com.cloudgeek.spool.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.TimeZone;

/** Shared native MessageFooter, including failure feedback and scoped copy-state timers. */
final class NativeMessageFooter extends LinearLayout {
    interface Copy { void write(String text); }
    final TextView author, time;
    final NativeUi.IconButton copy;
    private final NativeUi ui;
    private final boolean mine;
    private final Runnable reset;
    private long timestamp;
    private String text;
    private String copyState = "idle";

    NativeMessageFooter(NativeUi ui, boolean mine, String name, String text, long timestamp) {
        this(ui, mine, name, text, timestamp, System.currentTimeMillis(), TimeZone.getDefault(), value -> {
            ClipboardManager clipboard = (ClipboardManager) ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            if (clipboard == null) throw new IllegalStateException("Clipboard unavailable");
            clipboard.setPrimaryClip(ClipData.newPlainText(ui.t("Message"), value));
        });
    }

    NativeMessageFooter(NativeUi ui, boolean mine, String name, String text, long timestamp,
            long now, TimeZone zone, Copy writer) {
        super(ui.context); this.ui = ui; this.mine = mine;
        this.timestamp = timestamp; this.text = text;
        setGravity((mine ? Gravity.END : Gravity.START) | Gravity.CENTER_VERTICAL);
        NativeMessageTime formatted = NativeMessageTime.format(timestamp, ui.locale, now, zone);
        author = ui.text(name == null ? "" : name, 11, ui.muted);
        author.setSingleLine(); author.setMaxWidth(ui.dp(140)); author.setEllipsize(TextUtils.TruncateAt.END);
        ui.preciseText(author, true); ui.fixedLineHeight(author, 1);
        time = ui.text(formatted == null ? "" : formatted.label, 11, ui.muted);
        time.setSingleLine(); time.setContentDescription(formatted == null ? "" : formatted.title);
        time.setFontFeatureSettings("tnum"); ui.preciseText(time, true); ui.fixedLineHeight(time, 1);
        copy = ui.icon("copy", 13, ui.t("Copy message"), () -> {});
        copyPadding();
        copy.setBackground(ui.states(0, 6, 0, ui.soft, 0));
        reset = () -> { copyState = "idle"; feedback("copy", "Copy message", false); };
        copy.setOnClickListener(view -> {
            String next;
            try { writer.write(this.text); next = "copied"; }
            catch (RuntimeException error) { next = "failed"; }
            boolean changed = !next.equals(copyState);
            copyState = next;
            feedback(next.equals("copied") ? "check" : "circle-alert", next.equals("copied") ? "Message copied" : "Could not copy message", changed);
            if (changed) { copy.removeCallbacks(reset); copy.postDelayed(reset, 2000); }
        });
        copy.setEnabled(!text.isEmpty());
        if (mine && !author.getText().toString().isEmpty()) add(author, -2);
        if (mine && formatted != null) add(time, -2);
        add(copy, ui.dp(28));
        if (!mine && formatted != null) add(time, -2);
        setMinimumHeight(ui.dp(28));
    }

    void bindText(String value) {
        text = value;
        if (copy.isEnabled() != !value.isEmpty()) copy.setEnabled(!value.isEmpty());
    }

    void resetFeedback() {
        if (copyState.equals("idle")) return;
        copy.removeCallbacks(reset); reset.run();
    }

    void bindAuthor(String name) {
        if (!mine || author.getText().toString().equals(name)) return;
        author.setText(name);
        if (name.isEmpty()) removeView(author);
        else if (author.getParent() == null) addView(author, 0, new LayoutParams(-2, -2));
        metadataMargins();
    }

    void bindTimestamp(long value) {
        if (timestamp == value) return;
        timestamp = value;
        NativeMessageTime formatted = NativeMessageTime.format(value, ui.locale, System.currentTimeMillis(), TimeZone.getDefault());
        time.setText(formatted == null ? "" : formatted.label);
        time.setContentDescription(formatted == null ? "" : formatted.title);
        if (formatted == null) removeView(time);
        else if (time.getParent() == null) addView(time, mine ? indexOfChild(copy) : getChildCount(), new LayoutParams(-2, -2));
        metadataMargins();
    }

    private void metadataMargins() {
        for (int i = 0; i < getChildCount(); i++) {
            LayoutParams params = (LayoutParams) getChildAt(i).getLayoutParams();
            params.setMarginStart(i == 0 ? 0 : ui.dp(7));
            getChildAt(i).setLayoutParams(params);
        }
    }

    private void add(View child, int width) {
        LayoutParams params = new LayoutParams(width, child == copy ? ui.dp(28) : -2);
        if (getChildCount() > 0) params.setMarginStart(ui.dp(7));
        addView(child, params);
    }

    private void feedback(String icon, String key, boolean announce) {
        copy.setKind(icon); copyPadding(); copy.setContentDescription(ui.t(key));
        if (announce) copy.announceForAccessibility(ui.t(key));
    }

    private void copyPadding() {
        int gap = ui.dp(28) - ui.dp(13);
        copy.setPadding(gap / 2, gap / 2, gap - gap / 2, gap - gap / 2);
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int maximum = ui.dp(140);
        if (author.getParent() == this && MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED) {
            // CSS lets the ellipsized author shrink before the no-wrap timestamp and copy control.
            int fixed = getPaddingLeft() + getPaddingRight() + ui.dp(28 + 7 * (getChildCount() - 1));
            if (time.getParent() == this) {
                time.measure(MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED), heightSpec);
                fixed += time.getMeasuredWidth();
            }
            maximum = Math.min(maximum, Math.max(0, MeasureSpec.getSize(widthSpec) - fixed));
        }
        if (author.getMaxWidth() != maximum) author.setMaxWidth(maximum);
        super.onMeasure(widthSpec, heightSpec);
    }

    @Override protected void onDetachedFromWindow() {
        resetFeedback();
        super.onDetachedFromWindow();
    }
}
