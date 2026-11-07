package com.cloudgeek.spool.android;

import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import java.util.function.Consumer;

final class NativeServerDialog extends NativeDialog {
    final EditText name;
    final Button save;
    NativeServerDialog(NativeUi ui, String initial, boolean adding, Consumer<String> done) {
        super(ui, ui.t(adding ? "Add server" : "Rename server"), ui.t("Close"));
        body.addView(ui.text(ui.t("Server name"), 12, ui.text)); body.addView(ui.space(8));
        name = ui.field(ui.t("Server name"), false); name.setText(initial);
        name.setImportantForAutofill(android.view.View.IMPORTANT_FOR_AUTOFILL_NO);
        body.addView(name, new LinearLayout.LayoutParams(-1, -2)); body.addView(ui.space(16));
        actions.addView(ui.quietButton(ui.t("Cancel"), this::dismiss));
        save = ui.button(ui.t(adding ? "Add server" : "Save"), true, () -> {
            String value = name.getText().toString().trim();
            if (value.isEmpty() || value.length() > 80) { name.setError(ui.t("Enter a server name (1–80 characters).")); return; }
            dismiss(); done.accept(value);
        });
        actions.addView(save);
    }
}
