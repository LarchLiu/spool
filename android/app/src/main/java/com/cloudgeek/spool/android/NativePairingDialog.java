package com.cloudgeek.spool.android;

import android.widget.TextView;

final class NativePairingDialog extends NativeDialog {
    NativePairingDialog(NativeUi ui, RelayPairing pairing, Runnable confirmed) {
        super(ui, ui.t("Confirm server identity"), ui.t("Close"));
        body.addView(ui.text(ui.t("Compare this identity with Spool Desktop before connecting."), 13, ui.text));
        body.addView(ui.space(16));
        body.addView(ui.bold(ui.t("Relay HTTPS address"), 12));
        TextView address = ui.text(pairing.relay, 13, ui.text); address.setTextIsSelectable(true); body.addView(address);
        body.addView(ui.space(16));
        body.addView(ui.bold(ui.t("Server identity (SHA-256)"), 12));
        TextView identity = ui.text(pairing.fingerprint, 13, ui.text); identity.setTextIsSelectable(true);
        identity.setTypeface(android.graphics.Typeface.MONOSPACE); body.addView(identity);
        body.addView(ui.space(16));
        body.addView(ui.text(ui.t("After pairing, sign in with your Spool account."), 12, ui.muted));
        body.addView(ui.space(16));
        actions.addView(ui.quietButton(ui.t("Cancel"), this::dismiss));
        actions.addView(ui.button(ui.t("Confirm and connect"), true, () -> { dismiss(); confirmed.run(); }));
    }
}
