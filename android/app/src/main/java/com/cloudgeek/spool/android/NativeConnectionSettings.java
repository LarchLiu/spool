package com.cloudgeek.spool.android;

import android.text.InputType;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Consumer;

/** Android-only settings extension using Web's notification/settings section and form controls. */
final class NativeConnectionSettings extends LinearLayout {
    final EditText server;
    final TextView diagnostics, note;
    final Button action, login;
    private final NativeUi ui;
    private boolean active;

    NativeConnectionSettings(NativeUi ui, String origin, String explanation,
            Consumer<String> connect, Runnable disconnect, Runnable signIn) {
        super(ui.context);
        this.ui = ui;
        setOrientation(VERTICAL);
        addView(ui.divider());
        addView(ui.space(18));
        TextView title = ui.text("Tailscale (tsnet)", 12, ui.text);
        ui.typography(title, 12, ui.text, 500);
        addView(title);
        addView(ui.space(10));
        TextView description = ui.text(ui.label("Your private server, through this app's embedded node. No system VPN or shared auth key.",
                "通过应用内嵌节点连接私有服务器，不启动系统 VPN，也不使用共享入网密钥。"), 12, ui.muted);
        ui.fixedLineHeight(description, 1.6f);
        addView(description);
        addView(ui.space(16));
        addView(ui.text(ui.label("Spool server HTTPS address", "Spool 服务器 HTTPS 地址"), 12, ui.text));
        addView(ui.space(8));
        server = ui.field("https://spool.example.ts.net", false);
        ui.typography(server, 12, ui.text, 400);
        server.setPadding(ui.dp(12), ui.dp(10), ui.dp(12), ui.dp(10));
        server.setBackground(ui.states(ui.input, 9, ui.line, ui.input, ui.input));
        server.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        server.setText(origin);
        server.setContentDescription(ui.label("Spool server HTTPS address", "Spool 服务器 HTTPS 地址"));
        server.setImportantForAutofill(IMPORTANT_FOR_AUTOFILL_NO);
        addView(server, new LayoutParams(-1, -2));
        addView(ui.space(12));
        action = ui.button("", true, () -> {
            if (active) disconnect.run();
            else connect.accept(server.getText().toString());
        });
        addView(action, new LayoutParams(-1, -2));
        addView(ui.space(8));
        login = ui.button(ui.label("Sign in to Tailscale", "登录 Tailscale"), false, () -> { if (loginAllowed()) signIn.run(); });
        login.setBackground(ui.states(ui.input, 9, ui.line, ui.input, ui.input));
        login.setEnabled(false);
        addView(login, new LayoutParams(-1, -2));
        addView(ui.space(14));
        diagnostics = ui.text("", 12, ui.text);
        ui.fixedLineHeight(diagnostics, 1.6f);
        diagnostics.setTextIsSelectable(true);
        addView(diagnostics);
        addView(ui.space(10));
        note = ui.text(explanation, 11, ui.muted);
        ui.fixedLineHeight(note, 1.6f);
        addView(note);
    }

    private boolean loginAllowed() { return login != null && login.isEnabled(); }

    void bind(boolean connected, boolean connecting, boolean canSignIn, String summary) {
        active = connected || connecting;
        server.setEnabled(!active);
        action.setText(active ? ui.label("Disconnect", "断开连接") : ui.label("Connect", "连接"));
        login.setEnabled(canSignIn);
        diagnostics.setText(summary);
    }
}
