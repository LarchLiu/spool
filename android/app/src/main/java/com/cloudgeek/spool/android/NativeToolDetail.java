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
        private void heading(String title) {
            String heading = title.toUpperCase(java.util.Locale.ROOT);
            if (!android.text.TextUtils.equals(label.getText(), heading)) label.setText(heading);
        }
        void bind(String title, String value, boolean thinking, boolean input) {
            heading(title);
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
        void bindRaw(String title, String value) {
            heading(title);
            if (value.equals(source) && "raw".equals(mode)) return;
            content = NativeActivityList.raw(ui, value);
            mode = "raw"; source = value;
        }
        void add(List<View> views) { views.add(label); views.add(gap); views.add(content); }
        void bindLazy(String title, org.json.JSONObject summary, boolean thinking, boolean input) {
            heading(title);
            if (content instanceof NativeToolBody && "lazy".equals(mode)) ((NativeToolBody) content).bind(summary);
            else content = new NativeToolBody(ui, summary, thinking, input, open);
            mode = "lazy"; source = null;
        }
    }
    private final NativeUi ui;
    private final Consumer<String> open;
    private final View between;
    private Block input, output;
    // A subagent's detail: what it was asked, its latest steps, what it last said, and its facts.
    private final View[] taskGaps = new View[3];
    private final Runnable tick;
    private TextView stepsLabel, facts;
    private LinearLayout steps;
    private View stepsViewport;
    private String stepsSource;
    private NativeActivityModel.Activity task;

    NativeToolDetail(NativeUi ui, NativeActivityModel.Activity activity, Consumer<String> open) {
        super(ui.context); this.ui = ui; this.open = open;
        setOrientation(VERTICAL); setPadding(ui.dp(13), ui.dp(11), ui.dp(13), ui.dp(11));
        setBackground(ui.shape(ui.input, 9, ui.borderStrong));
        between = ui.space(10);
        // A working child sends nothing during one long step: its time is counted here.
        tick = new Runnable() {
            @Override public void run() {
                removeCallbacks(this);
                if (task == null || facts == null) return;
                String value = NativeActivityModel.taskMetadata(ui, task, true, System.currentTimeMillis());
                if (!value.contentEquals(facts.getText())) facts.setText(value);
                facts.setVisibility(value.isEmpty() ? GONE : VISIBLE);
                if (isAttachedToWindow() && getWindowVisibility() == VISIBLE && NativeActivityModel.taskStartedAt(task) > 0) postDelayed(this, 1000);
            }
        };
        bind(activity);
    }

    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); tick.run(); }

    @Override protected void onDetachedFromWindow() { removeCallbacks(tick); super.onDetachedFromWindow(); }

    @Override protected void onWindowVisibilityChanged(int visibility) {
        super.onWindowVisibilityChanged(visibility);
        // The superclass constructor reports visibility before this view has its fields.
        if (tick != null) {
            if (visibility == VISIBLE) tick.run();
            else removeCallbacks(tick);
        }
    }

    /** Web TaskDetail: the same blocks in the same order, each present only when it has something to say. */
    private boolean bindTask(NativeActivityModel.Activity activity) {
        org.json.JSONObject progress = NativeActivityModel.task(activity);
        if (progress == null) return false;
        String status = NativeActivityModel.status(activity), prompt = progress.optString("prompt"), summary = progress.optString("summary");
        org.json.JSONArray list = progress.optJSONArray("steps");
        task = activity;
        String told = NativeActivityModel.taskMetadata(ui, activity, true, System.currentTimeMillis());
        boolean showFacts = !told.isEmpty() || NativeActivityModel.taskStartedAt(activity) > 0;
        if (prompt.isEmpty() && summary.isEmpty() && !showFacts && (list == null || list.length() == 0)) return false;
        List<View> views = new ArrayList<>();
        if (!prompt.isEmpty()) {
            if (input == null) input = new Block();
            input.bindRaw(ui.t("Prompt"), prompt); input.add(views);
        } else input = null;
        if (list != null && list.length() > 0) {
            if (steps == null) {
                steps = ui.column();
                stepsViewport = NativeActivityList.detailViewport(ui, steps);
                stepsLabel = ui.text(ui.t("Recent steps").toUpperCase(java.util.Locale.ROOT), 10, ui.subtle); stepsLabel.setLetterSpacing(.05f);
                stepsSource = null;
            }
            // Statuses are worded here, so the wording is part of what was rendered.
            String source = ui.t("Running") + '\n' + list;
            if (!source.equals(stepsSource)) {
                stepsSource = source; steps.removeAllViews();
                for (int i = 0; i < list.length(); i++) {
                    org.json.JSONObject step = list.optJSONObject(i);
                    if (step == null) continue;
                    boolean message = step.optString("kind").equals("message");
                    String label = NativeActivityModel.taskStepLabel(ui, step), outcome = NativeActivityModel.taskStepOutcome(ui, step);
                    // The layout of an activity row, without its disclosure.
                    LinearLayout row = ui.row();
                    row.addView(ui.decorativeIcon(message ? "message" : NativeActivityModel.icon(step.optString("kind")), 12));
                    TextView text = ui.text(label, 12, message ? ui.subtle : ui.muted);
                    ui.lineHeight(text, 1.5f); text.setSingleLine(true); text.setEllipsize(android.text.TextUtils.TruncateAt.END);
                    LayoutParams textParams = new LayoutParams(0, -2, 1); textParams.setMarginStart(ui.dp(7));
                    row.addView(text, textParams);
                    if (!outcome.isEmpty()) {
                        TextView state = ui.text(outcome, 11, step.optString("status").equals("running") ? ui.muted : ui.error);
                        state.setSingleLine(true);
                        LayoutParams stateParams = new LayoutParams(-2, -2); stateParams.setMarginStart(ui.dp(7));
                        row.addView(state, stateParams);
                    }
                    LayoutParams params = new LayoutParams(-1, -2); if (i > 0) params.topMargin = ui.dp(3);
                    steps.addView(row, params);
                }
            }
            if (!views.isEmpty()) views.add(taskGap(0));
            views.add(stepsLabel); views.add(taskGap(1)); views.add(stepsViewport);
        } else { steps = null; stepsLabel = null; stepsViewport = null; stepsSource = null; }
        if (!summary.isEmpty()) {
            if (!views.isEmpty()) views.add(between);
            if (output == null) output = new Block();
            output.bind(ui.t(status.matches("completed|failed") ? "Result" : "Latest update"), summary, true, false); output.add(views);
        } else output = null;
        if (showFacts) {
            if (facts == null) {
                facts = ui.text("", 11, ui.subtle);
                facts.setFontFeatureSettings("tnum");
            }
            if (!views.isEmpty()) views.add(taskGap(2));
            views.add(facts);
        } else facts = null;
        NativeKeyedViews.sync(this, views);
        tick.run();
        return true;
    }

    private View taskGap(int index) {
        if (taskGaps[index] == null) taskGaps[index] = ui.space(index == 1 ? 4 : 10);
        return taskGaps[index];
    }

    void bind(NativeActivityModel.Activity activity) {
        if (bindTask(activity)) return;
        task = null; steps = null; stepsLabel = null; stepsViewport = null; stepsSource = null; facts = null;
        removeCallbacks(tick);
        String command = activity.use == null ? "" : activity.use.optString("text");
        // Task progress is inline, never loaded as an ordinary tool call or result.
        if (activity.kind.equals("task")) {
            if (input == null) input = new Block();
            input.bindRaw("", command);
            output = null;
            NativeKeyedViews.sync(this, java.util.Collections.singletonList(input.content));
            return;
        }
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
