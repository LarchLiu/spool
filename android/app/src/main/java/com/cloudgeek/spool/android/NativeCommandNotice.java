package com.cloudgeek.spool.android;

import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/** Scroll-capped, selectable goal result, separate from transcript turns. */
final class NativeCommandNotice extends NativeSurface {
    final TextView text;
    final NativeUi.IconButton close;
    final LinearLayout row;

    NativeCommandNotice(NativeUi ui, String value, Runnable dismiss) {
        super(ui, ui.elevated, ui.line, 10, 0, 0, 0);
        setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        ScrollView scroll = new ScrollView(ui.context);
        scroll.setVerticalScrollBarEnabled(false);
        row = ui.row(); row.setGravity(android.view.Gravity.TOP);
        row.setPadding(ui.dp(15), ui.dp(12), ui.dp(15), ui.dp(12));
        text = ui.text(value, 12, ui.text); text.setTypeface(android.graphics.Typeface.MONOSPACE);
        ui.normalFallbackLineSpacing(text);
        text.setTextIsSelectable(true); ui.preciseText(text);
        text.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        row.addView(text, new LayoutParams(0, -2, 1));
        close = ui.icon("close", 15, ui.t("Dismiss"), dismiss);
        LayoutParams button = new LayoutParams(ui.dp(32), ui.dp(32)); button.leftMargin = ui.dp(10); row.addView(close, button);
        scroll.addView(row, new ScrollView.LayoutParams(-1, -2));
        addView(scroll, new LayoutParams(-1, -2));
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int maximum = Math.min(Math.round(180 * getResources().getDisplayMetrics().density),
                MeasureSpec.getMode(heightSpec) == MeasureSpec.UNSPECIFIED ? Integer.MAX_VALUE : MeasureSpec.getSize(heightSpec));
        super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(maximum, MeasureSpec.AT_MOST));
    }
}
