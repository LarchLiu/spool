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
    private final NativeActivityList.State state;
    private final Consumer<String> open;
    private final NativeTranscriptImages images;
    private final NativeKeyedViews children;
    private JSONObject current;
    private Object bound;

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
        super(ui.context);
        this.ui = ui; this.state = state; this.open = open; this.images = images;
        setOrientation(VERTICAL);
        children = new NativeKeyedViews(this);
        tick = new Runnable() {
            @Override public void run() {
                main.removeCallbacks(this);
                activity.setSummary(summary(ui, current, answered, System.currentTimeMillis()));
                if (isAttachedToWindow() && getWindowVisibility() == VISIBLE && running) main.postDelayed(this, 1000);
            }
        };
        bind(parts, start, end, turn);
    }

    void bind(JSONArray parts, int start, int end, JSONObject turn) {
        JSONArray inputs = new JSONArray(), content = new JSONArray();
        for (int i = start; i < end; i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part != null && !part.optString("role").equals("user")) content.put(part);
        }
        inputs.put(content);
        for (String field : new String[] {"status", "compacting", "backend", "model", "reasoning_effort", "started_at", "finished_at"}) inputs.put(turn.opt(field));
        if (NativeChatState.equivalent(bound, inputs)) return;
        bound = NativeChatState.snapshot(inputs); current = turn;
        running = turn.optString("status").equals("running");
        List<JSONObject> finalParts = new ArrayList<>(), process = new ArrayList<>();
        int lastActivity = -1;
        for (int i = start; i < end; i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part == null) continue;
            if (activity(part)) lastActivity = i;
            if (text(part) && part.optString("phase").equals("final_answer")) finalParts.add(part);
        }
        if (finalParts.isEmpty() && !running)
            for (int i = Math.max(start, lastActivity + 1); i < end; i++) {
                JSONObject part = parts.optJSONObject(i);
                if (part != null && text(part) && part.optString("phase").isEmpty()) finalParts.add(part);
            }
        for (int i = start; i < end; i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part != null && (activity(part) || text(part) && !finalParts.contains(part))) process.add(part);
        }
        answered = !finalParts.isEmpty();
        CharSequence summary = summary(ui, turn, answered, System.currentTimeMillis());
        children.begin();
        activity = (NativeActivityList) children.use("activity", "activity",
                () -> new NativeActivityList(ui, process, turn.optString("backend"), summary, running && !answered, state, open, images));
        activity.bindParts(process, turn.optString("backend"), summary, running && !answered);
        StringBuilder answer = new StringBuilder();
        String previous = "";
        for (JSONObject part : finalParts) {
            String id = part.optString("id");
            if (answer.length() > 0 && (id.isEmpty() || !id.equals(previous))) answer.append("\n\n");
            answer.append(part.optString("text"));
            previous = id;
        }
        if (answer.length() > 0) {
            String source = answer.toString();
            NativeMessage message = (NativeMessage) children.use("answer", "answer",
                    () -> new NativeMessage(ui, source, false, null, turn.optLong("finished_at"), ui.chinese, open));
            message.bindSource(source);
            message.footer.bindTimestamp(turn.optLong("finished_at"));
        }
        children.end();
        main.removeCallbacks(tick);
        if (running && isAttachedToWindow() && getWindowVisibility() == VISIBLE) tick.run();
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

    private static boolean text(JSONObject part) {
        return part.optString("role").equals("assistant") && part.optString("kind").equals("text")
                && !part.optString("text").trim().isEmpty();
    }

    private static boolean activity(JSONObject part) {
        String kind = part.optString("kind");
        return !part.optString("role").equals("user")
                && (kind.equals("thinking") || kind.equals("tool_use") || kind.equals("tool_result"));
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
