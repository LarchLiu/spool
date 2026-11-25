package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.drawable.Drawable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.ReplacementSpan;
import android.widget.Button;
import android.widget.LinearLayout;
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
    /** Each source keeps its own trusted entries; ordinary profiles always remain visible. */
    NativeServerTabs(NativeUi ui, ServerProfiles profiles, Map<String, String> states, Consumer<String> choose, boolean includeDisabled, Runnable create, List<NativeFederation.Tab> trusted) {
        super(ui, ui.t("Servers")); this.ui = ui; this.profiles = profiles; this.trusted = trusted;
        setPadding(ui.dp(6), ui.dp(6), ui.dp(6), ui.dp(6));
        // Desktop's 3px track padding plus its 1px border stay outside the scroll viewport.
        track.setPadding(ui.dp(4), ui.dp(4), ui.dp(4), ui.dp(4));
        tabs.setPadding(0, 0, 0, 0);
        setBackgroundColor(ui.sidebar);
        for (ServerProfiles.Server server : profiles.entries) {
            if (server.trusted()) continue;
            if (!includeDisabled && !server.enabled) continue;
            addServer(server, choose);
            for (NativeFederation.Tab entry : trusted) {
                if (entry.source == server && (entry.server == null || includeDisabled || entry.server.enabled)) addTrusted(entry, choose);
            }
        }
        if (create != null) {
            add = addAction("plus", ui.t("Add server"), create);
        } else add = null;
        status(states);
    }

    @Override Button addTab(String key, String label, Runnable choose) {
        Button tab = super.addTab(key, label, choose);
        tab.setPadding(ui.dp(12), ui.dp(6), ui.dp(12), ui.dp(6));
        return tab;
    }

    private void addServer(ServerProfiles.Server server, Consumer<String> choose) {
        Button tab = addTab(server.id, server.displayName(), () -> choose.accept(server.id));
        SpannableString label = new SpannableString("\ufffc" + server.displayName());
        Dot dot = new Dot(ui.dp(8), ui.dp(8));
        label.setSpan(dot, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        tab.setText(label);
    }

    private void addTrusted(NativeFederation.Tab entry, Consumer<String> choose) {
        String name = entry.server == null ? entry.target.name : entry.server.displayName();
        Button tab = addTab(entry.key(), name, () -> choose.accept(entry.key()));
        SpannableString label = new SpannableString("\ufffc" + name);
        Link link = new Link(ui, ui.dp(14), ui.dp(6));
        label.setSpan(link, 0, 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        tab.setText(label);
        tab.setMinimumWidth(0); tab.setMaxWidth(ui.dp(160));
        LinearLayout.LayoutParams params = (LinearLayout.LayoutParams) tab.getLayoutParams();
        params.height -= ui.dp(6); params.weight = 0; tab.setLayoutParams(params);
    }

    void status(Map<String, String> states) { status(states, ""); }

    void status(Map<String, String> states, String selectedFederationTab) {
        String selected = button(profiles.selected) == null ? "" : profiles.selected;
        for (ServerProfiles.Server server : profiles.entries) {
            Button tab = button(server.id); if (tab == null) continue;
            String description = server.displayName() + ", " + connectionStatus(tab, server, states);
            describe(tab, description);
        }
        for (NativeFederation.Tab entry : trusted) {
            Button tab = button(entry.key()); if (tab == null) continue;
            boolean pending = "signing-in".equals(states.get(entry.key()));
            tab.setEnabled(!pending);
            ServerProfiles.Server server = entry.server;
            String description = entry.source.displayName() + " → " + (server == null ? entry.target.name
                    : server.displayName() + ", " + connectionStatus(tab, server, states));
            describe(tab, description + ", " + ui.t(pending ? "Signing in…" : "Trusted server"));
            if ((selected.isEmpty() || entry.key().equals(selectedFederationTab)) && server != null && server.id.equals(profiles.selected))
                selected = entry.key();
        }
        select(selected);
    }

    private String connectionStatus(Button tab, ServerProfiles.Server server, Map<String, String> states) {
        String state = server.enabled ? states.getOrDefault(server.id, "disconnected") : "disconnected";
        int color = statusColor(ui, state);
        for (Dot dot : ((Spanned) tab.getText()).getSpans(0, 1, Dot.class)) {
            if (dot.color != color) { dot.color = color; tab.invalidate(); }
        }
        for (Link link : ((Spanned) tab.getText()).getSpans(0, 1, Link.class)) {
            if (link.color != color) { link.color = color; tab.invalidate(); }
        }
        return ui.t(statusLabel(state));
    }

    private static void describe(Button tab, String description) {
        tab.setContentDescription(description); tab.setTooltipText(description);
    }

    @Override int tabHeight() { return 32; }

    @Override int tabRadius(String key) { return isTrustedTab(key) ? 999 : super.tabRadius(key); }

    @Override Drawable buttonBackground(String key, boolean selected) {
        return isTrustedTab(key) ? ui.states(0, 999, ui.borderStrong, ui.elevated, ui.controlActive, ui.elevated)
                : super.buttonBackground(key, selected);
    }

    private static boolean isTrustedTab(String key) { return key != null && key.startsWith(NativeFederation.PREFIX); }

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

    /** A repeat icon marks a login owned by a particular source server. */
    static final class Link extends ReplacementSpan {
        static final int COLOR = 0xff3987e0;
        final int size, gap;
        final Drawable icon;
        int color = COLOR;
        Link(NativeUi ui, int size, int gap) {
            this.size = size; this.gap = gap;
            icon = NativeIcons.drawable(ui.context, "repeat", color, size);
        }
        @Override public int getSize(Paint paint, CharSequence text, int start, int end, Paint.FontMetricsInt metrics) { return size + gap; }
        @Override public void draw(Canvas canvas, CharSequence text, int start, int end, float x, int top, int baseline, int bottom, Paint paint) {
            Paint.FontMetrics metrics = paint.getFontMetrics();
            float center = baseline + (metrics.ascent + metrics.descent) / 2f;
            int saved = canvas.save();
            canvas.translate(x, center - size / 2f);
            icon.setTint(color); icon.draw(canvas);
            canvas.restoreToCount(saved);
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
