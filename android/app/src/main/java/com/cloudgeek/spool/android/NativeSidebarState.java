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

/** Sidebar view state plus device preferences scoped to a server and account. */
final class NativeSidebarState {
    int projectLimit = 5, recentLimit = 5, scrollY;
    final Set<String> expanded = new LinkedHashSet<>();
    final Map<String, Integer> chatLimits = new LinkedHashMap<>();
    final Set<String> pinned = new LinkedHashSet<>(), collapsed = new LinkedHashSet<>();
    private android.content.SharedPreferences preferences;
    private String storageKey;
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
        if (java.util.Objects.equals(storageKey, key)) return;
        preferences = context.getApplicationContext().getSharedPreferences("sidebar", android.content.Context.MODE_PRIVATE);
        storageKey = key; pinned.clear(); collapsed.clear();
        if (key == null) return;
        try {
            JSONObject value = new JSONObject(preferences.getString(key, "{}"));
            JSONArray ids = value.optJSONArray("pinned");
            if (ids != null) for (int i = 0; i < ids.length(); i++) {
                Object id = ids.opt(i); if (id instanceof String && !((String) id).isEmpty()) pinned.add((String) id);
            }
            JSONObject groups = value.optJSONObject("collapsed");
            for (String section : new String[] {"pinned", "projects", "recents"})
                if (groups != null && Boolean.TRUE.equals(groups.opt(section))) collapsed.add(section);
        } catch (Exception ignored) { /* Corrupt device preferences restore expanded, unpinned navigation. */ }
    }
    private void savePreferences() {
        if (preferences == null || storageKey == null) return;
        try {
            JSONObject groups = new JSONObject();
            for (String section : new String[] {"pinned", "projects", "recents"}) groups.put(section, collapsed.contains(section));
            preferences.edit().putString(storageKey, new JSONObject().put("pinned", new JSONArray(pinned)).put("collapsed", groups).toString()).apply();
        } catch (org.json.JSONException error) { throw new IllegalStateException(error); }
    }
    void togglePin(String chat) {
        if (!pinned.remove(chat)) {
            Set<String> next = new LinkedHashSet<>(); next.add(chat); next.addAll(pinned); pinned.clear(); pinned.addAll(next);
            collapsed.remove("pinned");
        }
        savePreferences();
    }
    void toggleSection(String section) { if (!collapsed.add(section)) collapsed.remove(section); savePreferences(); }
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
    void toggle(String project) { if (!expanded.add(project)) expanded.remove(project); }
    void moreProjects() { projectLimit += 5; }
    void moreChats(String project, int total) { if (project == null) recentLimit = nextLimit(total, recentLimit); else chatLimits.put(project, nextLimit(total, limit(project))); }
    void copyFrom(NativeSidebarState other) {
        projectLimit = other.projectLimit; recentLimit = other.recentLimit; scrollY = other.scrollY;
        selectedProject = other.selectedProject; selectedChat = other.selectedChat;
        expanded.clear(); expanded.addAll(other.expanded);
        chatLimits.clear(); chatLimits.putAll(other.chatLimits);
        pinned.clear(); pinned.addAll(other.pinned); collapsed.clear(); collapsed.addAll(other.collapsed);
        preferences = other.preferences; storageKey = other.storageKey;
    }
    void reset() { projectLimit = recentLimit = 5; scrollY = 0; selectedProject = selectedChat = null; expanded.clear(); chatLimits.clear(); pinned.clear(); collapsed.clear(); storageKey = null; preferences = null; }
}
