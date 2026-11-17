package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.widget.LinearLayout;
import android.widget.TextView;

/** A turn's error can be dismissed until that turn reports another failure. */
final class NativeTurnError extends LinearLayout {
    static final class State {
        String message = "";
        long finishedAt;
        boolean dismissed;

        void update(String message, long finishedAt) {
            if (!this.message.equals(message) || this.finishedAt != finishedAt) dismissed = false;
            this.message = message; this.finishedAt = finishedAt;
        }
    }

    final TextView text;
    final NativeUi.IconButton close;

    NativeTurnError(NativeUi ui, State state) {
        super(ui.context);
        setOrientation(HORIZONTAL); setGravity(Gravity.TOP);
        setPadding(ui.dp(14), ui.dp(11), ui.dp(14), ui.dp(11));
        setBackground(ui.shape(ui.errorBg, 10, ui.errorBorder));
        text = ui.text(state.message, 12, ui.error); ui.fixedLineHeight(text, 1.7f);
        text.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_ASSERTIVE);
        addView(text, new LayoutParams(0, -2, 1));
        close = ui.icon("close", 15, ui.t("Dismiss"), () -> { state.dismissed = true; setVisibility(GONE); });
        close.setTranslationX(ui.dp(12)); close.setTranslationY(-ui.dp(6));
        LayoutParams button = new LayoutParams(ui.dp(32), ui.dp(32)); button.leftMargin = ui.dp(10);
        addView(close, button);
        LayoutParams position = new LayoutParams(-1, -2); position.topMargin = position.bottomMargin = ui.dp(10);
        setLayoutParams(position);
        setVisibility(state.dismissed ? GONE : VISIBLE);
    }
}
