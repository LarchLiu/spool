package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/** Web chat-state/turn-control semantics shared by optimistic sends, REST and SSE. */
final class NativeChatState {
    private NativeChatState() {}
    static JSONObject copy(JSONObject value) {
        return (JSONObject) snapshot(value);
    }
    // Copy containers, never serialize transcript bodies. Strings are immutable and shared.
    static Object snapshot(Object value) {
        if (value instanceof JSONObject) {
            JSONObject source = (JSONObject) value, result = new JSONObject();
            java.util.Iterator<String> keys = source.keys();
            while (keys.hasNext()) { String key = keys.next(); put(result, key, snapshot(source.opt(key))); }
            return result;
        }
        if (value instanceof JSONArray) {
            JSONArray source = (JSONArray) value, result = new JSONArray();
            for (int i = 0; i < source.length(); i++) result.put(snapshot(source.opt(i)));
            return result;
        }
        return value;
    }
    private static JSONObject shallow(JSONObject value) {
        if (value == null) return null;
        JSONObject result = new JSONObject(); java.util.Iterator<String> keys = value.keys();
        while (keys.hasNext()) { String key = keys.next(); put(result, key, value.opt(key)); }
        return result;
    }
    static JSONObject put(JSONObject value, String key, Object item) {
        try { value.put(key, item); return value; } catch (Exception error) { throw new IllegalArgumentException(error); }
    }
    private static JSONArray array(JSONObject value, String key) {
        JSONArray items = value == null ? null : value.optJSONArray(key); return items == null ? new JSONArray() : items;
    }
    private static JSONObject part(JSONArray values, int index) {
        JSONObject value = values.optJSONObject(index); return value == null ? new JSONObject() : value;
    }
    private static JSONArray slice(JSONArray values, int start, int end) {
        JSONArray result = new JSONArray();
        for (int i = Math.max(0, start); i < Math.min(end, values.length()); i++) result.put(values.opt(i));
        return result;
    }
    static JSONObject runningTurn(JSONObject chat) {
        JSONArray turns = array(chat, "turns");
        JSONObject turn = turns.optJSONObject(turns.length() - 1);
        return chat != null && chat.optString("status").equals("running") && turn != null && turn.optString("status").equals("running") ? turn : null;
    }

    static boolean hasRunningTurn(JSONObject chat) {
        JSONArray turns = array(chat, "turns");
        for (int i = 0; i < turns.length(); i++) if (part(turns, i).optString("status").equals("running")) return true;
        return false;
    }

    static boolean equivalent(Object left, Object right) {
        if (left == right) return true;
        if (left == null || right == null) return false;
        if (left instanceof JSONObject && right instanceof JSONObject) {
            JSONObject a = (JSONObject) left, b = (JSONObject) right;
            if (a.length() != b.length()) return false;
            java.util.Iterator<String> keys = a.keys();
            while (keys.hasNext()) { String key = keys.next(); if (!b.has(key) || !equivalent(a.opt(key), b.opt(key))) return false; }
            return true;
        }
        if (left instanceof JSONArray && right instanceof JSONArray) {
            JSONArray a = (JSONArray) left, b = (JSONArray) right;
            if (a.length() != b.length()) return false;
            for (int i = 0; i < a.length(); i++) if (!equivalent(a.opt(i), b.opt(i))) return false;
            return true;
        }
        if (left instanceof Number && right instanceof Number) return ((Number) left).doubleValue() == ((Number) right).doubleValue();
        return left.equals(right);
    }

    static JSONArray reconcileList(JSONArray chats, JSONObject next) {
        JSONArray result = new JSONArray();
        for (int i = 0; i < chats.length(); i++) {
            JSONObject item = part(chats, i);
            result.put(item.optString("id").equals(next.optString("id")) ? reconcile(item, next) : item);
        }
        return result;
    }

    private static void nullable(JSONObject target, JSONObject source, String key) { put(target, key, source.opt(key)); }
    private static Object coalesce(JSONObject value, String key, Object fallback) {
        Object item = value.opt(key); return item == null || item == JSONObject.NULL ? fallback : item;
    }

    static JSONObject metadata(JSONObject previous, JSONObject next) {
        if (previous == null || !previous.optString("id").equals(next.optString("id")) || previous.optDouble("updated_at") > next.optDouble("updated_at")) return previous;
        JSONObject result = shallow(previous);
        for (String key : new String[] {"title", "participants", "can_control"}) put(result, key, coalesce(next, key, previous.opt(key)));
        nullable(result, next, "updated_at");
        put(result, "automation_chat", coalesce(next, "automation_chat", false));
        for (String key : new String[] {"collaborator_ids", "artifacts"}) put(result, key, coalesce(next, key, new JSONArray()));
        return result;
    }

    static JSONObject event(JSONObject previous, String kind, JSONObject data) {
        if (kind.equals("snapshot")) return reconcile(previous, data);
        if (kind.equals("metadata")) return metadata(previous, data);
        if (!kind.equals("turn") || previous == null) return previous;
        JSONObject next = shallow(previous);
        for (String key : new String[] {"title", "backend", "model", "reasoning_effort", "permission_mode", "status", "transcript", "history", "updated_at"}) nullable(next, data, key);
        JSONArray supplied = data.optJSONArray("turns");
        JSONArray turns = supplied == null ? null : slice(supplied, 0, supplied.length());
        if (turns == null) {
            turns = new JSONArray();
            for (int i = 0; i < array(previous, "turns").length(); i++) {
                JSONObject turn = part(array(previous, "turns"), i);
                if (!turn.optString("id").startsWith("pending-")) turns.put(turn);
            }
        }
        JSONObject incoming = data.optJSONObject("turn");
        if (incoming == null) return previous;
        int index = -1;
        for (int i = 0; i < turns.length(); i++) if (part(turns, i).optString("id").equals(incoming.optString("id"))) index = i;
        if (index < 0) turns.put(incoming);
        else try { turns.put(index, incoming); } catch (Exception error) { throw new IllegalArgumentException(error); }
        put(next, "turns", turns);
        return reconcile(previous, next);
    }

    static JSONArray eventList(JSONArray chats, String chatID, String kind, JSONObject data) {
        JSONArray result = new JSONArray();
        for (int i = 0; i < chats.length(); i++) {
            JSONObject previous = part(chats, i), next = previous;
            if (kind.equals("metadata")) next = metadata(previous, data);
            else if (kind.equals("snapshot") && previous.optString("id").equals(data.optString("id")) && previous.optDouble("updated_at") <= data.optDouble("updated_at")) {
                next = shallow(previous);
                for (String key : new String[] {"title", "participants"}) nullable(next, data, key);
            } else if (kind.equals("turn") && previous.optString("id").equals(chatID)) {
                next = shallow(previous);
                for (String key : new String[] {"title", "backend", "model", "reasoning_effort", "status", "updated_at"}) nullable(next, data, key);
            }
            result.put(next);
        }
        return result;
    }

    static JSONArray input(String message, JSONArray attachments) {
        JSONArray parts = new JSONArray(); String text = NativeGoalCommand.trim(message);
        if (!text.isEmpty()) parts.put(put(put(put(new JSONObject(), "role", "user"), "kind", "text"), "text", text));
        for (int i = 0; i < attachments.length(); i++) {
            JSONObject file = part(attachments, i), item = new JSONObject(); boolean image = file.optString("media_type").startsWith("image/");
            put(item, "role", "user"); put(item, "kind", image ? "image" : "context"); put(item, "name", file.optString("name"));
            put(item, "text", image ? "[image]" : file.optString("name")); parts.put(item);
        }
        return parts;
    }

    static JSONObject optimistic(JSONObject chat, String project, JSONObject turn, JSONArray input) {
        JSONObject next = chat == null ? new JSONObject() : shallow(chat);
        if (chat == null) {
            put(next, "id", turn.optString("id")); put(next, "project_id", project); put(next, "title", "New chat"); put(next, "updated_at", 0);
        }
        JSONArray existingParts = array(next.optJSONObject("transcript"), "parts");
        JSONArray parts = slice(existingParts, 0, existingParts.length());
        int start = parts.length();
        JSONObject pending = shallow(turn);
        put(pending, "part_start", start); put(pending, "input_end", start + input.length()); put(pending, "part_end", start + input.length());
        for (int i = 0; i < input.length(); i++) parts.put(input.opt(i));
        JSONArray existingTurns = array(next, "turns");
        JSONArray turns = slice(existingTurns, 0, existingTurns.length()); turns.put(pending);
        put(next, "backend", pending.optString("backend", chat == null ? "codex" : chat.optString("backend", "codex")));
        put(next, "model", pending.optString("requested_model")); put(next, "reasoning_effort", pending.optString("reasoning_effort"));
        put(next, "permission_mode", pending.optString("permission_mode")); put(next, "status", "running");
        put(next, "transcript", put(new JSONObject(), "parts", parts)); put(next, "turns", turns);
        return next;
    }

    static JSONObject adoptCreated(JSONObject pending, JSONObject created) {
        JSONObject previous = shallow(pending), next = shallow(created);
        put(previous, "id", created.optString("id")); put(previous, "updated_at", created.optLong("updated_at"));
        for (String key : new String[] {"model", "reasoning_effort", "permission_mode"}) put(next, key, pending.opt(key));
        return reconcile(previous, next);
    }

    static JSONObject reconcile(JSONObject previous, JSONObject next) {
        if (previous == null || !previous.optString("id").equals(next.optString("id"))) return next;
        if (previous.optDouble("updated_at") > next.optDouble("updated_at")) return previous;
        return mergeTurns(previous, next, false);
    }

    static JSONObject prepend(JSONObject previous, JSONObject page) {
        if (previous == null || !previous.optString("id").equals(page.optString("id"))) return previous;
        return mergeTurns(previous, page, true);
    }

    private static JSONArray mergeParts(JSONArray old, JSONArray next) {
        if (old.length() > next.length()) return old;
        JSONArray parts = slice(next, 0, next.length());
        for (int i = 0; i < old.length(); i++) {
            JSONObject before = part(old, i), after = part(parts, i);
            if (equivalent(before, after)) {
                try { parts.put(i, before); } catch (Exception error) { throw new IllegalArgumentException(error); }
                continue;
            }
            boolean same = true;
            for (String key : new String[] {"role", "kind", "name", "model", "id"}) same &= before.optString(key).equals(after.optString(key));
            if (!same) continue;
            after = shallow(after);
            if (!before.optString("phase").isEmpty() && after.optString("phase").isEmpty()) put(after, "phase", before.opt("phase"));
            if (before.optString("text").length() > after.optString("text").length()) {
                put(after, "text", before.optString("text")); put(after, "cut", before.opt("cut"));
            }
            try { parts.put(i, after); } catch (Exception error) { throw new IllegalArgumentException(error); }
        }
        return parts;
    }

    private static JSONObject mergeTurn(JSONObject before, JSONObject after) {
        if (before == null) return after;
        if (!before.optString("status").equals("running") && after.optString("status").equals("running")) return before;
        if (before.optInt("part_end") > before.optInt("part_start") && after.optInt("part_end") == after.optInt("part_start")) {
            JSONObject result = shallow(after);
            for (String key : new String[] {"part_start", "input_end", "part_end"}) put(result, key, before.opt(key));
            return result;
        }
        return after;
    }

    private static final class Entry {
        final JSONObject turn; final JSONArray parts;
        Entry(JSONObject turn, JSONArray parts) { this.turn = turn; this.parts = parts; }
    }

    private static JSONObject mergeTurns(JSONObject previous, JSONObject next, boolean older) {
        Map<String, Entry> entries = new LinkedHashMap<>(); Map<String, String> aliases = new LinkedHashMap<>(), renamed = new LinkedHashMap<>();
        JSONArray oldTurns = array(previous, "turns"), incoming = array(next, "turns");
        JSONArray oldParts = array(previous.optJSONObject("transcript"), "parts"), incomingParts = array(next.optJSONObject("transcript"), "parts");
        for (int i = 0; i < oldTurns.length(); i++) {
            JSONObject turn = part(oldTurns, i); String id = turn.optString("id");
            entries.put(id, new Entry(turn, slice(oldParts, turn.optInt("part_start"), turn.optInt("part_end"))));
            if (!turn.optString("native_turn_id").isEmpty()) aliases.put(turn.optString("native_turn_id"), id);
        }
        List<String> incomingIDs = new ArrayList<>(); boolean newServerTurn = false;
        JSONObject transcript = next.optJSONObject("transcript");
        boolean nativeSource = transcript != null && transcript.optString("source").endsWith("-native");
        for (int i = 0; i < incoming.length(); i++) {
            JSONObject turn = part(incoming, i); String id = turn.optString("id"); incomingIDs.add(id);
            String oldID = entries.containsKey(id) ? id : aliases.get(turn.optString("native_turn_id"));
            Entry old = oldID == null ? null : entries.get(oldID);
            if (old == null && !id.startsWith("pending-")) newServerTurn = true;
            if (oldID != null && !oldID.equals(id)) { entries.remove(oldID); renamed.put(oldID, id); }
            JSONArray parts = slice(incomingParts, turn.optInt("part_start"), turn.optInt("part_end"));
            boolean complete = !turn.optString("status").equals("running") && nativeSource && parts.length() > 0;
            if (old != null && !complete) parts = mergeParts(old.parts, parts);
            int inputs = old != null && parts == old.parts ? old.turn.optInt("input_end") - old.turn.optInt("part_start") : turn.optInt("input_end") - turn.optInt("part_start");
            JSONObject before = old == null ? null : put(shallow(old.turn), "id", id);
            JSONObject merged = shallow(mergeTurn(before, turn)); put(merged, "part_start", 0); put(merged, "input_end", inputs);
            entries.put(id, new Entry(merged, parts));
        }
        JSONObject history = next.optJSONObject("history");
        boolean nativeSnapshot = !older && nativeSource && (history == null || history.optString("error").isEmpty() && !history.optBoolean("has_more"));
        java.util.Set<String> incomingSet = new java.util.HashSet<>(incomingIDs);
        List<String> missing = new ArrayList<>();
        for (int i = 0; i < oldTurns.length(); i++) {
            String id = part(oldTurns, i).optString("id"); id = renamed.containsKey(id) ? renamed.get(id) : id;
            if (incomingSet.contains(id)) continue;
            JSONObject turn = entries.get(id).turn;
            boolean live = id.startsWith("pending-") || turn.optString("status").equals("running");
            if (older || (live ? !newServerTurn : !nativeSnapshot)) missing.add(id);
        }
        List<String> order = new ArrayList<>();
        if (older) { order.addAll(incomingIDs); order.addAll(missing); }
        else {
            for (String id : missing) if (!id.startsWith("pending-") && !entries.get(id).turn.optString("status").equals("running")) order.add(id);
            order.addAll(incomingIDs);
            for (String id : missing) if (id.startsWith("pending-") || entries.get(id).turn.optString("status").equals("running")) order.add(id);
        }
        JSONArray parts = new JSONArray(), turns = new JSONArray();
        for (String id : new LinkedHashSet<>(order)) {
            Entry entry = entries.get(id); int start = parts.length();
            int inputs = Math.min(entry.parts.length(), entry.turn.optInt("input_end") - entry.turn.optInt("part_start"));
            for (int i = 0; i < entry.parts.length(); i++) parts.put(entry.parts.opt(i));
            JSONObject turn = shallow(entry.turn); put(turn, "part_start", start); put(turn, "input_end", start + inputs); put(turn, "part_end", parts.length()); turns.put(turn);
        }
        JSONObject result = shallow(older ? previous : next);
        if (older) put(result, "history", next.opt("history"));
        else if (!part(turns, 0).optString("id").equals(part(incoming, 0).optString("id"))) {
            JSONObject retained = previous.optJSONObject("history") == null ? new JSONObject() : shallow(previous.optJSONObject("history"));
            put(retained, "error", history == null ? null : history.opt("error")); put(result, "history", retained);
        }
        put(result, "turns", turns);
        JSONObject base = older ? previous.optJSONObject("transcript") : transcript;
        put(result, "transcript", put(base == null ? new JSONObject() : shallow(base), "parts", parts));
        if (turns.length() > 0) put(result, "status", part(turns, turns.length() - 1).optString("status"));
        return result;
    }
}
