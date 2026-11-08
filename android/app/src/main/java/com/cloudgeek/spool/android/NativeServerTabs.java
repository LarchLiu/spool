package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.ReplacementSpan;
import android.widget.Button;
import java.util.Map;
import java.util.function.Consumer;

/** A shared rounded track with inset server tabs and live connection indicators. */
final class NativeServerTabs extends NativeTabs {
    final NativeUi.IconButton add;
    private final NativeUi ui;
    private final ServerProfiles profiles;

    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose) {
        this(ui, profiles, states, choose, false, null);
    }
    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose, boolean includeDisabled, Runnable create) {
        super(ui, ui.t("Servers")); this.ui = ui; this.profiles = profiles;
        setPadding(ui.dp(18), ui.dp(8), ui.dp(18), ui.dp(8));
        setBackgroundColor(ui.sidebar);
        for (ServerProfiles.Server server : profiles.entries) if (includeDisabled || server.enabled) {
            Button tab = addTab(server.id, server.name, () -> choose.accept(server.id));
            SpannableString label = new SpannableString("\ufffc" + server.name);
            Dot dot = new Dot(ui.dp(8), ui.dp(8));
            label.setSpan(dot, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            tab.setText(label);
            // Balance the leading dot so the server name itself stays centered.
            tab.setPaddingRelative(tab.getPaddingStart(), tab.getPaddingTop(),
                    tab.getPaddingEnd() + dot.size + dot.gap, tab.getPaddingBottom());
        }
        if (create != null) {
            add = addAction("plus", ui.t("Add server"), create);
        } else add = null;
        status(states);
    }

    void status(Map<String, String> states) {
        select(profiles.selected);
        for (ServerProfiles.Server server : profiles.entries) {
            Button tab = button(server.id); if (tab == null) continue;
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

    /** An inline status dot with a fixed gap before the label. */
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
