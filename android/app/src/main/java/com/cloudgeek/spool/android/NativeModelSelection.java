package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;

/** Same catalog/default semantics as Web model-selection.ts. */
final class NativeModelSelection {
    final String backend, model, effort;

    NativeModelSelection(String backend, String model, String effort) {
        this.backend = backend;
        this.model = model;
        this.effort = effort;
    }

    NativeModelSelection withEffort(String value) { return new NativeModelSelection(backend, model, value); }

    static JSONObject entry(String id) {
        try { return new JSONObject().put("model", id).put("displayName", id); }
        catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
    }

    static NativeModelSelection chat(JSONArray agents, JSONObject chat) {
        String backend = chat.optString("backend"), id = chat.optString("model");
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = agents.optJSONObject(i);
            if (agent == null || !agent.optString("backend").equals(backend)) continue;
            JSONObject catalog = agent.optJSONObject("catalog");
            if (id.isEmpty() && catalog != null) {
                id = catalog.optString("defaultModel");
                JSONArray models = catalog.optJSONArray("models");
                JSONObject first = models == null ? null : models.optJSONObject(0);
                if (id.isEmpty() && first != null) id = first.optString("model");
            }
            NativeModelSelection selection = choose(agent, id);
            return chat.optString("reasoning_effort").isEmpty() ? selection : selection.withEffort(chat.optString("reasoning_effort"));
        }
        return new NativeModelSelection(backend, id, chat.optString("reasoning_effort"));
    }

    static JSONObject model(JSONObject agent, String id) {
        JSONObject catalog = agent.optJSONObject("catalog");
        JSONArray models = catalog == null ? null : catalog.optJSONArray("models");
        if (models != null) for (int i = 0; i < models.length(); i++) {
            JSONObject model = models.optJSONObject(i);
            if (model != null && model.optString("model").equals(id)) return model;
        }
        return new JSONObject();
    }

    static NativeModelSelection choose(JSONObject agent, String id) {
        JSONObject catalog = agent.optJSONObject("catalog"), entry = model(agent, id);
        String backend = agent.optString("backend");
        String effort = catalog != null && catalog.optString("defaultModel").equals(id)
                ? catalog.optString("defaultEffort") : "";
        if (effort.isEmpty()) effort = entry.optString("defaultReasoningEffort");
        if (backend.equals("claude")) {
            boolean valid = false;
            JSONArray levels = entry.optJSONArray("supportedReasoningEfforts");
            if (levels != null) for (int i = 0; i < levels.length(); i++) {
                JSONObject level = levels.optJSONObject(i);
                valid |= level != null && level.optString("reasoningEffort").equals(effort);
            }
            if (!valid) effort = "";
        }
        return new NativeModelSelection(backend, id, effort);
    }

    static List<String> levels(String backend, JSONObject entry) {
        List<String> values = new ArrayList<>();
        JSONArray levels = entry.optJSONArray("supportedReasoningEfforts");
        if (levels == null || levels.length() == 0) return values;
        if (backend.equals("claude")) values.add("");
        for (int i = 0; i < levels.length(); i++) {
            JSONObject level = levels.optJSONObject(i);
            if (level != null) values.add(level.optString("reasoningEffort"));
        }
        return values;
    }
}
