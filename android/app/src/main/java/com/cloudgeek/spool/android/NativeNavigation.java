package com.cloudgeek.spool.android;

import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

/** Navigation membership is authoritative; SSE-newer metadata survives only for listed chats. */
final class NativeNavigation {
    private Set<String> previousIDs;
    static final class Selection {
        final String project, chat;
        Selection(String project, String chat) { this.project = project; this.chat = chat; }
    }

    Selection select(String project, String chat, JSONArray projects, JSONArray chats) {
        boolean found = false;
        for (int i = 0; i < projects.length(); i++) found |= projects.optJSONObject(i).optString("id").equals(project);
        Set<String> ids = new HashSet<>();
        for (int i = 0; i < chats.length(); i++) ids.add(chats.optJSONObject(i).optString("id"));
        if (!found) {
            project = projects.length() == 0 ? "" : projects.optJSONObject(0).optString("id");
            chat = first(project, chats);
        } else if (previousIDs == null && chat.isEmpty()) chat = first(project, chats);
        else if (previousIDs != null && previousIDs.contains(chat) && !ids.contains(chat)) chat = "";
        previousIDs = ids;
        return new Selection(project, chat);
    }

    private String first(String project, JSONArray chats) {
        for (int i = 0; i < chats.length(); i++) {
            JSONObject chat = chats.optJSONObject(i);
            if (chat.optString("project_id").equals(project)) return chat.optString("id");
        }
        return "";
    }

    static JSONArray merge(JSONArray previous, JSONArray incoming) {
        Map<String, JSONObject> old = new HashMap<>();
        for (int i = 0; i < previous.length(); i++) {
            JSONObject value = previous.optJSONObject(i); if (value != null) old.put(value.optString("id"), value);
        }
        JSONArray result = new JSONArray();
        for (int i = 0; i < incoming.length(); i++) {
            JSONObject next = incoming.optJSONObject(i); if (next == null) continue;
            JSONObject before = old.get(next.optString("id"));
            if (before != null && before.optDouble("updated_at") > next.optDouble("updated_at")) {
                next = NativeChatState.copy(before);
                if (incoming.optJSONObject(i).has("can_control")) NativeChatState.put(next, "can_control", incoming.optJSONObject(i).opt("can_control"));
                else next.remove("can_control");
            }
            result.put(next);
        }
        return result;
    }

    void copyFrom(NativeNavigation other) { previousIDs = other.previousIDs == null ? null : new HashSet<>(other.previousIDs); }
    void reset() { previousIDs = null; }
}
