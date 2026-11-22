package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.ReplacementSpan;
import android.widget.Button;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/** A shared rounded track with inset server tabs and live connection indicators. */
final class NativeServerTabs extends NativeTabs {
    final NativeUi.IconButton add;
    private final NativeUi ui;
    private final ServerProfiles profiles;
    private final List<NativeFederation.Tab> trusted;

    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose) {
        this(ui, profiles, states, choose, false, null);
    }
    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose, boolean includeDisabled, Runnable create) {
        this(ui, profiles, states, choose, includeDisabled, create, List.of());
    }
    /** \u4e92\u4fe1\u6807\u7b7e\u7d27\u8ddf\u5f53\u524d\u6765\u6e90\u670d\u52a1\u5668\uff1b\u5339\u914d\u5df2\u6709\u8fde\u63a5\u65f6\u4e0d\u91cd\u590d\u6dfb\u52a0\u3002 */
    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose, boolean includeDisabled, Runnable create, List<NativeFederation.Tab> trusted) {
        super(ui, ui.t("Servers")); this.ui = ui; this.profiles = profiles; this.trusted = trusted;
        setPadding(ui.dp(18), ui.dp(8), ui.dp(18), ui.dp(8));
        setBackgroundColor(ui.sidebar);
        boolean groupTrusted = !trusted.isEmpty() && profiles.active() != null && (includeDisabled || profiles.active().enabled);
        for (ServerProfiles.Server server : profiles.entries) {
            if ((!includeDisabled && !server.enabled) || (groupTrusted && NativeFederation.find(trusted, server.id) != null)) continue;
            addServer(server, choose);
            if (groupTrusted && server.id.equals(profiles.selected)) {
                for (NativeFederation.Tab entry : trusted) {
                    if (entry.server == null) addTrusted(entry, choose);
                    else if (includeDisabled || entry.server.enabled) addServer(entry.server, choose);
                }
            }
        }
        if (create != null) {
            add = addAction("plus", ui.t("Add server"), create);
        } else add = null;
        status(states);
    }

    private void addServer(ServerProfiles.Server server, Consumer<String> choose) {
        Button tab = addTab(server.id, server.name, () -> choose.accept(server.id));
        boolean linked = trusted(server);
        SpannableString label = new SpannableString("\ufffc" + server.name + (linked ? "\ufffc" : ""));
        Dot dot = new Dot(ui.dp(8), ui.dp(8));
        label.setSpan(dot, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        if (linked) label.setSpan(new Link(ui.dp(14), ui.dp(6), false), label.length() - 1, label.length(), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        tab.setText(label);
        // \u6709\u672b\u5c3e\u4e92\u4fe1\u6807\u8bb0\u65f6\uff0c\u4e0d\u518d\u989d\u5916\u8865\u9f50\u524d\u7f6e\u72b6\u6001\u70b9\u7684\u5bbd\u5ea6\u3002
        if (!linked) tab.setPaddingRelative(tab.getPaddingStart(), tab.getPaddingTop(),
                tab.getPaddingEnd() + dot.size + dot.gap, tab.getPaddingBottom());
    }

    private void addTrusted(NativeFederation.Tab entry, Consumer<String> choose) {
        Button tab = addTab(entry.key(), entry.target.name, () -> choose.accept(entry.key()));
        SpannableString label = new SpannableString("\ufffc" + entry.target.name);
        Link link = new Link(ui.dp(14), ui.dp(6), true);
        label.setSpan(link, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        tab.setText(label);
        tab.setPaddingRelative(tab.getPaddingStart(), tab.getPaddingTop(), tab.getPaddingEnd() + link.size + link.gap, tab.getPaddingBottom());
        String description = entry.target.name + ", " + ui.t("Trusted server");
        tab.setContentDescription(description); tab.setTooltipText(description);
    }

    void status(Map<String, String> states) {
        select(profiles.selected);
        for (ServerProfiles.Server server : profiles.entries) {
            Button tab = button(server.id); if (tab == null) continue;
            String state = server.enabled ? states.getOrDefault(server.id, "disconnected") : "disconnected";
            int color = statusColor(ui, state);
            Dot dot = ((Spanned) tab.getText()).getSpans(0, 1, Dot.class)[0];
            if (dot.color != color) { dot.color = color; tab.invalidate(); }
            String description = server.name + ", " + ui.t(statusLabel(state)) + (trusted(server) ? ", " + ui.t("Trusted server") : "");
            tab.setContentDescription(description);
            tab.setTooltipText(description);
        }
    }

    /** Offered by the selected server, or already signed in with an account that came from another server. */
    private boolean trusted(ServerProfiles.Server server) {
        if (!server.account.optString("source_server_id").isEmpty()) return true;
        for (NativeFederation.Tab entry : trusted) if (entry.server == server) return true;
        return false;
    }

    static String statusLabel(String state) {
        return state.equals("connected") ? "Connected · Signed in" : state.equals("connecting") ? "Connecting…"
                : state.equals("login") ? "Connected · Sign-in required" : state.equals("error") ? "Connection failed" : "Disconnected";
    }

    static CharSequence statusText(NativeUi ui, String label, String state, int size, int gap) {
        SpannableString text = new SpannableString("\ufffc" + label);
        Dot dot = new Dot(ui.dp(size), ui.dp(gap)); dot.color = statusColor(ui, state);
        text.setSpan(dot, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        return text;
    }

    static int statusColor(NativeUi ui, String state) {
        return state.equals("connected") ? ui.dark ? 0xff4ade80 : 0xff15803d
                : state.equals("login") ? 0xff3987e0
                : state.equals("connecting") ? ui.dark ? 0xfffbbf24 : 0xffb45309
                : state.equals("error") ? ui.error : ui.muted;
    }

    /** Two joined rings marking a server reached through the selected server's trust rather than its own sign-in. */
    static final class Link extends ReplacementSpan {
        static final int COLOR = 0xff3987e0;
        final int size, gap;
        final boolean leading;
        Link(int size, int gap, boolean leading) { this.size = size; this.gap = gap; this.leading = leading; }
        @Override public int getSize(Paint paint, CharSequence text, int start, int end, Paint.FontMetricsInt metrics) { return size + gap; }
        @Override public void draw(Canvas canvas, CharSequence text, int start, int end, float x, int top, int baseline, int bottom, Paint paint) {
            int previous = paint.getColor(); Paint.Style style = paint.getStyle(); float width = paint.getStrokeWidth();
            Paint.FontMetrics metrics = paint.getFontMetrics();
            float stroke = Math.max(1f, size / 9f), radius = size * .3f - stroke / 2, left = leading ? x : x + gap;
            float center = baseline + (metrics.ascent + metrics.descent) / 2f;
            paint.setColor(COLOR); paint.setStyle(Paint.Style.STROKE); paint.setStrokeWidth(stroke);
            canvas.drawCircle(left + radius + stroke / 2, center, radius, paint);
            canvas.drawCircle(left + size - radius - stroke / 2, center, radius, paint);
            paint.setColor(previous); paint.setStyle(style); paint.setStrokeWidth(width);
        }
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
