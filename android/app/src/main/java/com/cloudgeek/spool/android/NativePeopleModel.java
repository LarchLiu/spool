package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

/** Session-only DirectInbox, including draft identity and request-scoped acknowledgements. */
final class NativePeopleModel {
    static final String ROOM = "online-room";
    static final class Draft { final String text; Draft(String text) { this.text = text; } }
    static final class Send {
        final String id, target, text; final Draft draft;
        Send(String id, String target, String text, Draft draft) { this.id = id; this.target = target; this.text = text; this.draft = draft; }
    }
    String active = "";
    JSONArray groups = new JSONArray();
    final List<JSONObject> messages = new ArrayList<>();
    final Map<String, Draft> drafts = new LinkedHashMap<>();
    final Map<String, Send> sending = new LinkedHashMap<>();
    final Map<String, Integer> unread = new LinkedHashMap<>();
    final Map<String, String> errors = new LinkedHashMap<>(), groupNames = new LinkedHashMap<>();
    final Map<String, JSONObject> remembered = new LinkedHashMap<>();

    static boolean group(String id) { return id.equals(ROOM) || id.startsWith("group-"); }
    static String badge(int count) { return count > 9 ? "9+" : String.valueOf(count); }
    static String trim(String text) { return text.replaceAll("^[\\s\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+|[\\s\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+$", ""); }
    static boolean tooLong(String text) { return text.codePointCount(0, text.length()) > 2000; }
    static String peer(JSONObject message, String user) {
        return message.optBoolean("group") || message.optString("to").equals(ROOM) ? message.optString("to")
                : message.optString("from").equals(user) ? message.optString("to") : message.optString("from");
    }
    String draft(String id) { Draft value = drafts.get(id); return value == null ? "" : value.text; }
    void draft(String id, String text) { if (text.isEmpty()) drafts.remove(id); else drafts.put(id, new Draft(text)); errors.remove(id); prune(); }
    void open(String id, JSONArray profiles) { if (!active.equals(id)) errors.remove(id); active = id; unread.put(id, 0); profiles(profiles); }
    void close() { active = ""; prune(); }
    int unreadCount() { int count = 0; for (int value : unread.values()) count += value; return count; }
    String initial(String user) {
        for (Map.Entry<String, Integer> item : unread.entrySet()) if (item.getValue() > 0) return item.getKey();
        return messages.isEmpty() ? ROOM : peer(messages.get(messages.size() - 1), user);
    }
    List<String> conversations(String user, JSONArray online) {
        Set<String> ids = new LinkedHashSet<>(); ids.add(ROOM);
        for (int i = 0; i < groups.length(); i++) ids.add(groups.optJSONObject(i).optString("id"));
        for (int i = 0; i < online.length(); i++) ids.add(online.optJSONObject(i).optString("id"));
        ids.addAll(remembered.keySet());
        for (JSONObject message : messages) ids.add(peer(message, user));
        ids.addAll(drafts.keySet()); ids.add(active); ids.remove(""); ids.remove(user);
        return new ArrayList<>(ids);
    }
    JSONObject groupInfo(String id) { for (int i = 0; i < groups.length(); i++) if (groups.optJSONObject(i).optString("id").equals(id)) return groups.optJSONObject(i); return null; }
    static String status(JSONArray online, String id) { for (int i = 0; i < online.length(); i++) if (online.optJSONObject(i).optString("id").equals(id)) return online.optJSONObject(i).optString("state"); return ""; }
    void groups(JSONArray next) {
        groups = next;
        for (int i = 0; i < next.length(); i++) groupNames.put(next.optJSONObject(i).optString("id"), next.optJSONObject(i).optString("name"));
        prune();
    }
    private Set<String> contacts() {
        Set<String> ids = new LinkedHashSet<>(); ids.add(active); ids.addAll(drafts.keySet()); ids.addAll(sending.keySet());
        for (int i = 0; i < groups.length(); i++) { JSONArray members = groups.optJSONObject(i).optJSONArray("members"); if (members != null) for (int j = 0; j < members.length(); j++) ids.add(members.optString(j)); }
        for (JSONObject message : messages) { ids.add(message.optString("from")); ids.add(message.optString("to")); }
        return ids;
    }
    private void prune() {
        Set<String> ids = contacts(); remembered.keySet().retainAll(ids); unread.keySet().retainAll(ids);
        for (int i = 0; i < groups.length(); i++) ids.add(groups.optJSONObject(i).optString("id"));
        groupNames.keySet().retainAll(ids);
    }
    void profiles(JSONArray profiles) {
        Set<String> ids = contacts(); remembered.keySet().retainAll(ids);
        for (int i = 0; i < profiles.length(); i++) { JSONObject person = profiles.optJSONObject(i); if (ids.contains(person.optString("id"))) remembered.put(person.optString("id"), person); }
    }
    Send begin(String id, String request) {
        if (sending.containsKey(id)) return null;
        Draft draft = drafts.get(id); String text = draft == null ? "" : draft.text;
        if (trim(text).isEmpty() || tooLong(text)) return null;
        Send send = new Send(request, id, text, draft); sending.put(id, send); errors.remove(id); return send;
    }
    void acknowledge(JSONObject message) {
        for (Send send : new ArrayList<>(sending.values())) if (send.id.equals(message.optString("request_id"))) {
            sending.remove(send.target);
            if (drafts.get(send.target) == send.draft) drafts.remove(send.target);
            break;
        }
    }
    boolean fail(String request, String code) {
        for (Send send : new ArrayList<>(sending.values())) if (send.id.equals(request)) { sending.remove(send.target); errors.put(send.target, code); return true; }
        return false;
    }
    void disconnected() { for (Send send : new ArrayList<>(sending.values())) fail(send.id, "unknown"); }
    void receive(JSONObject message, String user, boolean visible, JSONArray profiles) {
        if (!message.optBoolean("group") && !message.optString("to").equals(ROOM) && !message.optString("from").equals(user) && !message.optString("to").equals(user)) return;
        for (JSONObject old : messages) if (old.optString("id").equals(message.optString("id"))) return;
        messages.add(message); if (messages.size() > 300) messages.remove(0);
        String peer = peer(message, user);
        if (!message.optString("from").equals(user) && (!peer.equals(active) || !visible)) unread.put(peer, unread.getOrDefault(peer, 0) + 1);
        prune(); profiles(profiles);
    }
    List<JSONObject> history(String user) { List<JSONObject> result = new ArrayList<>(); for (JSONObject message : messages) if (peer(message, user).equals(active)) result.add(message); return result; }
    void reset() { active = ""; groups = new JSONArray(); messages.clear(); drafts.clear(); sending.clear(); unread.clear(); errors.clear(); groupNames.clear(); remembered.clear(); }
    static String errorKey(String code, boolean groupOperation) {
        switch (code) {
            case "offline": return groupOperation ? "Some selected people went offline. Update your selection and try again." : "This person is offline. Messages are not queued.";
            case "invalid_message": return "Enter a message of up to 2000 characters.";
            case "unavailable": return "Chat disconnected. Wait for reconnection before sending.";
            case "rate_limited": return "You are sending too quickly. Try again shortly.";
            case "forbidden": return "This group is no longer available to you.";
            case "invalid_group": return "Choose 1-31 other people. Groups support up to 32 members.";
            case "group_limit": return "Too many active groups. Try again after unused groups close.";
            default: return groupOperation ? "Group operation status unknown. Check your conversation list before trying again." : "Send status unknown. The message may have arrived; it will not be retried automatically.";
        }
    }
}
