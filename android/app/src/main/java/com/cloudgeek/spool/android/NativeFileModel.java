package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

/** Session-scoped lazy tree state; search only visits entries already authorized by the server. */
final class NativeFileModel {
    static final class Entry {
        final String name, path;
        final boolean directory;
        final NativeFileOrder.Key sortKey;
        Entry(JSONObject value) {
            name = value.optString("name"); path = value.optString("path");
            directory = value.optString("kind").equals("directory");
            sortKey = new NativeFileOrder.Key(name);
        }
    }
    static final class Row {
        final Entry entry;
        final int depth;
        final boolean expanded, match;
        Row(Entry entry, int depth, boolean expanded, boolean match) { this.entry = entry; this.depth = depth; this.expanded = expanded; this.match = match; }
    }
    final Map<String, List<Entry>> directories = new HashMap<>();
    final Set<String> expanded = new LinkedHashSet<>(), requested = new LinkedHashSet<>();
    private Set<String> beforeSearch;
    final String project, chat;
    JSONObject workspace;
    JSONObject latestWorkspace;
    JSONArray workspaces = new JSONArray();
    String manualWorkspace = "";
    String workspaceError = "";
    String query = "", selected = "", focused = "", error = "", fileError = "";
    JSONObject file;
    NativeFileTarget target;
    NativeFileTarget loadedTarget;
    boolean searchOpen, truncated;
    int epoch, fileRequest;
    double split = .32;

    NativeFileModel(String project) { this(project, ""); }
    NativeFileModel(String project, String chat) { this.project = project; this.chat = chat; }

    String apiBase() { return NativeFileData.apiBase(project, chat); }
    String workspacePath() { return workspaces.length() > 0 && workspace != null ? workspace.optString("path") : ""; }
    String filePath(String path, String mode) { return NativeFileData.filePath(project, chat, path, mode, workspacePath()); }
    String directoryPath(String path) { return apiBase() + "/files?path=" + android.net.Uri.encode(path)
            + (workspacePath().isEmpty() ? "" : "&workspace=" + android.net.Uri.encode(workspacePath())); }
    boolean ready() { return chat.isEmpty() || workspace != null && workspace.optBoolean("available"); }

    void receiveWorkspace(JSONObject latest) {
        if (latestWorkspace != null && (!latestWorkspace.optString("path").equals(latest.optString("path"))
                || !latest.optString("turn_id").isEmpty() && !latestWorkspace.optString("turn_id").equals(latest.optString("turn_id")))) manualWorkspace = "";
        latestWorkspace = latest;
        workspaces = latest.optJSONArray("workspaces");
        if (workspaces == null) workspaces = new JSONArray();
        workspace = latest;
        for (int i = 0; i < workspaces.length(); i++) {
            JSONObject option = workspaces.optJSONObject(i);
            if (option != null && option.optString("path").equals(manualWorkspace)) { workspace = option; return; }
        }
        manualWorkspace = "";
    }

    boolean selectWorkspace(String path) {
        for (int i = 0; i < workspaces.length(); i++) {
            JSONObject option = workspaces.optJSONObject(i);
            if (option != null && option.optBoolean("available") && option.optString("path").equals(path)) {
                manualWorkspace = path; workspace = option; return true;
            }
        }
        return false;
    }

    static String directoryName(String path) {
        String value = path.replace('\\', '/').replaceFirst("/+$", "");
        return value.isEmpty() ? path : value.substring(value.lastIndexOf('/') + 1);
    }

    void clearScope() {
        reset(); closeSearch();
        selected = focused = fileError = "";
        file = null; target = loadedTarget = null;
    }

    static String key(String path) { return path.replaceFirst("/+$", "").isEmpty() ? "." : path.replaceFirst("/+$", ""); }

    void accept(String path, JSONObject listing) {
        List<Entry> entries = new ArrayList<>();
        JSONArray input = listing.optJSONArray("entries");
        if (input != null) for (int i = 0; i < input.length(); i++) {
            JSONObject item = input.optJSONObject(i);
            if (item == null) continue;
            Entry entry = new Entry(item);
            if (!entry.name.isEmpty() && !entry.path.isEmpty()) entries.add(entry);
        }
        entries.sort((a, b) -> {
            if (a.directory != b.directory) return a.directory ? -1 : 1;
            return NativeFileOrder.compare(a.sortKey, b.sortKey);
        });
        directories.put(key(path), entries);
        truncated |= listing.optBoolean("truncated");
        if (!query.isEmpty()) refreshSearch();
    }

    void beginSearch() {
        if (searchOpen) return;
        searchOpen = true;
        beforeSearch = new LinkedHashSet<>(expanded);
    }

    void closeSearch() {
        query = ""; searchOpen = false;
        if (beforeSearch != null) { expanded.clear(); expanded.addAll(beforeSearch); beforeSearch = null; }
    }

    void search(String input) {
        String normalized = input.trim().replace('\\', '/').toLowerCase(Locale.ROOT);
        if (query.equals(normalized)) return;
        query = normalized;
        refreshSearch();
    }

    private void refreshSearch() {
        expanded.clear();
        if (query.isEmpty()) { if (beforeSearch != null) expanded.addAll(beforeSearch); return; }
        for (List<Entry> entries : directories.values()) for (Entry entry : entries) if (matches(entry)) {
            if (entry.directory) expanded.add(entry.path);
            ancestors(entry.path, expanded);
        }
        // Pierre searches canonical leaves before generated parent directories.
        for (Row row : rows()) if (row.match && (!row.entry.directory
                || directories.getOrDefault(key(row.entry.path), List.of()).isEmpty())) {
            focused = row.entry.path;
            return;
        }
        String directory = null;
        for (List<Entry> entries : directories.values()) for (Entry entry : entries)
            if (entry.directory && matches(entry) && (directory == null || entry.path.compareTo(directory) < 0)) directory = entry.path;
        if (directory != null) focused = directory;
    }

    private boolean matches(Entry entry) { return !query.isEmpty() && (entry.path + (entry.directory ? "/" : "")).toLowerCase(Locale.ROOT).contains(query); }

    static void ancestors(String path, Set<String> into) {
        for (int slash = path.indexOf('/'); slash >= 0; slash = path.indexOf('/', slash + 1)) into.add(path.substring(0, slash));
    }

    List<Row> rows() {
        Set<String> visible = new LinkedHashSet<>();
        if (!query.isEmpty()) for (List<Entry> entries : directories.values()) for (Entry entry : entries) if (matches(entry)) {
            visible.add(entry.path); ancestors(entry.path, visible);
        }
        List<Row> rows = new ArrayList<>();
        append(".", 0, visible, rows);
        return rows;
    }

    private void append(String path, int depth, Set<String> visible, List<Row> rows) {
        if (depth > 128) return;
        List<Entry> entries = directories.get(key(path));
        if (entries == null) return;
        for (Entry entry : entries) {
            if (!visible.isEmpty() && !visible.contains(entry.path)) continue;
            boolean open = entry.directory && expanded.contains(entry.path);
            rows.add(new Row(entry, depth, open, matches(entry)));
            if (open) append(entry.path, depth + 1, visible, rows);
        }
    }

    void reset() {
        epoch++; fileRequest++;
        directories.clear(); requested.clear(); expanded.clear();
        error = ""; truncated = false;
        if (beforeSearch != null) beforeSearch.clear();
    }

    static String previewKind(JSONObject file) {
        if (file.optString("media_type").split(";", 2)[0].trim().equalsIgnoreCase("application/pdf")) return "pdf";
        if (file.optString("kind").equals("binary")) {
            if (file.optString("name").matches("(?i).*\\.docx$")) return "word";
            if (file.optString("name").matches("(?i).*\\.xlsx$")) return "spreadsheet";
        }
        if (!file.optString("kind").equals("text")) return "";
        String name = file.optString("name");
        if (name.matches("(?i).*\\.md(?:own|x)?$")) return "markdown";
        if (name.matches("(?i).*\\.html?$")) return "html";
        return file.optString("media_type").split(";", 2)[0].trim().equalsIgnoreCase("text/html") ? "html" : "";
    }

    static String size(long size) {
        if (size < 1024) return size + " B";
        if (size < 1024 * 1024) return String.format(Locale.US, "%.1f KB", size / 1024d);
        return String.format(Locale.US, "%.1f MB", size / (1024d * 1024));
    }
}
