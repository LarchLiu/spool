package com.cloudgeek.spool.android;

import java.util.HashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/** Revision-aware identity directory, matching Web's profile cache and save merge. */
final class NativeProfiles {
    private long revision = -1;
    private long epoch;
    JSONArray people = new JSONArray();

    boolean accept(JSONObject snapshot) {
        long next = snapshot.optLong("revision", -1);
        if (next < revision) return false;
        JSONArray values = snapshot.optJSONArray("people");
        people = values == null ? new JSONArray() : values;
        revision = next;
        epoch++;
        return true;
    }

    boolean acceptSaved(JSONObject snapshot, String owner) {
        long next = snapshot.optLong("revision", -1);
        if (next < revision) return false;
        JSONArray values = snapshot.optJSONArray("people");
        if (values == null) return false;
        Map<String, JSONObject> updates = new HashMap<>();
        for (int i = 0; i < values.length(); i++) {
            JSONObject person = values.optJSONObject(i);
            if (person != null) updates.put(person.optString("id"), person);
        }
        JSONArray merged = new JSONArray();
        boolean containsOwner = false;
        // A permission/presence push can change directory membership without a
        // new profile revision. A delayed save must not restore that old directory.
        for (int i = 0; i < people.length(); i++) {
            JSONObject person = people.optJSONObject(i);
            if (person == null) continue;
            String id = person.optString("id");
            containsOwner |= id.equals(owner);
            merged.put(updates.getOrDefault(id, person));
        }
        if (!containsOwner && updates.containsKey(owner)) merged.put(updates.get(owner));
        people = merged;
        revision = next;
        epoch++;
        return true;
    }

    void clear() {
        epoch++;
        revision = -1;
        people = new JSONArray();
    }

    void copyFrom(NativeProfiles other) {
        epoch++;
        revision = other.revision;
        people = other.people;
    }

    long epoch() { return epoch; }

    String missing(JSONArray chats, JSONObject chat, JSONArray presence) {
        java.util.Set<String> known = new java.util.HashSet<>(), ids = new java.util.TreeSet<>();
        for (int i = 0; i < people.length(); i++) {
            JSONObject person = people.optJSONObject(i); if (person != null) known.add(person.optString("id"));
        }
        for (int i = 0; i < chats.length(); i++) participants(ids, chats.optJSONObject(i));
        participants(ids, chat);
        JSONArray turns = chat == null ? null : chat.optJSONArray("turns");
        if (turns != null) for (int i = 0; i < turns.length(); i++) {
            JSONObject turn = turns.optJSONObject(i);
            if (turn == null) continue;
            String owner = turn.optString("user_id"); if (owner.isEmpty()) owner = chat.optString("user_id");
            ids.add(owner);
            JSONArray actions = turn.optJSONArray("actions");
            if (actions != null) for (int j = 0; j < actions.length(); j++) {
                JSONObject action = actions.optJSONObject(j); if (action != null) ids.add(action.optString("user_id"));
            }
        }
        for (int i = 0; i < presence.length(); i++) {
            JSONObject person = presence.optJSONObject(i); if (person != null) ids.add(person.optString("id"));
        }
        ids.remove(""); ids.remove("spool-bot"); ids.removeAll(known);
        return String.join(",", ids);
    }

    private static void participants(java.util.Set<String> ids, JSONObject chat) {
        JSONArray values = chat == null ? null : chat.optJSONArray("participants");
        if (values != null) for (int i = 0; i < values.length(); i++) {
            JSONObject person = values.optJSONObject(i); if (person != null) ids.add(person.optString("id"));
        }
    }
}
