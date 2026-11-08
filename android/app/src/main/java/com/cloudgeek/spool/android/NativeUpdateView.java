package com.cloudgeek.spool.android;

import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.TextView;

/** Local APK controls, independent of server connection and account settings. */
final class NativeUpdateView extends LinearLayout {
    final TextView status;
    final Button action, cancel;
    final LinearLayout buttons;
    final ProgressBar progress;
    private final NativeUi ui;
    NativeUpdateView(NativeUi ui, Runnable check, Runnable download, Runnable install, Runnable cancelUpdate) {
        super(ui.context); this.ui = ui; setOrientation(VERTICAL);
        status = ui.text("", 11, ui.muted); status.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE); addView(status);
        progress = new ProgressBar(ui.context, null, android.R.attr.progressBarStyleHorizontal);
        progress.setMax(100); LayoutParams bar = new LayoutParams(-1, ui.dp(4)); bar.topMargin = ui.dp(8); addView(progress, bar);
        buttons = ui.row();
        action = ui.quietButton(ui.t("Check for updates"), check); buttons.addView(action);
        cancel = ui.quietButton(ui.t("Cancel"), cancelUpdate); buttons.addView(cancel);
        for (Button button : new Button[]{action, cancel}) {
            ui.typography(button, 11, ui.muted, 400);
            button.setMaxLines(1);
            button.setPadding(0, ui.dp(7), 0, ui.dp(7));
        }
        this.check = check; this.download = download; this.install = install;
    }
    private final Runnable check, download, install;
    void bind(NativeAppUpdate update) {
        boolean downloading = update.phase == NativeAppUpdate.Phase.DOWNLOADING;
        progress.setVisibility(downloading ? VISIBLE : GONE);
        progress.setProgress(downloading && update.release != null ? (int) (100 * update.received / update.release.size) : 0);
        cancel.setVisibility(update.busy() ? VISIBLE : GONE);
        action.setVisibility(update.busy() ? GONE : VISIBLE);
        action.setEnabled(!update.busy() && update.phase != NativeAppUpdate.Phase.DISABLED);
        Runnable click = check;
        String label = ui.t("Check for updates"), text = "";
        switch (update.phase) {
            case DISABLED: text = ui.t("App updates are available in release builds."); break;
            case CHECKING: text = ui.t("Checking for updates..."); break;
            case CURRENT: text = ui.t("You are up to date."); break;
            case AVAILABLE:
                label = ui.t("Download v{version}", "version", update.release.version); click = download; break;
            case DOWNLOADING:
                text = ui.t("Downloading update: {percent}%", "percent", progress.getProgress());
                label = ui.t("Download update"); break;
            case READY:
                label = ui.t("Update v{version}", "version", update.release.version); click = install; break;
            case VERIFYING: text = ui.t("Verifying update..."); label = ui.t("Install update"); break;
            case ERROR:
                if (update.release != null) { label = ui.t("Download v{version}", "version", update.release.version); click = download; }
                break;
            default: break;
        }
        if (!update.message.isEmpty()) text = ui.t(update.message);
        status.setText(text); status.setVisibility(text.isEmpty() ? GONE : VISIBLE);
        action.setText(label); Runnable selected = click; action.setOnClickListener(view -> selected.run());
    }
}
