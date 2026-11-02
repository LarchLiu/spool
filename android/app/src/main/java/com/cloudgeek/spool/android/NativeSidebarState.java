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

/** Session-scoped Web Sidebar state, independent of drawer mount/unmount and metadata pushes. */
final class NativeSidebarState {
    int projectLimit = 5, recentLimit = 5, scrollY;
    final Set<String> expanded = new LinkedHashSet<>();
    final Map<String, Integer> chatLimits = new LinkedHashMap<>();
    private String selectedProject, selectedChat;

    static List<JSONObject> sorted(JSONArray chats, String project) {
        List<JSONObject> result = new ArrayList<>();
        for (int i = 0; i < chats.length(); i++) { JSONObject chat = chats.optJSONObject(i); if (chat != null && (project == null || chat.optString("project_id").equals(project))) result.add(chat); }
        result.sort(Comparator.comparingDouble((JSONObject chat) -> chat.optDouble("updated_at", 0)).reversed());
        return result;
    }
    static int nextLimit(int total, int limit) { return total > limit ? Math.min(limit + 10, total) : 5; }
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
    void reset() { projectLimit = recentLimit = 5; scrollY = 0; selectedProject = selectedChat = null; expanded.clear(); chatLimits.clear(); }
}
