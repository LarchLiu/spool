package com.cloudgeek.spool.android;

import java.util.ArrayDeque;
import java.util.LinkedHashSet;
import java.util.Set;
import java.util.function.Consumer;
import org.json.JSONArray;
import org.json.JSONObject;

/** Account-scoped sync model, shared with the cached workspace while switching servers. */
final class NativeSidebarPreferences {
    interface Writer { void send(JSONObject change, Consumer<JSONObject> done, Consumer<Exception> failed); }
    private JSONObject base, value;
    private final JSONObject initial;
    private final ArrayDeque<JSONObject> pending = new ArrayDeque<>();
    private final Set<String> collapsed = new LinkedHashSet<>();
    private boolean ready, busy;
    private Writer writer;
    private Consumer<Exception> failed = error -> {};
    private Runnable changed = () -> {};
    private final Consumer<JSONObject> cache;

    NativeSidebarPreferences(JSONObject stored, Consumer<JSONObject> cache) {
        initial = normalize(stored, 0); base = normalize(stored, -1); this.cache = cache;
        JSONObject groups = stored.optJSONObject("collapsed");
        for (String section : new String[] {"pinned", "projects", "recents"})
            if (groups != null && Boolean.TRUE.equals(groups.opt(section))) collapsed.add(section);
        emit();
    }
    static JSONObject object(Object... pairs) {
        JSONObject result = new JSONObject();
        try { for (int i = 0; i < pairs.length; i += 2) result.put((String) pairs[i], pairs[i + 1]); }
        catch (org.json.JSONException error) { throw new IllegalStateException(error); }
        return result;
    }
    private static JSONObject normalize(JSONObject value, long revision) {
        Set<String> ids = new LinkedHashSet<>();
        JSONArray pins = value.optJSONArray("pinned");
        if (pins != null) for (int i = 0; i < pins.length(); i++) {
            Object id = pins.opt(i); if (id instanceof String && !((String) id).isEmpty()) ids.add((String) id);
        }
        return object("pinned", new JSONArray(ids), "revision", revision);
    }
    JSONObject snapshot() { return value; }
    void configure(Writer writer, Runnable changed, Consumer<Exception> failed) {
        this.writer = writer; this.changed = changed; this.failed = failed; pump();
    }
    void accept(JSONObject snapshot) {
        if (snapshot == null || snapshot.optLong("revision", -1) < 0) return;
        if (!ready) {
            ready = true;
            if (snapshot.optLong("revision") == 0 && initial.optJSONArray("pinned").length() > 0)
                pending.addFirst(object("initial", initial));
        }
        if (snapshot.optLong("revision") >= base.optLong("revision")) base = normalize(snapshot, snapshot.optLong("revision"));
        emit(); pump();
    }
    void togglePin(String chat) {
        boolean pinned = false;
        JSONArray ids = value.optJSONArray("pinned");
        for (int i = 0; i < ids.length(); i++) if (chat.equals(ids.optString(i))) pinned = true;
        if (!pinned) collapsed.remove("pinned");
        change(object("chat_id", chat, "pinned", !pinned));
    }
    void toggleSection(String section) { if (!collapsed.add(section)) collapsed.remove(section); emit(); }
    private void change(JSONObject operation) { pending.add(operation); emit(); pump(); }
    private void emit() {
        value = normalize(base, base.optLong("revision"));
        for (JSONObject operation : pending) value = apply(value, operation);
        JSONObject groups = new JSONObject();
        try {
            for (String section : new String[] {"pinned", "projects", "recents"}) groups.put(section, collapsed.contains(section));
            value.put("collapsed", groups);
        } catch (org.json.JSONException error) { throw new IllegalStateException(error); }
        cache.accept(value); changed.run();
    }
    private static JSONObject apply(JSONObject old, JSONObject operation) {
        if (operation.has("initial")) return old.optLong("revision") == 0 ? normalize(operation.optJSONObject("initial"), 0) : old;
        JSONObject next = normalize(old, old.optLong("revision"));
        try {
            String chat = operation.optString("chat_id"); boolean pin = operation.optBoolean("pinned");
            JSONArray ids = new JSONArray(), previous = next.optJSONArray("pinned");
            if (pin) for (int i = 0; i < previous.length(); i++) if (chat.equals(previous.optString(i))) return next;
            if (pin) ids.put(chat);
            for (int i = 0; i < previous.length(); i++) if (!chat.equals(previous.optString(i))) ids.put(previous.optString(i));
            next.put("pinned", ids);
        } catch (org.json.JSONException error) { throw new IllegalStateException(error); }
        return next;
    }
    private void pump() {
        if (!ready || busy || writer == null || pending.isEmpty()) return;
        busy = true;
        Consumer<Exception> onFailure = failed;
        writer.send(pending.getFirst(), snapshot -> {
            pending.removeFirst();
            if (snapshot.optLong("revision", -1) >= base.optLong("revision")) base = normalize(snapshot, snapshot.optLong("revision"));
            busy = false; emit(); pump();
        }, error -> {
            pending.removeFirst(); busy = false; onFailure.accept(error); emit(); pump();
        });
    }
}
