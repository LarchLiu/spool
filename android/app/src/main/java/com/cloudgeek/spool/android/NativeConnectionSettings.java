package com.cloudgeek.spool.android;

import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.EnumMap;
import java.util.Map;
import java.util.function.BiConsumer;
import java.util.function.Consumer;

/** Browsing a connection tab preserves the active route until Connect is pressed. */
final class NativeConnectionSettings extends LinearLayout {
    final Map<ConnectionMode, Row> rows = new EnumMap<>(ConnectionMode.class);
    final TextView diagnostics, note;
    final NativeUi.IconButton login, scan;
    final NativeTabs tabs;
    final LinearLayout entries;
    final NativeSwitch automatic;
    final Map<ConnectionMode, String> details = new EnumMap<>(ConnectionMode.class);
    private final NativeUi ui;
    private final Consumer<ConnectionPlan> changed;
    private ConnectionMode selected;
    private ConnectionMode visible;
    private boolean selecting;
    private ConnectionMode current;
    private boolean active;

    NativeConnectionSettings(NativeUi ui, ConnectionPlan plan, Consumer<ConnectionPlan> changed,
            BiConsumer<ConnectionPlan, ConnectionMode> connect, Runnable stop, Runnable signIn, Runnable scanPairing) {
        super(ui.context);
        this.ui = ui; this.selected = plan.selected; this.changed = changed;
        setOrientation(VERTICAL);
        addView(ui.divider()); addView(ui.space(18));
        TextView title = ui.text(ui.t("Server connection"), 12, ui.text);
        ui.typography(title, 12, ui.text, 500);
        addView(title); addView(ui.space(10));
        automatic = new NativeSwitch(ui, ui.t("Allow automatic connection switching"));
        automatic.setChecked(plan.autoSwitch);
        automatic.setOnCheckedChangeListener((button, checked) -> changed.accept(plan()));
        LinearLayout policy = ui.row(); policy.setGravity(Gravity.CENTER_VERTICAL);
        TextView caption = ui.text(ui.t("Allow automatic connection switching"), 12, ui.text);
        policy.addView(caption, new LayoutParams(0, -2, 1)); policy.addView(automatic);
        addView(policy, new LayoutParams(-1, -2)); addView(ui.space(12));
        login = ui.icon("key-round", 17, ui.t("Sign in to Tailscale"), () -> { if (loginAllowed()) signIn.run(); });
        login.setEnabled(false);
        scan = ui.icon("scan-qr-code", 18, ui.t("Scan pairing QR code"), () -> { if (scanAllowed()) scanPairing.run(); });
        tabs = new NativeTabs(ui, ui.t("Server connection"), NativeTabs.Size.COMPACT);
        addView(tabs, new LayoutParams(-1, -2)); addView(ui.space(10));
        entries = ui.column(); addView(entries, new LayoutParams(-1, -2));
        for (ConnectionMode mode : ConnectionMode.values()) rows.put(mode, new Row(mode, plan.origin(mode), connect, stop));
        for (ConnectionMode mode : ConnectionMode.values()) {
            tabs.addTab(mode.value, ui.t(title(mode)), () -> show(mode));
            tabStatus(mode, "disconnected");
            entries.addView(rows.get(mode), new LayoutParams(-1, -2));
        }
        show(plan.selected);
        for (Row row : rows.values()) row.server.addTextChangedListener(new TextWatcher() {
            public void beforeTextChanged(CharSequence text, int start, int count, int after) {}
            public void onTextChanged(CharSequence text, int start, int before, int count) { changed.accept(plan()); }
            public void afterTextChanged(Editable text) {}
        });
        diagnostics = ui.text("", 12, ui.text); ui.fixedLineHeight(diagnostics, 1.6f);
        diagnostics.setTextIsSelectable(true);
        note = ui.text("", 11, ui.muted);
    }

    private boolean loginAllowed() { return login != null && login.isEnabled(); }
    private boolean scanAllowed() { return scan != null && scan.isEnabled(); }
    private static String title(ConnectionMode mode) {
        return mode == ConnectionMode.DIRECT ? "Public HTTPS" : mode == ConnectionMode.TAILSCALE ? "Tailscale" : "LAN / Relay";
    }

    private void tabStatus(ConnectionMode mode, String state) {
        Button tab = tabs.button(mode.value);
        String label = ui.t(title(mode));
        tab.setText(NativeServerTabs.statusText(ui, label, state, 6, 4));
        tab.setContentDescription(label + ", " + ui.t(NativeServerTabs.statusLabel(state)));
    }

    void show(ConnectionMode mode) {
        if (visible == mode) return;
        ui.dismissMenus();
        if (visible != null) rows.get(visible).server.clearFocus();
        visible = mode;
        tabs.select(mode.value);
        for (Row row : rows.values()) row.setVisibility(row.mode == mode ? VISIBLE : GONE);
    }
    ConnectionPlan plan() { return new ConnectionPlan(input(ConnectionMode.DIRECT).getText().toString(), input(ConnectionMode.TAILSCALE).getText().toString(), input(ConnectionMode.RELAY).getText().toString(), selected, automatic.isChecked()); }
    EditText input(ConnectionMode mode) { return rows.get(mode).server; }

    void bind(ConnectionMode current, boolean connected, boolean active, boolean selecting, boolean canSignIn,
            Map<ConnectionMode, String> states, String summary) {
        bind(current, connected, active, selecting, canSignIn, states, summary, connected ? "connected" : active ? "connecting" : "disconnected");
    }

    void bind(ConnectionMode current, boolean connected, boolean active, boolean selecting, boolean canSignIn,
            Map<ConnectionMode, String> states, String summary, String connectionState) {
        this.selecting = selecting;
        this.current = current; this.active = active;
        automatic.setEnabled(!selecting);
        login.setEnabled(canSignIn && !selecting);
        scan.setEnabled(!selecting);
        diagnostics.setText(summary);
        details.put(current, summary + (current == ConnectionMode.TAILSCALE || note.getText().length() == 0 ? "" : "\n\n" + note.getText()));
        for (Row row : rows.values()) {
            row.server.setEnabled(!selecting);
            row.status.setText(states.getOrDefault(row.mode, ""));
            tabStatus(row.mode, row.mode == current ? connectionState : "disconnected");
            boolean selected = active && row.mode == current;
            String label = ui.t(selected ? "Disconnect" : "Connect");
            row.use.setText(label);
            row.use.setContentDescription(label);
            row.use.setEnabled(selected || !selecting && !row.server.getText().toString().trim().isEmpty());
            if (row.information != null) row.information.refresh();
        }
    }

    final class Row extends LinearLayout {
        final ConnectionMode mode;
        final EditText server;
        final TextView status;
        final Button use;
        final NativeInfo information;
        final FrameLayout actions;
        final LinearLayout tools;
        Row(ConnectionMode mode, String origin, BiConsumer<ConnectionPlan, ConnectionMode> connect, Runnable stop) {
            super(ui.context); this.mode = mode;
            setOrientation(VERTICAL);
            information = mode == ConnectionMode.DIRECT ? null : new NativeInfo(ui, ui.t(title(mode)),
                    () -> details.getOrDefault(mode, mode == ConnectionMode.RELAY
                            ? ui.t("Prefer the paired server on your LAN; use its relay when away. Both paths verify the same server identity.") : statusText()));
            server = ui.field(mode == ConnectionMode.DIRECT ? "https://spool.example.com" : mode == ConnectionMode.TAILSCALE ? "https://spool.example.ts.net" : "spool-pair:…", false);
            server.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
            server.setText(origin); server.setContentDescription(ui.t(mode == ConnectionMode.DIRECT ? "Public HTTPS address" : mode == ConnectionMode.TAILSCALE ? "Tailscale HTTPS address" : "Pairing code"));
            server.setImportantForAutofill(IMPORTANT_FOR_AUTOFILL_NO);
            addView(server, new LayoutParams(-1, -2)); addView(ui.space(8));
            status = ui.text("", 11, ui.muted);
            use = ui.button(ui.t("Connect"), false, () -> {
                if (active && current == mode) { stop.run(); return; }
                if (selecting) return;
                selected = mode;
                changed.accept(plan());
                connect.accept(plan(), mode);
            });
            use.setBackground(ui.states(ui.dark ? ui.controlHover : ui.soft, 6, ui.dark ? ui.subtle : 0,
                    ui.controlActive, ui.soft, ui.dark ? ui.controlActive : ui.controlHover));
            use.setMaxLines(1); use.setEllipsize(android.text.TextUtils.TruncateAt.END);
            tools = ui.row();
            if (information != null) tools.addView(information.button);
            if (mode == ConnectionMode.TAILSCALE) tools.addView(login);
            if (mode == ConnectionMode.RELAY) tools.addView(scan);
            actions = new FrameLayout(ui.context) {
                @Override protected void onMeasure(int widthSpec, int heightSpec) {
                    int width = MeasureSpec.getSize(widthSpec);
                    tools.measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.AT_MOST), heightSpec);
                    int available = Math.max(0, width - 2 * (tools.getMeasuredWidth() + ui.dp(4)));
                    use.setMinWidth(Math.min(ui.dp(112), available));
                    use.setMaxWidth(available);
                    super.onMeasure(widthSpec, heightSpec);
                }
            };
            actions.addView(use, new FrameLayout.LayoutParams(-2, -2, Gravity.CENTER));
            actions.addView(tools, new FrameLayout.LayoutParams(-2, -2, Gravity.END | Gravity.CENTER_VERTICAL));
            addView(actions, new LayoutParams(-1, -2));
        }
        private String statusText() { return status == null ? "" : status.getText().toString(); }
    }
}
