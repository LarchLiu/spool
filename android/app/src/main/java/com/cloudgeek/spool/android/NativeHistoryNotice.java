package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Original history status paragraphs and the text-only retry control. */
final class NativeHistoryNotice extends LinearLayout {
    final TextView unavailable, loading, retry;
    NativeHistoryNotice(NativeUi ui, boolean error, boolean more, boolean failed, Runnable reload) {
        super(ui.context); setOrientation(VERTICAL); setGravity(Gravity.CENTER_HORIZONTAL);
        unavailable = text(ui, ui.t("Native session history is unavailable"), 12);
        loading = text(ui, ui.t("Loading earlier messages…"), 12);
        retry = text(ui, ui.t("Retry loading earlier messages"), 10);
        unavailable.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        loading.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        retry.setPadding(ui.dp(12), ui.dp(8), ui.dp(12), ui.dp(8));
        retry.setFocusable(true); retry.setOnClickListener(view -> reload.run()); ui.buttonKeys(retry);
        retry.setAccessibilityDelegate(new View.AccessibilityDelegate() {
            @Override public void onInitializeAccessibilityNodeInfo(View host, android.view.accessibility.AccessibilityNodeInfo info) {
                super.onInitializeAccessibilityNodeInfo(host, info); info.setClassName("android.widget.Button");
            }
        });
        add(unavailable, error, -1, ui); add(loading, more && !failed, -1, ui); add(retry, failed, -2, ui);
    }
    private TextView text(NativeUi ui, String value, int size) {
        TextView text = ui.text(value, size, ui.muted); text.setGravity(Gravity.CENTER);
        text.setLineSpacing(0, 1); ui.normalFallbackLineSpacing(text); ui.cssNormalLabel(text); ui.preciseText(text);
        return text;
    }
    private void add(TextView view, boolean visible, int width, NativeUi ui) {
        if (!visible) return;
        LayoutParams params = new LayoutParams(width, -2); params.bottomMargin = ui.dp(24);
        addView(view, params);
    }
}
