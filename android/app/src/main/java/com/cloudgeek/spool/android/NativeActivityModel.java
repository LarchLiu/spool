package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.HashMap;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.json.JSONObject;

/** Web tool-activity.tsx's classification, call-ID result pairing, grouping and change statistics. */
final class NativeActivityModel {
    static final class Activity {
        JSONObject use, result;
        String kind, label, detail, stats;
        boolean noDetails;
        final List<String> images = new ArrayList<>(), imageURLs = new ArrayList<>();
    }
    static final class Group {
        String kind, label;
        boolean compound;
        final List<Activity> activities = new ArrayList<>();
    }
    private static final Pattern PATCH_FILE = Pattern.compile("^\\*\\*\\* (Add|Update|Delete) File: (.+)\\r?$", Pattern.MULTILINE);
    private static final Pattern PATCH_START = Pattern.compile("(?:^|\\n)(?:diff --git |--- [^\\n]+\\n\\+\\+\\+ |\\*\\*\\* Begin Patch)");
    private static final Pattern STATS = Pattern.compile("(?:^|\\s)(\\+\\d+)\\s+(-\\d+)\\s*$");

    static List<Activity> fromParts(NativeUi ui, List<JSONObject> parts, String backend) {
        List<Activity> activities = new ArrayList<>();
        Map<String, Activity> calls = new HashMap<>();
        for (JSONObject part : parts) {
            if (part.optString("role").equals("user")) continue;
            String kind = part.optString("kind"), text = part.optString("text");
            if (part.optString("role").equals("assistant") && kind.equals("text")) {
                if (!text.trim().isEmpty()) {
                    Activity item = new Activity();
                    item.use = part;
                    item.kind = "message";
                    item.label = "";
                    item.detail = text;
                    activities.add(item);
                }
            } else if (kind.equals("tool_result")) {
                String id = part.optString("id");
                Activity last = id.isEmpty() ? null : calls.get(id);
                if (id.isEmpty() && !activities.isEmpty()) {
                    Activity candidate = activities.get(activities.size() - 1);
                    if (candidate.use != null && candidate.use.optString("kind").equals("tool_use") && candidate.result == null) last = candidate;
                }
                JSONObject summary = part.optJSONObject("tool");
                List<String> urls = strings(summary == null ? part.optJSONArray("images") : summary.optJSONArray("image_urls"));
                if (last != null && last.result == null) {
                    last.result = part;
                    if (!urls.isEmpty()) { last.kind = "image"; last.imageURLs.addAll(urls); last.label = imageLabel(ui, urls.size()); }
                } else {
                    Activity item = new Activity();
                    item.kind = urls.isEmpty() ? "other" : "image";
                    item.imageURLs.addAll(urls);
                    item.label = urls.isEmpty() ? ui.t("Tool result") : imageLabel(ui, urls.size());
                    item.detail = "";
                    item.result = part;
                    activities.add(item);
                }
            } else if (kind.equals("tool_use") || kind.equals("thinking")) {
                Activity activity = display(ui, part, backend); activities.add(activity);
                if (kind.equals("tool_use") && !part.optString("id").isEmpty()) calls.put(part.optString("id"), activity);
            }
        }
        List<Activity> expanded = new ArrayList<>();
        for (Activity activity : activities) {
            JSONObject part = activity.use;
            JSONObject summary = part == null ? null : part.optJSONObject("tool");
            org.json.JSONArray operations = null;
            if (part != null && part.optString("kind").equals("tool_use") && !activity.kind.equals("image")) {
                if (summary != null) operations = summary.optJSONArray("operations");
                else if (NativeToolKind.nameKind(part.optString("name")).equals("shell"))
                    operations = NativeToolKind.operations(NativeToolKind.command(part.optString("text"), payload(part.optString("text"))), 0);
            }
            if (operations == null || operations.length() == 0) { expanded.add(activity); continue; }
            for (int i = 0; i < operations.length(); i++) {
                JSONObject operation = operations.optJSONObject(i);
                if (operation == null) continue;
                try {
                    JSONObject synthetic = new JSONObject().put("kind", "tool_use").put("name", part.optString("name")).put("tool", operation);
                    Activity item = display(ui, synthetic, backend);
                    if (operation.optString("path").isEmpty()) item.label += " \u00b7 " + oneLine(operation.optString("command"), 180);
                    item.use = part; item.result = activity.result; item.detail = ""; item.stats = ""; item.noDetails = true;
                    expanded.add(item);
                } catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
            }
        }
        return expanded;
    }

    static Activity display(NativeUi ui, JSONObject part, String backend) {
        Activity item = new Activity();
        item.use = part;
        JSONObject summary = part.optJSONObject("tool");
        item.images.addAll(summary == null ? imagePaths(part.optString("name"), payload(part.optString("text"))) : strings(summary.optJSONArray("image_paths")));
        item.kind = !item.images.isEmpty() ? "image" : summary == null ? classify(part) : summary.optString("kind", "other");
        item.detail = part.optString("text");
        JSONObject data = summary == null ? payload(item.detail) : null;
        String path = summary == null ? path(part, data) : summary.optString("path"),
                query = summary == null ? field(data, "pattern", "query", "search_query", "searchTerm", "glob", "include") : summary.optString("query");
        String name = part.optString("name").trim();
        if (name.isEmpty()) name = backend.equals("claude") ? "Claude tool" : "Codex tool";
        switch (item.kind) {
            case "image": item.label = imageLabel(ui, item.images.size()); break;
            case "read": item.label = path.isEmpty() ? ui.t("Read files") : ui.t("Read {path}", "path", shortPath(path)); break;
            case "write": item.label = path.isEmpty() ? ui.t("Edited files") : ui.t("Edited {path}", "path", shortPath(path)); break;
            case "create": item.label = path.isEmpty() ? ui.t("Created files") : ui.t("Created {path}", "path", shortPath(path)); break;
            case "delete": item.label = path.isEmpty() ? ui.t("Deleted files") : ui.t("Deleted {path}", "path", shortPath(path)); break;
            case "search": item.label = query.isEmpty() ? path.isEmpty() ? ui.t("Searched files") : ui.t("Searched files") + " \u00b7 " + shortPath(path) : path.isEmpty()
                    ? ui.t("Searched for {query}", "query", oneLine(query, 80))
                    : ui.t("Searched for {query} in {path}", "query", oneLine(query, 80), "path", shortPath(path)); break;
            case "list": item.label = path.isEmpty() ? ui.t("Listed files") : ui.t("Listed files in {path}", "path", shortPath(path)); break;
            case "web": item.label = query.isEmpty() ? ui.t("Searched the web") : ui.t("Searched web for {query}", "query", oneLine(query, 80)); break;
            case "shell": item.label = ui.t("Ran command"); break;
            case "thinking": item.label = ui.t("Think"); break;
            default:
                name = name.replaceAll("[_-]+", " ").trim();
                name = name.isEmpty() ? name : name.substring(0, 1).toUpperCase(Locale.ROOT) + name.substring(1);
                item.label = name.isEmpty() ? ui.t("Used tool") : ui.t("Used {tool}", "tool", name);
        }
        Matcher stats = STATS.matcher(item.detail);
        item.stats = summary != null ? summary.optString("stats") : stats.find() ? stats.group(1) + " " + stats.group(2) : "";
        return item;
    }

    private static String classify(JSONObject part) {
        String tool = part.optString("name").trim().toLowerCase(Locale.ROOT).replace("__", ".");
        if (part.optString("kind").equals("thinking")) return "thinking";
        String kind = NativeToolKind.nameKind(tool);
        tool = tool.substring(tool.lastIndexOf('.') + 1);
        if (tool.matches("apply_?patch|patch|file_?change")) {
            List<String[]> files = patchFiles(part);
            if (!files.isEmpty() && files.stream().allMatch(file -> file[0].equals("Add"))) return "create";
            if (!files.isEmpty() && files.stream().allMatch(file -> file[0].equals("Delete"))) return "delete";
            return "write";
        }
        return kind.equals("shell") ? NativeToolKind.shellKind(NativeToolKind.command(part.optString("text"), payload(part.optString("text"))), 0) : kind;
    }

    private static boolean has(String pattern, String value) { return Pattern.compile(pattern).matcher(value).find(); }

    private static List<String> strings(org.json.JSONArray values) {
        List<String> out = new ArrayList<>();
        if (values != null) for (int i = 0; i < values.length(); i++) if (values.opt(i) instanceof String) out.add(values.optString(i));
        return out;
    }

    private static List<String> imagePaths(String name, JSONObject data) {
        List<String> paths = new ArrayList<>();
        if (data == null) return paths;
        name = name.trim().toLowerCase(Locale.ROOT);
        boolean imageTool = name.matches("(?:.*(?:\\.|__))?(?:view_?image|image_?view)");
        if (imageTool || name.equals("read")) {
            String path = field(data, "path", "file_path", "image_path");
            if (!path.isEmpty() && path.length() <= 4096 && !has("[\\x00\\r\\n]", path)
                    && (!name.equals("read") || path.toLowerCase(Locale.ROOT).matches(".*\\.(png|jpe?g|gif|webp)$"))) paths.add(path);
        } else if (name.equals("multi_tool_use.parallel")) {
            org.json.JSONArray calls = data.optJSONArray("tool_uses");
            if (calls == null || calls.length() > 64) return paths;
            for (int i = 0; i < calls.length(); i++) {
                JSONObject call = calls.optJSONObject(i);
                if (call == null || !call.optString("recipient_name").toLowerCase(Locale.ROOT).matches("(?:.*(?:\\.|__))?(?:view_?image|image_?view)")) return new ArrayList<>();
                List<String> child = imagePaths(call.optString("recipient_name"), call.optJSONObject("parameters"));
                if (child.isEmpty()) return new ArrayList<>();
                paths.addAll(child);
            }
        }
        return paths;
    }

    static String imageLabel(NativeUi ui, int count) { return count == 1 ? ui.t("Viewed 1 image") : ui.t("Viewed {count} images", "count", count); }

    private static List<String[]> patchFiles(JSONObject part) {
        List<String[]> files = new ArrayList<>();
        if (!has("(?:^|\\.|__)(?:apply_?patch|patch|file_?change)$", part.optString("name").trim().toLowerCase(Locale.ROOT))) return files;
        Matcher matcher = PATCH_FILE.matcher(part.optString("text"));
        while (matcher.find()) files.add(new String[] {matcher.group(1), matcher.group(2).trim()});
        return files;
    }

    private static JSONObject payload(String text) {
        String value = text.trim();
        if (!value.startsWith("{") || !value.endsWith("}")) return null;
        try { return new JSONObject(value); } catch (org.json.JSONException ignored) { return null; }
    }

    private static String field(JSONObject data, String... keys) {
        if (data != null) for (String key : keys) {
            Object value = data.opt(key);
            if (value instanceof String && !((String) value).trim().isEmpty()) return ((String) value).trim();
        }
        return "";
    }

    private static String path(JSONObject part, JSONObject data) {
        String value = field(data, "file_path", "path", "file", "filename", "target_file", "notebook_path");
        if (!value.isEmpty()) return value;
        List<String[]> files = patchFiles(part);
        if (files.size() == 1) return files.get(0)[1];
        if (files.size() > 1) return "";
        if (NativeToolKind.nameKind(part.optString("name")).equals("shell")) {
            String command = NativeToolKind.command(part.optString("text"), data);
            return NativeToolKind.shellKind(command, 0).equals("search") ? NativeToolKind.searchPath(command, 0) : "";
        }
        Matcher matcher = Pattern.compile("(?:^|\\s)([\\w./~-]+\\.[\\w-]+)(?:\\s|$)").matcher(part.optString("text"));
        return matcher.find() ? matcher.group(1) : "";
    }

    private static String shortPath(String value) {
        List<String> pieces = new ArrayList<>();
        for (String piece : value.replace('\\', '/').split("/")) if (!piece.isEmpty()) pieces.add(piece);
        return pieces.size() > 3 ? "\u2026/" + String.join("/", pieces.subList(pieces.size() - 3, pieces.size())) : value;
    }

    private static String oneLine(String value, int limit) {
        String text = value.replaceAll("\\s+", " ").trim();
        return text.length() > limit ? text.substring(0, limit - 1) + "\u2026" : text;
    }

    static List<Group> groups(NativeUi ui, List<Activity> activities) {
        List<Group> groups = new ArrayList<>();
        for (Activity activity : activities) {
            Group last = groups.isEmpty() ? null : groups.get(groups.size() - 1);
            if (activity.noDetails) {
                if (last != null && last.compound && last.activities.get(0).use == activity.use) {
                    last.activities.add(activity); last.label = compoundLabel(ui, last.activities);
                } else {
                    Group group = new Group(); group.compound = true; group.kind = activity.kind;
                    group.activities.add(activity); group.label = compoundLabel(ui, group.activities); groups.add(group);
                }
                continue;
            }
            if (last != null && !last.compound && last.kind.equals(activity.kind) && !activity.kind.equals("message")) {
                last.activities.add(activity);
                last.label = groupLabel(ui, last.kind);
            } else {
                Group group = new Group();
                group.kind = activity.kind;
                group.label = activity.label;
                group.activities.add(activity);
                groups.add(group);
            }
        }
        for (Group group : groups) if (group.kind.equals("image")) {
            int count = 0;
            for (Activity activity : group.activities) count += activity.imageURLs.isEmpty() ? activity.images.size() : activity.imageURLs.size();
            group.label = imageLabel(ui, count);
        }
        return groups;
    }

    private static String compoundLabel(NativeUi ui, List<Activity> activities) {
        Map<String, Integer> counts = new java.util.LinkedHashMap<>();
        for (Activity activity : activities) counts.put(activity.kind, counts.getOrDefault(activity.kind, 0) + 1);
        String label = "";
        for (Map.Entry<String, Integer> entry : counts.entrySet()) {
            String next = entry.getKey().equals("shell") && entry.getValue() == 1 ? ui.t("Ran command") : groupLabel(ui, entry.getKey());
            label = label.isEmpty() ? next : ui.t("{previous}, {next}", "previous", label, "next", next.substring(0, 1).toLowerCase(Locale.ROOT) + next.substring(1));
        }
        return label;
    }

    private static String groupLabel(NativeUi ui, String kind) {
        switch (kind) {
            case "read": return ui.t("Read files");
            case "write": return ui.t("Edited files");
            case "create": return ui.t("Created files");
            case "delete": return ui.t("Deleted files");
            case "search": return ui.t("Searched files");
            case "list": return ui.t("Listed files");
            case "web": return ui.t("Searched the web");
            case "shell": return ui.t("Ran commands");
            case "thinking": return ui.t("Thought through tasks");
            default: return ui.t("Used tools");
        }
    }

    static String icon(String kind) {
        switch (kind) {
            case "image": return "image";
            case "read": return "book-open";
            case "write": return "file-pen";
            case "create": return "file-plus";
            case "delete": return "file-x";
            case "search": return "search";
            case "list": return "list";
            case "web": return "globe";
            case "shell": return "terminal";
            case "thinking": return "brain";
            default: return "wrench";
        }
    }

    static String patchText(String value) {
        Matcher matcher = PATCH_START.matcher(value);
        if (!matcher.find()) return "";
        String patch = value.substring(matcher.start()).replaceFirst("^\\n", "");
        return patch.contains("@@") || patch.contains("*** Begin Patch") ? patch : "";
    }

    static String changeStats(List<Activity> activities) {
        long additions = 0, deletions = 0;
        for (Activity activity : activities) {
            if (!activity.kind.matches("write|create|delete")) return "";
            JSONObject resultSummary = activity.result == null ? null : activity.result.optJSONObject("tool");
            JSONObject useSummary = activity.use == null ? null : activity.use.optJSONObject("tool");
            JSONObject changes = resultSummary == null ? null : resultSummary.optJSONObject("changes");
            if (changes == null && useSummary != null) changes = useSummary.optJSONObject("changes");
            if (changes != null) { additions += changes.optInt("additions"); deletions += changes.optInt("deletions"); continue; }
            String patch = patchText(activity.result == null ? "" : activity.result.optString("text"));
            if (patch.isEmpty()) patch = patchText(activity.use == null ? "" : activity.use.optString("text"));
            if (!patch.isEmpty()) {
                for (String line : patch.split("\\r?\\n", -1)) {
                    if (line.startsWith("+++") || line.startsWith("---")) continue;
                    if (line.startsWith("+")) additions++;
                    else if (line.startsWith("-")) deletions++;
                }
            } else if (activity.stats != null && activity.stats.matches("\\+\\d+\\s+-\\d+")) {
                String[] counts = activity.stats.split("\\s+");
                try {
                    additions += Long.parseLong(counts[0].substring(1));
                    deletions += Long.parseLong(counts[1].substring(1));
                } catch (NumberFormatException ignored) {
                    // Malformed tool text must remain readable, not crash the transcript.
                }
            }
        }
        return additions != 0 || deletions != 0 ? "+" + additions + " -" + deletions : "";
    }
}
