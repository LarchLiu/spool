package com.cloudgeek.spool.android;

import java.util.HashMap;
import java.util.Map;
import org.json.JSONObject;

/** Unread session events, kept in memory with their server/account workspace. */
final class NativeSessionNotifications {
    private final Map<String, String> statuses = new HashMap<>();

    boolean receive(JSONObject value, String user, String viewedChat) {
        if (value == null || user.isEmpty() || !user.equals(value.optString("user"))) return false;
        String chat = value.optString("chat"), kind = value.optString("kind");
        if (!chat.matches("[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}") || !validKind(kind)) return false;
        if (chat.equals(viewedChat)) return dismiss(chat);
        return !kind.equals(statuses.put(chat, kind));
    }

    static boolean validKind(String kind) { return kind.equals("completed") || kind.equals("failed") || kind.equals("approval"); }
    String kind(String chat) { return statuses.getOrDefault(chat, ""); }
    boolean dismiss(String chat) { return statuses.remove(chat) != null; }
    void clear() { statuses.clear(); }
    void copyFrom(NativeSessionNotifications source) {
        if (source == this) return;
        statuses.clear(); statuses.putAll(source.statuses);
    }
}
