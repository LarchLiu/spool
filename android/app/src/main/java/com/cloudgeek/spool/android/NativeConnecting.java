package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Web's loading page while the retained session is being checked. */
final class NativeConnecting extends FrameLayout {
    NativeConnecting(NativeUi ui, String error, Runnable retry, Runnable settings) {
        super(ui.context);
        LinearLayout body = ui.padded(28, 25);
        body.setGravity(Gravity.CENTER);
        body.addView(ui.logo(32));
        body.addView(ui.space(15));
        TextView status = ui.text(ui.t("Connecting to your workspace…"), 12, ui.muted);
        status.setGravity(Gravity.CENTER);
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        body.addView(status);
        if (error != null && !error.isEmpty()) {
            body.addView(ui.space(15));
            TextView message = ui.text(error, 12, ui.error);
            message.setGravity(Gravity.CENTER);
            body.addView(message);
            body.addView(ui.space(15));
            body.addView(ui.button(ui.t("Retry"), false, retry));
        }
        addView(body, new LayoutParams(-1, -1));
        NativeUi.IconButton preferences = ui.icon("settings", 20, ui.t("Settings"), settings);
        LayoutParams top = new LayoutParams(ui.dp(32), ui.dp(32), Gravity.TOP | Gravity.END);
        top.topMargin = ui.dp(20);
        top.setMarginEnd(ui.dp(20));
        addView(preferences, top);
    }
}
