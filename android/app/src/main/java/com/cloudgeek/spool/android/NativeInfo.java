package com.cloudgeek.spool.android;

import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.Supplier;

/** Title-adjacent information, opened explicitly and refreshed while visible. */
final class NativeInfo {
    final NativeUi.IconButton button;
    private final NativeUi ui;
    private final String title;
    private final Supplier<String> details;
    private NativePopover popup;
    private TextView text;

    NativeInfo(NativeUi ui, String title, Supplier<String> details) {
        this.ui = ui; this.title = title; this.details = details;
        button = ui.icon("info", 14, ui.t("Information") + ": " + title, this::show);
    }

    private void show() {
        if (popup != null && popup.isShowing()) { popup.dismiss(); return; }
        popup = new NativePopover(ui, 300, 10, 13);
        popup.content.addView(ui.bold(title, 12)); popup.content.addView(ui.space(8));
        text = ui.text(details.get(), 12, ui.text); text.setTextIsSelectable(true); ui.fixedLineHeight(text, 1.6f);
        popup.content.addView(text, new LinearLayout.LayoutParams(-1, -2));
        popup.show(button, false);
    }

    void refresh() { if (popup != null && popup.isShowing()) text.setText(details.get()); }
}
