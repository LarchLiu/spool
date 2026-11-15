package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Pattern;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * Same conservative tool-name/command classification as the server and Web fallback. Kinds follow
 * Codex's own reading of a command: a content search is a search, a printed range is a read.
 */
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

    /** Commands of a source, and which of them read the previous command's output. */
    private static final class Parsed {
        final List<List<String>> commands = new ArrayList<>(); final List<Boolean> piped = new ArrayList<>();
    }

    private static List<List<String>> words(String source) { return words(source, null); }

    private static List<List<String>> words(String source, Boundary boundary) {
        Parsed parsed = parse(source, boundary);
        return parsed == null ? null : parsed.commands;
    }

    private static Parsed parse(String source, Boundary boundary) {
        Parsed parsed = new Parsed(); List<String> words = new ArrayList<>();
        StringBuilder word = new StringBuilder(); char quote = 0; boolean started = false, afterPipe = false;
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
                boolean stage = !words.isEmpty();
                if (stage) { parsed.commands.add(words); parsed.piped.add(afterPipe); words = new ArrayList<>(); }
                boolean doubled = i + 1 < source.length() && ch == source.charAt(i + 1) && (ch == '|' || ch == '&');
                String separator = doubled ? "" + ch + ch : String.valueOf(ch);
                // A newline may separate a pipe from its next stage.
                if (stage || !separator.equals("\n") || !afterPipe) afterPipe = separator.equals("|");
                if (boundary != null) boundary.accept(i, separator);
                if (doubled) i++;
            } else if (Character.isWhitespace(ch)) {
                if (started) words.add(word.toString()); word.setLength(0); started = false;
            } else { word.append(ch); started = true; }
        }
        if (quote != 0) return null;
        if (started) words.add(word.toString());
        if (!words.isEmpty()) { parsed.commands.add(words); parsed.piped.add(afterPipe); }
        if (boundary != null) boundary.accept(source.length(), "");
        return parsed;
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

    /** Input summaries only: one row per sequential step; a pipeline stays together. */
    static JSONArray operations(String source, int depth) {
        if (depth > 4 || source.length() > 65536) return null;
        List<String> groups = new ArrayList<>(); int[] start = {0}, stageStart = {0}; boolean[] required = {false}, valid = {true};
        List<List<String>> commands = words(source, (end, separator) -> {
            String stage = source.substring(stageStart[0], end).trim();
            if (stage.isEmpty() && required[0] && separator.equals("\n")) return;
            if (stage.isEmpty() && (required[0] || separator.matches("\\||\\|\\||&|&&"))) valid[0] = false;
            required[0] = separator.matches("\\||\\|\\||&&");
            stageStart[0] = end + separator.length();
            if (separator.equals("|")) return;
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
        // A lone pipeline did one thing; it is summarized as a single call.
        if (groups.size() < 2) return null;
        JSONArray operations = new JSONArray();
        for (String group : groups) {
            List<List<String>> groupCommands = words(group);
            if (groupCommands == null || groupCommands.isEmpty()) return null;
            String script = groupCommands.size() == 1 ? shellScript(groupCommands.get(0)) : null;
            JSONArray nested = script == null ? null : operations(script, depth + 1);
            if (nested != null) for (int i = 0; i < nested.length(); i++) operations.put(nested.opt(i));
            else {
                String kind = shellKind(group, depth); String[] target = operationTarget(groupCommands.get(0), kind);
                if (bytes(group) > 512 || bytes(target[0]) > 1024 || bytes(target[1]) > 1024) return null;
                JSONObject operation = new JSONObject();
                try {
                    operation.put("kind", kind).put("command", group);
                    if (!target[0].isEmpty()) operation.put("path", target[0]);
                    if (!target[1].isEmpty()) operation.put("query", target[1]);
                } catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
                operations.put(operation);
            }
            if (operations.length() > 64) return null;
        }
        return operations;
    }

    private static int bytes(String value) { return value.getBytes(java.nio.charset.StandardCharsets.UTF_8).length; }

    /** grep spells its options differently from rg: -r and -E take no value there. */
    private static final Pattern GREP_VALUE_FLAG = Pattern.compile("^(?:-[efmABCdD]|--(?:regexp|file|max-count|context|after-context|before-context|include|exclude|exclude-dir|exclude-from|binary-files|devices|directories|label))$");
    private static final Pattern HEAD_VALUE_FLAG = Pattern.compile("^(?:-[nc]|--(?:lines|bytes))$");
    private static final Pattern LS_PLAIN_FLAG = Pattern.compile("^-[1aAbBcCdfFgGhHiklLmnopqrRsStuUvxX]+$");
    private static final Pattern FIND_NAME_FLAG = Pattern.compile("^-i?(?:name|path|regex)$");

    private static boolean searchCommand(String command) { return command.matches("rg|ripgrep|grep|egrep|fgrep"); }

    /** An option of this search command whose value is the next argument. */
    private static boolean searchValueFlag(String command, String arg) {
        return (command.equals("rg") || command.equals("ripgrep") ? RG_VALUE_FLAG : GREP_VALUE_FLAG).matcher(arg).matches();
    }

    /** A grep-like command's pattern and path operands; patterned also covers a pattern file. */
    private static final class Search {
        String query = ""; boolean patterned; final List<String> paths = new ArrayList<>();
        Search(String command, List<String> args) {
            boolean options = true;
            for (int i = 0; i < args.size(); i++) {
                String arg = args.get(i);
                if (options && arg.equals("--")) { options = false; continue; }
                if (options && arg.startsWith("-") && !arg.equals("-")) {
                    String value = ""; boolean pattern = true;
                    if (arg.equals("-e") || arg.equals("--regexp")) value = i + 1 < args.size() ? args.get(i + 1) : "";
                    else if (arg.equals("-f") || arg.equals("--file") || arg.startsWith("--file=") || arg.startsWith("-f")) value = "";
                    else if (arg.startsWith("--regexp=")) value = arg.substring(9);
                    else if (arg.startsWith("-e")) value = arg.substring(2);
                    else pattern = false;
                    if (pattern && !patterned) { query = value; patterned = true; }
                    if (searchValueFlag(command, arg)) i++;
                    continue;
                }
                if (!patterned) { query = arg; patterned = true; continue; }
                if (!arg.equals("-")) paths.add(arg);
            }
        }
    }

    private static String one(List<String> paths) { return paths != null && paths.size() == 1 ? paths.get(0) : ""; }

    /** {path, query}: the single path, and a search's pattern, that a command names. */
    private static String[] operationTarget(List<String> words, String kind) {
        if (!kind.equals("read") && !kind.equals("list") && !kind.equals("search")) return new String[] {"", ""};
        Args input = new Args(words); List<String> args = input.args; String command = input.command;
        if (command.equals("sed")) return new String[] {one(sedPrintFiles(args)), ""};
        if (command.equals("find")) {
            List<String> paths = new ArrayList<>(); String query = "";
            for (int i = 0; i < args.size(); i++) {
                if (args.get(i).startsWith("-") || args.get(i).equals("!")) {
                    for (int j = i; j + 1 < args.size() && kind.equals("search"); j++)
                        if (FIND_NAME_FLAG.matcher(args.get(j)).matches()) { query = args.get(j + 1); break; }
                    break;
                }
                paths.add(args.get(i));
            }
            return new String[] {one(paths), query};
        }
        if (searchCommand(command) && kind.equals("search")) {
            Search search = new Search(command, args);
            return new String[] {one(search.paths), search.query};
        }
        List<String> paths = new ArrayList<>(); boolean options = true;
        for (int i = 0; i < args.size(); i++) {
            String arg = args.get(i);
            if (options && arg.equals("--")) { options = false; continue; }
            if (options && arg.startsWith("-") && !arg.equals("-")) {
                if (searchCommand(command)) { if (searchValueFlag(command, arg)) i++; }
                else if (command.matches("head|tail") && HEAD_VALUE_FLAG.matcher(arg).matches()) i++;
                else if (command.matches("cat|head|tail|nl")) { /* flags without values */ }
                else if (command.equals("ls") && LS_PLAIN_FLAG.matcher(arg).matches()) { /* flags without values */ }
                else return new String[] {"", ""};
                continue;
            }
            if (!arg.equals("-")) paths.add(arg);
        }
        return new String[] {one(paths), ""};
    }

    private static int operands(List<String> args, Pattern valueFlag) {
        int count = 0;
        for (int i = 0; i < args.size(); i++) {
            String arg = args.get(i);
            if (arg.startsWith("-") && !arg.equals("-")) { if (valueFlag != null && valueFlag.matcher(arg).matches()) i++; continue; }
            count++;
        }
        return count;
    }

    /** A pipeline stage that only narrows or formats the output of the stage before it. */
    private static boolean pipeFilter(List<String> words) {
        Args input = new Args(words); List<String> args = input.args;
        switch (input.command) {
            case "wc": case "tr": case "cut": case "column": return true;
            case "sort":
                for (String arg : args) if (arg.startsWith("-o") || arg.startsWith("--output")) return false;
                return true;
            case "uniq": return operands(args, null) == 0;
            case "head": case "tail": return operands(args, HEAD_VALUE_FLAG) == 0;
            case "rg": case "ripgrep": case "grep": case "egrep": case "fgrep": {
                if (args.contains("--files")) return false;
                Search search = new Search(input.command, args);
                return search.patterned && search.paths.isEmpty();
            }
            case "sed": {
                List<String> files = sedPrintFiles(args);
                return files != null && files.isEmpty();
            }
            default: return false;
        }
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

    /** {path, query} of a command that does one thing. */
    static String[] target(String source, int depth) {
        String[] none = {"", ""};
        if (depth > 4 || source.length() > 65536) return none;
        Parsed parsed = parse(source, null);
        if (parsed == null || parsed.commands.isEmpty()) return none;
        if (parsed.commands.size() == 1) {
            String script = shellScript(parsed.commands.get(0));
            if (script != null) return target(script, depth + 1);
        }
        for (int i = 1; i < parsed.commands.size(); i++) if (!parsed.piped.get(i) || !pipeFilter(parsed.commands.get(i))) return none;
        return operationTarget(parsed.commands.get(0), commandKind(parsed.commands.get(0), depth));
    }

    static String shellKind(String source, int depth) {
        if (depth > 4 || source.length() > 65536) return "shell";
        Parsed parsed = parse(source, null);
        if (parsed == null || parsed.commands.isEmpty() || parsed.commands.size() > 64) return "shell";
        String kind = commandKind(parsed.commands.get(0), depth);
        for (int i = 1; i < parsed.commands.size(); i++) {
            if (parsed.piped.get(i) && pipeFilter(parsed.commands.get(i))) continue;
            if (!commandKind(parsed.commands.get(i), depth).equals(kind)) return "shell";
        }
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
                return "search";
            case "grep": case "egrep": case "fgrep": return "search";
            case "ls": case "tree": return "list";
            case "find":
                for (String arg : args) if (arg.matches("^-(delete|exec|execdir|ok|okdir|fprint|fprint0|fprintf|fls)$")) return "shell";
                for (String arg : args) if (FIND_NAME_FLAG.matcher(arg).matches()) return "search";
                return "list";
            case "cat": case "head": case "tail": case "less": case "more": case "nl": return "read";
            case "sed": return sedPrintFiles(args) != null ? "read" : "shell";
            default: return "shell";
        }
    }
}
