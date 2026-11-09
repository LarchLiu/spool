package com.cloudgeek.spool.android;

import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.widget.TextView;
import java.util.concurrent.Executor;
import java.util.function.Consumer;
import spoolnet.PairingSession;
import spoolnet.Spoolnet;

/** Device approval uses its own cancelable transport; account clients stay idle. */
final class NativeDevicePairingDialog extends NativeDialog {
    interface Transport {
        String fingerprint();
        String poll() throws Exception;
        String code() throws Exception;
        void close();
    }
    interface Factory { Transport open(String directory, String code, String name) throws Exception; }
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Executor worker;
    private final Consumer<String> approved;
    private final RelayPairing pairing;
    private final String directory;
    private final Factory factory;
    private final TextView fingerprint;
    private Transport session;
    private boolean closed;
    private final Runnable poll = this::poll;

    NativeDevicePairingDialog(NativeUi ui, String directory, RelayPairing pairing, Executor worker, Consumer<String> approved) {
        this(ui, directory, pairing, worker, approved, (path, code, name) -> {
            PairingSession bound = Spoolnet.beginRelayPairing(path, code, name);
            return new Transport() {
                public String fingerprint() { return bound.fingerprint(); }
                public String poll() throws Exception { return bound.poll(); }
                public String code() throws Exception { return bound.code(); }
                public void close() { bound.close(); }
            };
        });
    }

    NativeDevicePairingDialog(NativeUi ui, String directory, RelayPairing pairing, Executor worker, Consumer<String> approved, Factory factory) {
        super(ui, ui.t("Device approval"), ui.t("Close"));
        this.directory = directory; this.pairing = pairing; this.worker = worker; this.approved = approved;
        this.factory = factory;
        body.addView(ui.text(ui.t("Approve this phone in Spool Desktop. Compare the device fingerprint on both screens."), 13, ui.text));
        body.addView(ui.space(16));
        body.addView(ui.bold(Build.MANUFACTURER + " " + Build.MODEL, 14));
        fingerprint = ui.text(ui.t("Preparing device identity…"), 13, ui.text);
        fingerprint.setTextIsSelectable(true);
        fingerprint.setTypeface(android.graphics.Typeface.MONOSPACE);
        body.addView(fingerprint);
        body.addView(ui.space(16));
        actions.addView(ui.quietButton(ui.t("Cancel"), this::dismiss));
        setOnDismissListener(dialog -> {
            closed = true; main.removeCallbacks(poll);
            Transport active = session;
            if (active != null) worker.execute(active::close);
        });
    }

    @Override public void show() {
        super.show();
        worker.execute(() -> {
            try {
                Transport started = factory.open(directory, pairing.code, Build.MANUFACTURER + " " + Build.MODEL);
                main.post(() -> {
                    if (closed) { worker.execute(started::close); return; }
                    session = started;
                    fingerprint.setText(started.fingerprint().replaceAll("(.{8})(?!$)", "$1 "));
                    poll();
                });
            } catch (Exception failure) { main.post(() -> failed(failure)); }
        });
    }

    private void poll() {
        if (closed || session == null) return;
        if (System.currentTimeMillis() / 1000 >= pairing.expiresAt) {
            failed(new IllegalStateException(ui.t("Pairing code expired. Generate a new code."))); return;
        }
        Transport active = session;
        worker.execute(() -> {
            try {
                String state = active.poll();
                String code = state.equals("approved") ? active.code() : "";
                main.post(() -> {
                    if (closed) return;
                    if (!code.isEmpty()) { dismiss(); approved.accept(code); }
                    else main.postDelayed(poll, 1200);
                });
            } catch (Exception failure) { main.post(() -> failed(failure)); }
        });
    }

    private void failed(Exception failure) {
        if (closed) return;
        error.setText(ui.t("Pairing failed. Check the connection or scan a new code."));
        error.setVisibility(View.VISIBLE);
        Transport active = session; session = null;
        if (active != null) worker.execute(active::close);
    }
}
