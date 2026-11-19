package com.cloudgeek.spool.android;

import java.text.Collator;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONObject;

/** The same catalog, parsing and suggestion order as Web slash-commands.ts. */
final class NativeSlashCommand {
    final String name, displayName, description, argumentHint, kind, scope, pluginId;
    final List<String> aliases = new ArrayList<>();

    NativeSlashCommand(JSONObject value) {
        name = value.optString("name"); displayName = value.optString("displayName");
        description = value.optString("description"); argumentHint = value.optString("argumentHint");
        kind = value.optString("kind"); scope = value.optString("scope"); pluginId = value.optString("pluginId");
        JSONArray values = value.optJSONArray("aliases");
        if (values != null) for (int i = 0; i < values.length(); i++) aliases.add(values.optString(i));
    }

    static List<NativeSlashCommand> catalog(JSONArray values) {
        List<NativeSlashCommand> commands = new ArrayList<>();
        if (values != null) for (int i = 0; i < values.length(); i++) {
            JSONObject value = values.optJSONObject(i);
            if (value != null && !value.optString("name").isEmpty()) commands.add(new NativeSlashCommand(value));
        }
        return commands;
    }

    static final class Input {
        final String name, arguments;
        Input(String name, String arguments) { this.name = name; this.arguments = arguments; }
    }

    static Input parse(String message) {
        String value = NativeGoalCommand.trim(message);
        if (!value.startsWith("/")) return null;
        int end = 1;
        while (end < value.length() && !NativeGoalCommand.space(value.charAt(end))) end++;
        String name = value.substring(1, end);
        if (name.isEmpty() || name.indexOf('/') >= 0 || name.indexOf('\\') >= 0 || name.indexOf('`') >= 0 || name.indexOf('"') >= 0) return null;
        return new Input(name, NativeGoalCommand.trim(value.substring(end)));
    }

    static NativeSlashCommand find(List<NativeSlashCommand> commands, String name) {
        for (NativeSlashCommand command : commands) if (command.name.equals(name)) return command;
        for (NativeSlashCommand command : commands) if (command.aliases.contains(name)) return command;
        return null;
    }

    static List<NativeSlashCommand> suggestions(String message, List<NativeSlashCommand> commands) {
        if (!message.startsWith("/")) return null;
        for (int i = 1; i < message.length(); i++) if (NativeGoalCommand.space(message.charAt(i)) || message.charAt(i) == '/' || message.charAt(i) == '\\') return null;
        String query = message.substring(1).toLowerCase(Locale.ROOT);
        List<NativeSlashCommand> result = new ArrayList<>();
        for (NativeSlashCommand command : commands) {
            boolean matches = command.name.toLowerCase(Locale.ROOT).contains(query) || command.displayName.toLowerCase(Locale.ROOT).contains(query)
                    || command.description.toLowerCase(Locale.ROOT).contains(query);
            for (String alias : command.aliases) matches |= alias.toLowerCase(Locale.ROOT).contains(query);
            if (matches) result.add(command);
        }
        Collator order = Collator.getInstance(Locale.ENGLISH);
        result.sort((first, second) -> {
            int group = Boolean.compare(first.skill(), second.skill());
            if (group != 0) return group;
            int rank = Integer.compare(first.rank(query), second.rank(query));
            if (rank != 0) return rank;
            int label = order.compare(first.displayName.isEmpty() ? first.name : first.displayName, second.displayName.isEmpty() ? second.name : second.displayName);
            return label != 0 ? label : order.compare(first.name, second.name);
        });
        return result;
    }

    boolean skill() { return kind.equals("skill"); }
    private int rank(String query) {
        if (name.startsWith(query)) return 0;
        for (String alias : aliases) if (alias.startsWith(query)) return 1;
        return 2;
    }

    String title(NativeUi ui) {
        if (skill()) return displayName.isEmpty() ? name : displayName;
        switch (name) {
            case "goal": return ui.t("Goal");
            case "plan": return ui.t("Plan mode");
            case "review": case "code-review": return ui.t("Code review");
            case "loop": return ui.t("Loop");
            case "deep-research": return ui.t("Deep research");
            default: return "/" + name;
        }
    }
    String detail(NativeUi ui) {
        if (!skill()) switch (name) {
            case "goal": return ui.t("Set a goal to keep pursuing");
            case "plan": return ui.t("Turn plan mode on");
            case "review": case "code-review": return ui.t("Review uncommitted changes or compare against a branch");
        }
        return description;
    }
    String source(NativeUi ui) {
        String source;
        switch (scope) {
            case "": source = ""; break;
            case "user": source = ui.t("Personal"); break;
            case "repo": source = ui.t("Project"); break;
            case "admin": source = ui.t("Administrator"); break;
            default: source = ui.t("System");
        }
        return source + (source.isEmpty() || pluginId.isEmpty() ? "" : " · ") + (pluginId.isEmpty() ? "" : ui.t("From {plugin}", "plugin", pluginId));
    }
    String icon() {
        if (skill()) return "package";
        switch (name) {
            case "goal": return "goal";
            case "plan": return "lightbulb";
            case "review": case "code-review": return "bug";
            case "loop": return "repeat-2";
            case "deep-research": return "scan-search";
            default: return "command-terminal";
        }
    }
}
