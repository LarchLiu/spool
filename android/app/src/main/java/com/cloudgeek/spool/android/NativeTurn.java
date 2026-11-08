package com.cloudgeek.spool.android;

import android.os.Handler;
import android.os.Looper;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.style.ForegroundColorSpan;
import android.widget.LinearLayout;
import org.json.JSONArray;
import org.json.JSONObject;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;

/** Native TurnResponse follows Web turnPresentation and preserves live activity disclosure state. */
final class NativeTurn extends LinearLayout {
    NativeActivityList activity;
    private final Handler main = new Handler(Looper.getMainLooper());
    private boolean running, answered;
    private final Runnable tick;
    private final NativeUi ui;
    private NativeActivityList.State state;
    private final Consumer<String> open;
    private NativeTranscriptImages images;
    private final boolean renderAnswer;
    private final NativeKeyedViews children;
    private JSONObject current;
    private Object bound;
    private NativeTurnPresentation presentation;
    private Runnable activityRowsChanged;

    NativeTurn(NativeUi ui, JSONArray parts, int start, int end, JSONObject turn,
            boolean chinese, Consumer<String> open) {
        this(ui, parts, start, end, turn, chinese, new NativeActivityList.State(), open);
    }

    NativeTurn(NativeUi ui, JSONArray parts, int start, int end, JSONObject turn,
            boolean chinese, NativeActivityList.State state, Consumer<String> open) {
        this(ui, parts, start, end, turn, chinese, state, open, null);
    }

    NativeTurn(NativeUi ui, JSONArray parts, int start, int end, JSONObject turn,
            boolean chinese, NativeActivityList.State state, Consumer<String> open, NativeTranscriptImages images) {
        this(ui, parts, start, end, turn, chinese, state, open, images, true);
    }

    NativeTurn(NativeUi ui, JSONArray parts, int start, int end, JSONObject turn,
            boolean chinese, NativeActivityList.State state, Consumer<String> open, NativeTranscriptImages images, boolean renderAnswer) {
        this(ui, NativeTurnPresentation.response(parts, start, end, turn), state, open, images, renderAnswer, null);
    }

    NativeTurn(NativeUi ui, NativeTurnPresentation presentation, NativeActivityList.State state,
            Consumer<String> open, NativeTranscriptImages images, boolean renderAnswer, Runnable activityRowsChanged) {
        super(ui.context);
        this.ui = ui; this.state = state; this.open = open; this.images = images;
        this.renderAnswer = renderAnswer; this.activityRowsChanged = activityRowsChanged;
        setOrientation(VERTICAL);
        children = new NativeKeyedViews(this);
        tick = new Runnable() {
            @Override public void run() {
                main.removeCallbacks(this);
                activity.setSummary(summary(ui, current, answered, System.currentTimeMillis()));
                if (isAttachedToWindow() && getWindowVisibility() == VISIBLE && running) main.postDelayed(this, 1000);
            }
        };
        bind(presentation);
    }

    void bind(JSONArray parts, int start, int end, JSONObject turn) {
        JSONArray actions = turn.optJSONArray("actions");
        boolean nextRunning = turn.optString("status").equals("running");
        if (!renderAnswer && !nextRunning && !state.open) {
            // A Markwon answer has its own block adapter. This folded row only displays
            // turn metadata; keep the latest deferred parts without cloning tool JSON.
            bound = null;
        } else {
            JSONArray inputs = new JSONArray(), content = new JSONArray();
            for (int i = start; i < end; i++) {
                JSONObject part = parts.optJSONObject(i);
                if (part != null) content.put(part);
            }
            inputs.put(content);
            for (String field : new String[] {"status", "compacting", "backend", "model", "reasoning_effort", "started_at", "finished_at", "actions"}) inputs.put(turn.opt(field));
            if (actions != null && ui.people != null) for (int i = 0; i < actions.length(); i++) {
                JSONObject action = actions.optJSONObject(i);
                if (action != null) inputs.put(ui.people.apply(action.optString("user_id")));
            }
            if (NativeChatState.equivalent(bound, inputs)) return;
            bound = NativeChatState.snapshot(inputs);
        }
        bind(NativeTurnPresentation.response(parts, start, end, turn));
    }

    void bind(NativeTurnPresentation value) {
        if (presentation == value) return;
        presentation = value; current = value.turn; running = value.running; answered = value.answered;
        CharSequence summary = summary(ui, current, answered, System.currentTimeMillis());
        children.begin();
        NativeActivityList previous = activity;
        activity = (NativeActivityList) children.use("activity", "activity",
                () -> new NativeActivityList(ui, value.process, current.optString("backend"), summary,
                        running && !answered, state, open, images, activityRowsChanged));
        if (activity == previous) activity.bindParts(value.process, current.optString("backend"), summary, running && !answered);
        if (!value.answer.isEmpty() && renderAnswer) {
            NativeMessage message = (NativeMessage) children.use("answer", "answer",
                    () -> new NativeMessage(ui, value.answer, false, null, current.optLong("finished_at"), ui.chinese, open));
            message.bindSource(value.answer); message.bindActions(value.actions);
            message.footer.bindTimestamp(current.optLong("finished_at"));
        } else if (value.answer.isEmpty() && value.actions.length() > 0 && activityRowsChanged == null) {
            NativeTurnActions audit = (NativeTurnActions) children.use("actions", "actions", () -> new NativeTurnActions(ui));
            audit.bind(value.actions);
        }
        children.end();
        main.removeCallbacks(tick);
        if (running && isAttachedToWindow() && getWindowVisibility() == VISIBLE) tick.run();
    }

    static String answer(JSONArray parts, int start, int end, JSONObject turn) {
        return NativeTurnPresentation.response(parts, start, end, turn).answer;
    }

    void userInput(NativeActivityList.UserInput input) { activity.userInput(input); }

    void activityRowsChanged(Runnable changed) {
        activityRowsChanged = changed; activity.rowsChanged(changed);
    }

    void rebindForTurn(NativeActivityList.State next, NativeTranscriptImages nextImages) {
        if (state == next) return;
        main.removeCallbacks(tick); running = answered = false; current = null; bound = null; presentation = null;
        state = next; images = nextImages; activity.rebindForTurn(next, nextImages);
    }

    @Override protected void onAttachedToWindow() {
        super.onAttachedToWindow();
        if (running) tick.run();
    }

    @Override protected void onDetachedFromWindow() {
        main.removeCallbacks(tick);
        super.onDetachedFromWindow();
    }

    @Override protected void onWindowVisibilityChanged(int visibility) {
        super.onWindowVisibilityChanged(visibility);
        if (tick == null) return;
        main.removeCallbacks(tick);
        if (visibility == VISIBLE && running && isAttachedToWindow()) tick.run();
    }

    static CharSequence summary(NativeUi ui, JSONObject turn, boolean answered, long now) {
        boolean running = turn.optString("status").equals("running");
        String label;
        if (running && turn.optBoolean("compacting")) label = ui.t("Compacting context…");
        else if (!running && turn.optString("status").equals("stopped")) label = ui.t("Stopped");
        else if (!running && (turn.optLong("started_at") == 0 || turn.optLong("finished_at") == 0)) label = ui.t("Completed");
        else {
            long end = running ? now : turn.optLong("finished_at");
            long seconds = Math.max(0, Math.round((end - turn.optLong("started_at")) / 1000d));
            boolean working = running && !answered;
            label = seconds < 60 ? working ? ui.t("Working for {seconds}s", "seconds", seconds)
                    : ui.t("Worked for {seconds}s", "seconds", seconds)
                    : working ? ui.t("Working for {minutes}m {seconds}s", "minutes", seconds / 60, "seconds", seconds % 60)
                    : ui.t("Worked for {minutes}m {seconds}s", "minutes", seconds / 60, "seconds", seconds % 60);
        }
        List<String> metadata = new ArrayList<>();
        String backend = turn.optString("backend");
        if (backend.equals("codex") || backend.equals("claude")) metadata.add(backend.equals("claude") ? "Claude" : "Codex");
        if (!turn.optString("model").isEmpty()) metadata.add(turn.optString("model"));
        if (!turn.optString("reasoning_effort").isEmpty()) metadata.add(NativeAgentModelMenu.effortLabel(ui, turn.optString("reasoning_effort")));
        SpannableStringBuilder text = new SpannableStringBuilder(label);
        if (!metadata.isEmpty()) {
            int start = text.length();
            text.append(" \u00b7 ").append(String.join(" \u00b7 ", metadata));
            text.setSpan(new ForegroundColorSpan(ui.subtle), start, text.length(), Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        }
        return text;
    }
}
