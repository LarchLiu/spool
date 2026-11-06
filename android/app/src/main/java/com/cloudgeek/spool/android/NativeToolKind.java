package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Pattern;
import org.json.JSONArray;
import org.json.JSONObject;

/** Same conservative tool-name/command classification as the server and Web fallback. */
final class NativeToolKind {
    private static final Pattern ASSIGNMENT = Pattern.compile("^[A-Za-z_][A-Za-z0-9_]*=");
    private static final Pattern RG_VALUE_FLAG = Pattern.compile("^(?:-[efgtTmjErABC]|--(?:regexp|file|glob|iglob|type|type-not|ignore-file|pre|pre-glob|encoding|max-count|max-depth|threads|replace|sort|sortr|color|colors|context|after-context|before-context|path-separator|hostname-bin|hyperlink-format))$");

    static String nameKind(String name) {
        name = name.trim().toLowerCase(Locale.ROOT).replace("__", ".");
        name = name.substring(name.lastIndexOf('.') + 1);
        String compact = name.replace("_", "").replace("-", "");
        switch (compact) {
            case "reason": case "reasoning": case "think": case "thinking": return "thinking";
            case "applypatch": case "patch": case "filechange": case "edit": case "editfile": case "multiedit": case "notebookedit": case "replace": return "write";
            case "write": case "writefile": case "createfile": return "create";
            case "deletefile": case "removefile": case "unlink": return "delete";
            case "read": case "readfile": case "getfile": case "openfile": case "viewfile": case "notebookread": return "read";
            case "glob": case "listfiles": case "listdirectory": case "directorylisting": case "readdir": case "ls": case "find": return "list";
            case "grep": case "ripgrep": case "rg": case "searchfiles": case "searchcode": case "findinfiles": return "search";
            case "websearch": case "webfetch": case "searchweb": case "browser": case "internet": return "web";
            case "shell": case "bash": case "zsh": case "sh": case "command": case "terminal": case "exec": case "execcommand": case "execute": case "executecommand": case "runcommand": case "shellcommand": case "commandexecution": case "writestdin": case "bashoutput": case "killshell": case "killbash": return "shell";
            default: return name.startsWith("browser_") ? "web" : "other";
        }
    }

    static String command(String text, JSONObject data) {
        if (data == null) return text.trim().startsWith("{") ? "" : text;
        for (String key : new String[] {"cmd", "command"}) {
            Object value = data.opt(key);
            if (value instanceof String) return (String) value;
            if (value instanceof JSONArray) {
                JSONArray args = (JSONArray) value; List<String> quoted = new ArrayList<>();
                for (int i = 0; i < args.length(); i++) {
                    if (!(args.opt(i) instanceof String)) return "";
                    quoted.add("'" + args.optString(i).replace("'", "'\\''") + "'");
                }
                return String.join(" ", quoted);
            }
        }
        return "";
    }

    private interface Boundary { void accept(int end, String separator); }

    private static List<List<String>> words(String source) { return words(source, null); }

    private static List<List<String>> words(String source, Boundary boundary) {
        List<List<String>> commands = new ArrayList<>(); List<String> words = new ArrayList<>();
        StringBuilder word = new StringBuilder(); char quote = 0; boolean started = false;
        for (int i = 0; i < source.length(); i++) {
            char ch = source.charAt(i);
            if (ch == '\\' && quote != '\'') {
                if (++i == source.length()) return null;
                if (source.charAt(i) != '\n') { word.append(source.charAt(i)); started = true; }
            } else if (ch == quote) quote = 0;
            else if (ch == '\'' || ch == '"') {
                if (quote != 0) word.append(ch);
                else { quote = ch; started = true; }
            } else if (quote == '\'') word.append(ch);
            else if ("$`<>(){}".indexOf(ch) >= 0) return null;
            else if (quote != 0) word.append(ch);
            else if (ch == '#' && !started) return null;
            else if (";|&\n".indexOf(ch) >= 0) {
                if (started) words.add(word.toString()); word.setLength(0); started = false;
                if (!words.isEmpty()) { commands.add(words); words = new ArrayList<>(); }
                boolean doubled = i + 1 < source.length() && ch == source.charAt(i + 1) && (ch == '|' || ch == '&');
                if (boundary != null) boundary.accept(i, doubled ? "" + ch + ch : String.valueOf(ch));
                if (doubled) i++;
            } else if (Character.isWhitespace(ch)) {
                if (started) words.add(word.toString()); word.setLength(0); started = false;
            } else { word.append(ch); started = true; }
        }
        if (quote != 0) return null;
        if (started) words.add(word.toString());
        if (!words.isEmpty()) commands.add(words);
        if (boundary != null) boundary.accept(source.length(), "");
        return commands;
    }

    private static final class Args {
        String command; List<String> args;
        Args(List<String> words) {
            int at = 0;
            while (at < words.size() && ASSIGNMENT.matcher(words.get(at)).find()) at++;
            command = at < words.size() ? base(words.get(at++)) : "";
            if (command.equals("env")) {
                if (at < words.size() && words.get(at).equals("--")) at++;
                while (at < words.size() && ASSIGNMENT.matcher(words.get(at)).find()) at++;
                command = at < words.size() ? base(words.get(at++)) : "";
            }
            args = words.subList(at, words.size());
        }
    }

    private static String shellScript(List<String> words) {
        Args input = new Args(words);
        if (!input.command.matches("sh|bash|zsh|dash|ksh|fish")) return null;
        for (int i = 0; i < input.args.size(); i++) {
            String arg = input.args.get(i);
            if (!arg.matches("^-[lc]+$")) break;
            if (arg.indexOf('c') >= 0) return input.args.size() == i + 2 ? input.args.get(i + 1) : null;
        }
        return null;
    }

    /** Input summaries only: a pipeline has one combined output and stays one row. */
    static JSONArray operations(String source, int depth) {
        if (depth > 4 || source.length() > 65536) return null;
        List<String> groups = new ArrayList<>(); int[] start = {0}, stageStart = {0}; boolean[] pipeline = {false}, required = {false}, valid = {true};
        List<List<String>> commands = words(source, (end, separator) -> {
            String stage = source.substring(stageStart[0], end).trim();
            if (stage.isEmpty() && required[0] && separator.equals("\n")) return;
            if (stage.isEmpty() && (required[0] || separator.matches("\\||\\|\\||&|&&"))) valid[0] = false;
            required[0] = separator.matches("\\||\\|\\||&&");
            stageStart[0] = end + separator.length();
            if (separator.equals("|")) { pipeline[0] = true; return; }
            String group = source.substring(start[0], end).trim();
            if (!group.isEmpty()) groups.add(group);
            start[0] = end + separator.length();
        });
        if (!valid[0] || commands == null || commands.isEmpty() || commands.size() > 64) return null;
        for (List<String> command : commands) if (new Args(command).command.matches("if|then|elif|else|fi|for|while|until|do|done|case|esac|select|function|!|time")) return null;
        if (commands.size() == 1) {
            String script = shellScript(commands.get(0));
            if (script != null) return operations(script, depth + 1);
        }
        if (groups.size() < 2 && !pipeline[0]) return null;
        JSONArray operations = new JSONArray();
        for (String group : groups) {
            List<List<String>> groupCommands = words(group);
            if (groupCommands == null || groupCommands.isEmpty()) return null;
            String script = groupCommands.size() == 1 ? shellScript(groupCommands.get(0)) : null;
            JSONArray nested = script == null ? null : operations(script, depth + 1);
            if (nested != null) for (int i = 0; i < nested.length(); i++) operations.put(nested.opt(i));
            else {
                String kind = shellKind(group, depth), path = operationPath(groupCommands.get(0), kind);
                if (group.getBytes(java.nio.charset.StandardCharsets.UTF_8).length > 512 || path.getBytes(java.nio.charset.StandardCharsets.UTF_8).length > 1024) return null;
                JSONObject operation = new JSONObject();
                try {
                    operation.put("kind", kind).put("command", group);
                    if (!path.isEmpty()) operation.put("path", path);
                } catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
                operations.put(operation);
            }
            if (operations.length() > 64) return null;
        }
        return operations;
    }

    private static String operationPath(List<String> words, String kind) {
        if (!kind.equals("read") && !kind.equals("list") && !kind.equals("search")) return "";
        Args input = new Args(words); List<String> args = input.args;
        if (input.command.equals("sed")) {
            List<String> files = sedPrintFiles(args);
            return files != null && files.size() == 1 ? files.get(0) : "";
        }
        boolean search = input.command.matches("rg|ripgrep|grep|egrep|fgrep"), pattern = kind.equals("list"), options = true;
        List<String> paths = new ArrayList<>();
        for (int i = 0; i < args.size(); i++) {
            String arg = args.get(i);
            if (options && arg.equals("--")) { options = false; continue; }
            if (options && arg.startsWith("-")) {
                if (search) {
                    if (Pattern.compile("^(?:-[ef]|--(?:regexp|file))(?:=|$)|^-[ef].+").matcher(arg).find()) pattern = true;
                    if (RG_VALUE_FLAG.matcher(arg).matches() || arg.matches("^--(?:include|exclude|exclude-dir)$")) i++;
                } else if (input.command.matches("head|tail") && arg.matches("^(?:-[nc]|--(?:lines|bytes))$")) i++;
                else if (!input.command.matches("cat|head|tail")) return "";
                continue;
            }
            if (search && !pattern) { pattern = true; continue; }
            if (!arg.equals("-")) paths.add(arg);
        }
        return paths.size() == 1 ? paths.get(0) : "";
    }

    private static List<String> sedPrintFiles(List<String> args) {
        List<String> scripts = new ArrayList<>(), files = new ArrayList<>(); boolean options = true;
        for (int i = 0; i < args.size(); i++) {
            String arg = args.get(i);
            if (options && arg.equals("--")) { options = false; continue; }
            if (options && arg.startsWith("-")) {
                if (arg.matches("^-[nEr]+$") || arg.matches("--quiet|--silent|--regexp-extended")) continue;
                if (arg.equals("-e") || arg.equals("--expression")) {
                    if (++i == args.size()) return null;
                    scripts.add(args.get(i));
                } else if (arg.startsWith("--expression=")) scripts.add(arg.substring(13));
                else if (arg.startsWith("-e") && arg.length() > 2) scripts.add(arg.substring(2));
                else return null;
            } else if (scripts.isEmpty()) scripts.add(arg);
            else files.add(arg);
        }
        if (scripts.isEmpty()) return null;
        for (String script : scripts) {
            int count = 0;
            for (String item : script.split("[;\\n]")) {
                if (item.trim().isEmpty()) continue;
                if (!item.matches("^\\s*(?:(?:[0-9]+|\\$)\\s*(?:,\\s*(?:[0-9]+|\\$)\\s*)?)?p\\s*$")) return null;
                count++;
            }
            if (count == 0) return null;
        }
        return files;
    }

    static String searchPath(String source, int depth) {
        if (depth > 4 || source.length() > 65536) return "";
        List<List<String>> commands = words(source);
        if (commands == null || commands.size() != 1) return "";
        String script = shellScript(commands.get(0));
        return script == null ? operationPath(commands.get(0), "search") : searchPath(script, depth + 1);
    }

    static String shellKind(String source, int depth) {
        if (depth > 4 || source.length() > 65536) return "shell";
        List<List<String>> commands = words(source);
        if (commands == null || commands.isEmpty() || commands.size() > 64) return "shell";
        String kind = commandKind(commands.get(0), depth);
        for (int i = 1; i < commands.size(); i++) if (!commandKind(commands.get(i), depth).equals(kind)) return "shell";
        return kind;
    }

    private static String base(String value) { return value.substring(value.lastIndexOf('/') + 1); }

    private static String commandKind(List<String> words, int depth) {
        Args input = new Args(words);
        String command = input.command; List<String> args = input.args;
        switch (command) {
            case "sh": case "bash": case "zsh": case "dash": case "ksh": case "fish":
                for (int i = 0; i < args.size(); i++) {
                    String arg = args.get(i);
                    if (!arg.matches("^-[lc]+$")) break;
                    if (arg.indexOf('c') >= 0) return args.size() == i + 2 ? shellKind(args.get(i + 1), depth + 1) : "shell";
                }
                return "shell";
            case "rg": case "ripgrep":
                for (int i = 0; i < args.size(); i++) {
                    String arg = args.get(i);
                    if (arg.equals("--")) break;
                    if (arg.equals("--files")) return "list";
                    if (RG_VALUE_FLAG.matcher(arg).matches()) i++;
                }
                return "read";
            case "grep": case "egrep": case "fgrep": return "read";
            case "ls": case "tree": return "list";
            case "find":
                for (String arg : args) if (arg.matches("^-(delete|exec|execdir|ok|okdir|fprint|fprint0|fprintf|fls)$")) return "shell";
                return "list";
            case "cat": case "head": case "tail": case "less": case "more": return "read";
            case "sed": return sedPrintFiles(args) != null ? "search" : "shell";
            default: return "shell";
        }
    }
}
