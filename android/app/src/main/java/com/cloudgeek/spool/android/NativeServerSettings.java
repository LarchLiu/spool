package com.cloudgeek.spool.android;

import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Consumer;

/** Only the currently selected server is edited here. Server tabs and creation live in the sidebar. */
final class NativeServerSettings extends LinearLayout {
    final TextView name;
    final Button rename;
    final NativeSwitch enabled;
    private boolean binding;
    NativeServerSettings(NativeUi ui, ServerProfiles.Server server, Consumer<Boolean> enable, Runnable edit) {
        super(ui.context); setOrientation(VERTICAL);
        LinearLayout identity = ui.row(); identity.setGravity(android.view.Gravity.CENTER_VERTICAL);
        name = ui.bold(server.name, 14); identity.addView(name, new LayoutParams(0, -2, 1));
        rename = ui.quietButton(ui.t("Rename server"), edit); identity.addView(rename); addView(identity);
        LinearLayout connection = ui.row();
        TextView label = ui.text(ui.t("Connect to this server"), 12, ui.text);
        connection.addView(label, new LayoutParams(0, -2, 1));
        enabled = new NativeSwitch(ui, ui.t("Connect to this server")); enabled.setChecked(server.enabled);
        enabled.setOnCheckedChangeListener((button, checked) -> { if (!binding) enable.accept(checked); });
        connection.addView(enabled); addView(connection, new LayoutParams(-1, -2));
    }
    void bind(ServerProfiles.Server server) {
        name.setText(server.name);
        binding = true; enabled.setChecked(server.enabled); binding = false;
    }
}
