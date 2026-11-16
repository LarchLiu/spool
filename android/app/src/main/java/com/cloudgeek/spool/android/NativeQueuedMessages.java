package com.cloudgeek.spool.android;

import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.function.Consumer;

/** Messages waiting for the running turn, attached to the top of the composer box. */
final class NativeQueuedMessages extends LinearLayout {
    private final NativeUi ui;
    private final Consumer<String> steer, delete;
    private String shown = "";

    NativeQueuedMessages(NativeUi ui, Consumer<String> steer, Consumer<String> delete) {
        super(ui.context);
        this.ui = ui; this.steer = steer; this.delete = delete;
        setOrientation(VERTICAL);
        setBackground(ui.shape(ui.elevated, 12, ui.line));
        setContentDescription(ui.t("Queued messages"));
        setVisibility(GONE);
    }

    void bind(JSONArray messages, boolean running, boolean busy) {
        int count = messages == null ? 0 : messages.length();
        String state = count == 0 ? "" : messages + "|" + running + "|" + busy;
        if (state.equals(shown)) return;
        shown = state;
        removeAllViews();
        setVisibility(count == 0 ? GONE : VISIBLE);
        for (int i = 0; i < count; i++) {
            JSONObject message = messages.optJSONObject(i);
            if (message == null) continue;
            final String id = message.optString("id");
            String body = message.optString("message").trim();
            JSONArray files = message.optJSONArray("attachments");
            if (body.isEmpty() && files != null) body = files.optString(0);
            LinearLayout row = ui.row();
            row.setPadding(ui.dp(12), ui.dp(1), ui.dp(4), ui.dp(1));
            TextView text = ui.text(body, 13, ui.text);
            text.setSingleLine(true); text.setEllipsize(TextUtils.TruncateAt.END);
            row.addView(text, new LayoutParams(0, -2, 1));
            TextView action = ui.text("↳ " + ui.t(running ? "Steer" : "Send now"), 12, ui.muted);
            action.setGravity(Gravity.CENTER); action.setPadding(ui.dp(8), 0, ui.dp(8), 0);
            action.setFocusable(true); action.setEnabled(!busy); action.setAlpha(busy ? .45f : 1);
            action.setOnClickListener(view -> steer.accept(id));
            row.addView(action, new LayoutParams(-2, ui.dp(32)));
            NativeUi.IconButton remove = ui.icon("close", 15, ui.t("Delete queued message"), () -> delete.accept(id));
            remove.setEnabled(!busy);
            row.addView(remove, new LayoutParams(ui.dp(32), ui.dp(32)));
            addView(row, new LayoutParams(-1, ui.dp(34)));
        }
    }

    int rows() { return getVisibility() == View.VISIBLE ? getChildCount() : 0; }
}
