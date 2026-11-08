package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.Collections;
import java.util.IdentityHashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

/** One turn's chronological presentation, shared by both transcript renderers. */
final class NativeTurnPresentation {
    final JSONObject turn;
    // Turn-local indices stay valid when earlier history is prepended.
    final JSONArray parts;
    final int inputEnd;
    final String input, answer, error;
    final long inputTimestamp;
    final JSONArray attachments, actions;
    final JSONObject approval;
    final boolean running, answered;
    final List<JSONObject> process;

    private NativeTurnPresentation(JSONArray parts, int inputEnd, JSONObject turn) {
        this.turn = turn; this.parts = parts; this.inputEnd = inputEnd;
        input = NativeUserRow.text(parts, 0, inputEnd);
        inputTimestamp = NativeUserRow.timestamp(parts, 0, inputEnd, turn.optLong("started_at"));
        attachments = array(turn, "attachments"); actions = array(turn, "actions");
        approval = turn.optJSONObject("pending_approval");
        error = turn.optString("status").equals("stopped") ? "" : turn.optString("error");
        running = turn.optString("status").equals("running");
        int lastUser = inputEnd - 1, lastActivity = inputEnd - 1;
        for (int i = inputEnd; i < parts.length(); i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part == null) continue;
            if (part.optString("role").equals("user")) lastUser = i;
            if (activity(part)) lastActivity = i;
        }
        List<JSONObject> finals = new ArrayList<>();
        for (int i = lastUser + 1; i < parts.length(); i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part != null && text(part) && part.optString("phase").equals("final_answer")) finals.add(part);
        }
        if (finals.isEmpty() && !running) for (int i = Math.max(lastUser, lastActivity) + 1; i < parts.length(); i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part != null && text(part) && part.optString("phase").isEmpty()) finals.add(part);
        }
        answered = !finals.isEmpty();
        Set<JSONObject> answers = Collections.newSetFromMap(new IdentityHashMap<>());
        answers.addAll(finals);
        List<JSONObject> activity = new ArrayList<>();
        for (int i = inputEnd; i < parts.length(); i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part != null && !answers.contains(part)) activity.add(part);
        }
        process = Collections.unmodifiableList(activity);
        StringBuilder result = new StringBuilder(); String previous = "";
        for (JSONObject part : finals) {
            String id = part.optString("id");
            if (result.length() > 0 && (id.isEmpty() || !id.equals(previous))) result.append("\n\n");
            result.append(part.optString("text")); previous = id;
        }
        answer = result.toString();
    }

    static NativeTurnPresentation turn(JSONArray all, JSONObject turn) {
        JSONArray parts = slice(all, turn.optInt("part_start"), turn.optInt("part_end", all.length()));
        return new NativeTurnPresentation(parts, NativeTurnExchange.split(parts, 0, parts.length()).get(0).inputEnd, turn);
    }

    /** Compatibility for callers that already supply only the response range. */
    static NativeTurnPresentation response(JSONArray parts, int start, int end, JSONObject turn) {
        return new NativeTurnPresentation(slice(parts, start, end), 0, turn);
    }

    private static JSONArray slice(JSONArray all, int start, int end) {
        JSONArray result = new JSONArray();
        for (int i = Math.max(0, start); i < Math.min(all.length(), end); i++) result.put(all.opt(i));
        return result;
    }
    private static JSONArray array(JSONObject value, String key) {
        JSONArray result = value.optJSONArray(key); return result == null ? new JSONArray() : result;
    }
    private static boolean text(JSONObject part) {
        return part.optString("role").equals("assistant") && part.optString("kind").equals("text") && !part.optString("text").trim().isEmpty();
    }
    private static boolean activity(JSONObject part) {
        String kind = part.optString("kind");
        return !part.optString("role").equals("user") && (kind.equals("thinking") || kind.equals("tool_use") || kind.equals("tool_result"));
    }

    static final class Cache {
        private String scope = "";
        private Map<String, NativeTurnPresentation> current = new LinkedHashMap<>();
        long builds;
        Map<String, NativeTurnPresentation> update(String scope, JSONObject page, Set<String> changed) {
            if (!this.scope.equals(scope)) { clear(); this.scope = scope; }
            Map<String, NativeTurnPresentation> next = new LinkedHashMap<>();
            if (page != null) {
                JSONArray turns = array(page, "turns"), parts = page.optJSONObject("transcript") == null
                        ? new JSONArray() : array(page.optJSONObject("transcript"), "parts");
                Map<String, Integer> occurrences = new LinkedHashMap<>();
                for (int i = 0; i < turns.length(); i++) {
                    JSONObject turn = turns.optJSONObject(i); if (turn == null) continue;
                    String id = turn.optString("id");
                    String key = new JSONArray().put(id.isEmpty() ? "index" : "turn").put(id.isEmpty() ? i : id)
                            .put(occurrences.merge(id, 1, Integer::sum)).toString();
                    NativeTurnPresentation value = current.get(key);
                    if (value == null || changed == null || id.isEmpty() || changed.contains(id)) { value = turn(parts, turn); builds++; }
                    next.put(key, value);
                }
            }
            current = next; return Collections.unmodifiableMap(current);
        }
        void clear() { current.clear(); scope = ""; }
    }
}
