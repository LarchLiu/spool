package com.cloudgeek.spool.android;

import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;

/** Rebind ActivityDetail sections without replacing an unchanged input or result document. */
final class NativeToolDetail extends LinearLayout {
    private final class Block {
        final TextView label = ui.text("", 10, ui.subtle);
        final View gap = ui.space(4);
        NativePatchView.State state = new NativePatchView.State();
        View content;
        String source, mode;
        Block() { label.setLetterSpacing(.05f); }
        void bind(String title, String value, boolean thinking, boolean input) {
            String heading = title.toUpperCase(java.util.Locale.ROOT);
            if (!android.text.TextUtils.equals(label.getText(), heading)) label.setText(heading);
            String nextMode = thinking ? "thinking" : "patch";
            if (value.equals(source) && nextMode.equals(mode)) return;
            long started = System.nanoTime();
            if (mode != null && !nextMode.equals(mode)) state = new NativePatchView.State();
            if (thinking && content != null && nextMode.equals(mode)) NativeMarkdown.bind(content, value);
            else if (thinking) content = NativeMarkdown.detail(ui, value, open);
            else content = NativePatchView.bind(ui, nextMode.equals(mode) ? content : null, value, input, state);
            mode = nextMode; source = value;
            if (ui.toolDetails != null) ui.toolDetails.rendered(thinking ? "thinking" : "other", System.nanoTime()-started);
        }
        void add(List<View> views) { views.add(label); views.add(gap); views.add(content); }
        void bindLazy(String title, org.json.JSONObject summary, boolean thinking, boolean input) {
            String heading = title.toUpperCase(java.util.Locale.ROOT);
            if (!android.text.TextUtils.equals(label.getText(), heading)) label.setText(heading);
            if (content instanceof NativeToolBody && "lazy".equals(mode)) ((NativeToolBody) content).bind(summary);
            else content = new NativeToolBody(ui, summary, thinking, input, open);
            mode = "lazy"; source = null;
        }
    }
    private final NativeUi ui;
    private final Consumer<String> open;
    private final View between;
    private Block input, output;

    NativeToolDetail(NativeUi ui, NativeActivityModel.Activity activity, Consumer<String> open) {
        super(ui.context); this.ui = ui; this.open = open;
        setOrientation(VERTICAL); setPadding(ui.dp(13), ui.dp(11), ui.dp(13), ui.dp(11));
        setBackground(ui.shape(ui.input, 9, ui.borderStrong));
        between = ui.space(10); bind(activity);
    }

    void bind(NativeActivityModel.Activity activity) {
        String command = activity.use == null ? "" : activity.use.optString("text");
        String result = activity.result == null ? "" : activity.result.optString("text");
        org.json.JSONObject commandSummary = activity.use == null ? null : activity.use.optJSONObject("tool");
        org.json.JSONObject resultSummary = activity.result == null ? null : activity.result.optJSONObject("tool");
        List<View> views = new ArrayList<>();
        if (!command.trim().isEmpty() || commandSummary != null) {
            if (input == null) input = new Block();
            boolean thinking = activity.kind.equals("thinking");
            String label = ui.t(thinking ? "Thought" : "Command");
            if (!thinking && !activity.use.optString("name").isEmpty()) label += " \u00b7 " + activity.use.optString("name");
            if (commandSummary != null) input.bindLazy(label, commandSummary, thinking, true);
            else input.bind(label, command, thinking, true);
            input.add(views);
        } else input = null;
        if (!result.isEmpty() || resultSummary != null) {
            if (input != null) views.add(between);
            if (output == null) output = new Block();
            if (resultSummary != null) output.bindLazy(ui.t("Result"), resultSummary, false, false);
            else output.bind(ui.t("Result"), result, false, false);
            output.add(views);
        } else output = null;
        NativeKeyedViews.sync(this, views);
    }
}
