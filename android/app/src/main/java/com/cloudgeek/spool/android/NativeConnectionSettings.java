package com.cloudgeek.spool.android;

import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.EnumMap;
import java.util.Map;
import java.util.function.BiConsumer;
import java.util.function.Consumer;

/** Independent network entries with one explicitly selected, persisted connection. */
final class NativeConnectionSettings extends LinearLayout {
    final Map<ConnectionMode, Row> rows = new EnumMap<>(ConnectionMode.class);
    final TextView diagnostics, note;
    final NativeUi.IconButton login;
    final LinearLayout entries;
    final NativeSwitch automatic;
    final NativeInfo information, automaticInformation;
    final Map<ConnectionMode, String> details = new EnumMap<>(ConnectionMode.class);
    private final NativeUi ui;
    private final Consumer<ConnectionPlan> changed;
    private ConnectionMode selected;
    private boolean selecting;
    private ConnectionMode current;
    private boolean active;

    NativeConnectionSettings(NativeUi ui, ConnectionPlan plan, Consumer<ConnectionPlan> changed,
            BiConsumer<ConnectionPlan, ConnectionMode> connect, Runnable stop, Runnable signIn) {
        super(ui.context);
        this.ui = ui; this.selected = plan.selected; this.changed = changed;
        setOrientation(VERTICAL);
        addView(ui.divider()); addView(ui.space(18));
        TextView title = ui.text(ui.t("Server connection"), 12, ui.text);
        ui.typography(title, 12, ui.text, 500);
        information = new NativeInfo(ui, ui.t("Server connection"), () -> ui.t("Choose a connection below. This server remembers your choice."));
        LinearLayout heading = ui.row(); heading.setGravity(Gravity.CENTER_VERTICAL);
        heading.addView(title); heading.addView(information.button); addView(heading); addView(ui.space(10));
        automatic = new NativeSwitch(ui, ui.t("Allow automatic connection switching"));
        automatic.setChecked(plan.autoSwitch);
        automatic.setOnCheckedChangeListener((button, checked) -> changed.accept(plan()));
        automaticInformation = new NativeInfo(ui, ui.t("Allow automatic connection switching"),
                () -> ui.t("Enable only when both addresses reach the same Spool service. When off, switching is manual."));
        LinearLayout policy = ui.row(); policy.setGravity(Gravity.CENTER_VERTICAL);
        LinearLayout label = ui.row();
        TextView caption = ui.text(ui.t("Allow automatic connection switching"), 12, ui.text);
        label.addView(caption, new LayoutParams(0, -2, 1)); label.addView(automaticInformation.button);
        policy.addView(label, new LayoutParams(0, -2, 1)); policy.addView(automatic);
        addView(policy, new LayoutParams(-1, -2)); addView(ui.space(12));
        login = ui.icon("key-round", 17, ui.t("Sign in to Tailscale"), () -> { if (loginAllowed()) signIn.run(); });
        login.setEnabled(false);
        entries = ui.column(); entries.setBackground(ui.shape(ui.input, 9, ui.line));
        addView(entries, new LayoutParams(-1, -2));
        for (ConnectionMode mode : ConnectionMode.values()) rows.put(mode, new Row(mode, plan.origin(mode), connect, stop));
        for (ConnectionMode mode : ConnectionMode.values()) {
            if (entries.getChildCount() > 0) {
                LayoutParams divider = new LayoutParams(-1, ui.dp(1));
                divider.setMargins(ui.dp(12), 0, ui.dp(12), 0);
                entries.addView(ui.divider(), divider);
            }
            entries.addView(rows.get(mode), new LayoutParams(-1, -2));
        }
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
    ConnectionPlan plan() { return new ConnectionPlan(input(ConnectionMode.DIRECT).getText().toString(), input(ConnectionMode.TAILSCALE).getText().toString(), selected, automatic.isChecked()); }
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
        diagnostics.setText(summary);
        details.put(current, summary + (note.getText().length() == 0 ? "" : "\n\n" + note.getText()));
        for (Row row : rows.values()) {
            row.server.setEnabled(!selecting);
            row.status.setText(states.getOrDefault(row.mode, ""));
            boolean selected = active && row.mode == current;
            String label = ui.t(selected ? "Disconnect" : "Connect");
            row.use.setText(selected ? NativeServerTabs.statusText(ui, label, connectionState) : label);
            row.use.setContentDescription(label);
            row.use.setEnabled(selected || !selecting && !row.server.getText().toString().trim().isEmpty());
            row.information.refresh();
        }
    }

    final class Row extends LinearLayout {
        final ConnectionMode mode;
        final EditText server;
        final TextView title, status;
        final Button use;
        final NativeInfo information;
        Row(ConnectionMode mode, String origin, BiConsumer<ConnectionPlan, ConnectionMode> connect, Runnable stop) {
            super(ui.context); this.mode = mode;
            setOrientation(VERTICAL); setPadding(ui.dp(12), ui.dp(10), ui.dp(12), ui.dp(12));
            LinearLayout heading = ui.row(); heading.setGravity(Gravity.CENTER_VERTICAL);
            title = ui.bold(ui.t(mode == ConnectionMode.DIRECT ? "Public HTTPS" : "Tailscale (tsnet)"), 12); heading.addView(title);
            information = new NativeInfo(ui, ui.t(mode == ConnectionMode.DIRECT ? "Public HTTPS" : "Tailscale (tsnet)"),
                    () -> details.getOrDefault(mode, ui.t(mode == ConnectionMode.DIRECT
                            ? "Connect using the same public HTTPS address as the PWA. Tailscale is not required."
                            : "Your private server, through this app's embedded node. No system VPN or shared auth key.") + "\n\n" + statusText()));
            heading.addView(information.button);
            heading.addView(new View(ui.context), new LayoutParams(0, 1, 1));
            if (mode == ConnectionMode.TAILSCALE) heading.addView(login);
            addView(heading); addView(ui.space(8));
            server = ui.field(mode == ConnectionMode.DIRECT ? "https://spool.example.com" : "https://spool.example.ts.net", false);
            server.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
            server.setText(origin); server.setContentDescription(ui.t(mode == ConnectionMode.DIRECT ? "Public HTTPS address" : "Tailscale HTTPS address"));
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
            addView(use, new LayoutParams(-1, -2));
        }
        private String statusText() { return status == null ? "" : status.getText().toString(); }
    }
}
