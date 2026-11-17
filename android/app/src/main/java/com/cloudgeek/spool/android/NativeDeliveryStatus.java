package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Small acknowledgement marker shared by queued rows and steering bubbles. */
final class NativeDeliveryStatus extends LinearLayout {
    NativeDeliveryStatus(NativeUi ui, String status, Runnable edit) {
        super(ui.context);
        setGravity(Gravity.CENTER_VERTICAL);
        setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        boolean failed = status.equals("failed");
        View icon = failed ? ui.decorativeIcon("circle_alert", 12) : new NativeSpinner(ui, 12, "");
        if (failed) ((android.widget.ImageView) icon).setImageTintList(android.content.res.ColorStateList.valueOf(ui.error));
        icon.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        addView(icon, new LayoutParams(ui.dp(12), ui.dp(12)));
        TextView text = ui.text(ui.t(failed ? "Send failed" : "Sending…"), 12, failed ? ui.error : ui.muted);
        LayoutParams label = new LayoutParams(-2, ui.dp(26)); label.leftMargin = ui.dp(5);
        text.setGravity(Gravity.CENTER_VERTICAL); addView(text, label);
        if (failed && edit != null) {
            android.widget.Button button = ui.quietButton(ui.t("Edit message"), edit);
            button.setTextSize(12); button.setMinHeight(0); button.setMinimumHeight(0);
            button.setPadding(ui.dp(6), 0, ui.dp(4), 0);
            addView(button, new LayoutParams(-2, ui.dp(26)));
        }
    }
}
