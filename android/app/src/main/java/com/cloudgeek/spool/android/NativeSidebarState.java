package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

/** Sidebar view state plus a server-synced, locally cached account preference model. */
final class NativeSidebarState {
    int projectLimit = 5, recentLimit = 5, scrollY;
    final Set<String> expanded = new LinkedHashSet<>();
    final Map<String, Integer> chatLimits = new LinkedHashMap<>();
    final Set<String> pinned = new LinkedHashSet<>(), collapsed = new LinkedHashSet<>();
    private android.content.SharedPreferences preferences;
    private String storageKey;
    NativeSidebarPreferences sync;
    private String selectedProject, selectedChat;

    static List<JSONObject> sorted(JSONArray chats, String project) {
        List<JSONObject> result = new ArrayList<>();
        for (int i = 0; i < chats.length(); i++) { JSONObject chat = chats.optJSONObject(i); if (chat != null && (project == null || chat.optString("project_id").equals(project))) result.add(chat); }
        result.sort(Comparator.comparingDouble((JSONObject chat) -> chat.optDouble("updated_at", 0)).reversed());
        return result;
    }
    static int nextLimit(int total, int limit) { return total > limit ? Math.min(limit + 10, total) : 5; }
    void bindPreferences(android.content.Context context, String server, String user) {
        String key = user.isEmpty() ? null : new JSONArray().put(server).put(user).toString();
        if (java.util.Objects.equals(storageKey, key)) { refreshPreferences(); return; }
        preferences = context.getApplicationContext().getSharedPreferences("sidebar", android.content.Context.MODE_PRIVATE);
        storageKey = key; pinned.clear(); collapsed.clear();
        sync = null;
        if (key == null) return;
        JSONObject stored = new JSONObject();
        try {
            stored = new JSONObject(preferences.getString(key, "{}"));
        } catch (Exception ignored) { /* Corrupt device preferences restore expanded, unpinned navigation. */ }
        final android.content.SharedPreferences storage = preferences;
        sync = new NativeSidebarPreferences(stored, value -> storage.edit().putString(key, value.toString()).apply());
        refreshPreferences();
    }
    void refreshPreferences() {
        if (sync == null) return;
        JSONObject value = sync.snapshot(); pinned.clear(); collapsed.clear();
        JSONArray ids = value.optJSONArray("pinned");
        for (int i = 0; i < ids.length(); i++) pinned.add(ids.optString(i));
        JSONObject groups = value.optJSONObject("collapsed");
        for (String section : new String[] {"pinned", "projects", "recents"}) if (groups.optBoolean(section)) collapsed.add(section);
    }
    void acceptPreferences(JSONObject value) {
        if (sync != null) { sync.accept(value); refreshPreferences(); }
    }
    void togglePin(String chat) {
        if (sync != null) { sync.togglePin(chat); refreshPreferences(); return; }
        if (!pinned.remove(chat)) {
            Set<String> next = new LinkedHashSet<>(); next.add(chat); next.addAll(pinned); pinned.clear(); pinned.addAll(next);
            collapsed.remove("pinned");
        }
    }
    void toggleSection(String section) { if (sync != null) { sync.toggleSection(section); refreshPreferences(); } else if (!collapsed.add(section)) collapsed.remove(section); }
    List<JSONObject> pinnedChats(JSONArray chats) {
        Map<String, JSONObject> available = new LinkedHashMap<>();
        for (int i = 0; i < chats.length(); i++) { JSONObject chat = chats.optJSONObject(i); if (chat != null) available.put(chat.optString("id"), chat); }
        List<JSONObject> result = new ArrayList<>();
        for (String id : pinned) if (available.containsKey(id)) result.add(available.get(id));
        return result;
    }
    int limit(String project) { return chatLimits.getOrDefault(project, 5); }
    void select(String project, String chat, JSONArray projects, JSONArray chats) {
        if (project.equals(selectedProject) && chat.equals(selectedChat)) return;
        selectedProject = project; selectedChat = chat;
        if (project.isEmpty()) return;
        expanded.add(project);
        for (int i = 0; i < projects.length(); i++) if (projects.optJSONObject(i).optString("id").equals(project)) projectLimit = Math.max(projectLimit, i + 1);
        int limit = limit(project); List<JSONObject> ordered = sorted(chats, project);
        for (int i = 0; i < ordered.size(); i++) if (ordered.get(i).optString("id").equals(chat)) limit = Math.max(limit, i + 1);
        chatLimits.put(project, limit);
    }
    void toggle(String project) {
        if (!expanded.add(project)) {
            expanded.remove(project);
            chatLimits.remove(project);
        }
    }
    void moreProjects() { projectLimit += 5; }
    void moreChats(String project, int total) { if (project == null) recentLimit = nextLimit(total, recentLimit); else chatLimits.put(project, nextLimit(total, limit(project))); }
    void copyFrom(NativeSidebarState other) {
        projectLimit = other.projectLimit; recentLimit = other.recentLimit; scrollY = other.scrollY;
        selectedProject = other.selectedProject; selectedChat = other.selectedChat;
        expanded.clear(); expanded.addAll(other.expanded);
        chatLimits.clear(); chatLimits.putAll(other.chatLimits);
        pinned.clear(); pinned.addAll(other.pinned); collapsed.clear(); collapsed.addAll(other.collapsed);
        preferences = other.preferences; storageKey = other.storageKey; sync = other.sync; refreshPreferences();
    }
    void reset() { projectLimit = recentLimit = 5; scrollY = 0; selectedProject = selectedChat = null; expanded.clear(); chatLimits.clear(); pinned.clear(); collapsed.clear(); storageKey = null; preferences = null; sync = null; }
}
