package com.cloudgeek.spool.android;

import android.text.InputFilter;
import android.view.KeyEvent;
import android.view.inputmethod.EditorInfo;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Consumer;

/** Shared inline identity fields for new and saved server connections. */
final class NativeServerSettings extends LinearLayout {
    final EditText name;
    final NativeSwitch enabled;
    private final Consumer<String> rename;
    private String savedName;
    private boolean binding;
    NativeServerSettings(NativeUi ui, ServerProfiles.Server server, Consumer<Boolean> enable, Consumer<String> rename) {
        super(ui.context); setOrientation(VERTICAL);
        this.rename = rename;
        LinearLayout identity = ui.row(); identity.setGravity(android.view.Gravity.CENTER_VERTICAL);
        TextView caption = ui.text(ui.t("Display name (optional)"), 12, ui.text);
        ui.typography(caption, 12, ui.text, 500);
        LayoutParams captionParams = new LayoutParams(0, -2, 1); captionParams.setMarginEnd(ui.dp(16));
        identity.addView(caption, captionParams);
        name = ui.field(server.serverName, false);
        ui.typography(name, 12, ui.text, 400);
        name.setPadding(ui.dp(10), ui.dp(6), ui.dp(10), ui.dp(6));
        name.setId(android.view.View.generateViewId()); caption.setLabelFor(name.getId());
        name.setContentDescription(ui.t("Display name (optional)"));
        name.setImportantForAutofill(IMPORTANT_FOR_AUTOFILL_NO);
        name.setFilters(new InputFilter[] {new InputFilter.LengthFilter(80)});
        name.setImeOptions(EditorInfo.IME_ACTION_DONE);
        identity.addView(name, new LayoutParams(0, ui.dp(32), 1));
        addView(identity); addView(ui.space(8));
        TextView help = ui.text(ui.t("Leave blank to use the name from the server's settings."), 11, ui.muted);
        ui.fixedLineHeight(help, 1.5f); addView(help); addView(ui.space(10));
        LinearLayout connection = ui.row();
        TextView label = ui.text(ui.t("Connect to this server"), 12, ui.text);
        connection.addView(label, new LayoutParams(0, -2, 1));
        enabled = new NativeSwitch(ui, ui.t("Connect to this server")); enabled.setChecked(server.enabled);
        enabled.setOnCheckedChangeListener((button, checked) -> { if (!binding) enable.accept(checked); });
        connection.addView(enabled); addView(connection, new LayoutParams(-1, -2));
        bind(server);
        name.setOnFocusChangeListener((view, focused) -> { if (!focused) commitName(); });
        name.setOnEditorActionListener((view, action, event) -> {
            if (action != EditorInfo.IME_ACTION_DONE && (event == null || event.getKeyCode() != KeyEvent.KEYCODE_ENTER)) return false;
            if (event == null || event.getAction() == KeyEvent.ACTION_UP) commitName();
            return true;
        });
    }
    void bind(ServerProfiles.Server server) {
        savedName = server.name;
        if (!name.hasFocus() && !name.getText().toString().equals(server.name)) name.setText(server.name);
        name.setHint(ServerProfiles.serverName(server.serverName));
        binding = true; enabled.setChecked(server.enabled); binding = false;
    }
    void commitName() {
        String value = name.getText().toString().trim();
        if (value.equals(savedName)) return;
        savedName = value;
        rename.accept(value);
    }
}
