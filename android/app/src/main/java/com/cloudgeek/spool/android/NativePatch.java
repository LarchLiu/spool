package com.cloudgeek.spool.android;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Strict display-only patch parser. Never applies changes or invents source line numbers. */
final class NativePatch {
    static final class Line {
        final char kind;
        String text;
        Line(char kind, String text) { this.kind = kind; this.text = text; }
    }
    static final class Hunk {
        int additionStart, deletionStart, additionCount, deletionCount, additions, deletions;
        boolean noEOFCRAdditions, noEOFCRDeletions;
        String context = "", specs = "";
        final List<Line> lines = new ArrayList<>();
    }
    static final class File {
        String name = "", prevName = "", type = "change";
        boolean lineNumbersUnavailable, contentsUnavailable;
        final List<Hunk> hunks = new ArrayList<>();
        final List<String> additionLines = new ArrayList<>(), deletionLines = new ArrayList<>();
        int additions() { int count = 0; for (Hunk h : hunks) count += h.additions; return count; }
        int deletions() { int count = 0; for (Hunk h : hunks) count += h.deletions; return count; }
    }
    private static final Pattern APPLY_HEADER = Pattern.compile("^\\*\\*\\* (Add|Update|Delete) File: (.+)$");
    private static final Pattern GIT_HEADER = Pattern.compile("^diff --git (\"a/(?:[^\"\\\\]|\\\\.)*\"|a/.+?) (\"b/(?:[^\"\\\\]|\\\\.)*\"|b/.+?)$");
    private static final Pattern HUNK = Pattern.compile("^@@ -(\\d+)(?:,(\\d+))? \\+(\\d+)(?:,(\\d+))? @@(?: ?(.*))?$");

    static List<File> parse(String source) {
        String patch = NativeActivityModel.patchText(source);
        if (patch.isEmpty()) return Collections.emptyList();
        try {
            return patch.startsWith("*** Begin Patch") ? apply(patch) : unified(patch);
        } catch (IllegalArgumentException error) {
            // Partial/unsupported input must keep its complete original text, never a partial diff.
            return Collections.emptyList();
        }
    }

    private static List<File> apply(String patch) {
        String[] lines = patch.replace("\r\n", "\n").replaceFirst("\\s+$", "").split("\n", -1);
        require(lines.length >= 2 && lines[0].equals("*** Begin Patch") && lines[lines.length - 1].equals("*** End Patch"));
        List<File> files = new ArrayList<>();
        int index = 1;
        while (index < lines.length - 1) {
            Matcher header = APPLY_HEADER.matcher(lines[index++]);
            require(header.matches());
            String action = header.group(1), oldName = header.group(2);
            File file = new File();
            file.name = oldName;
            file.lineNumbersUnavailable = action.equals("Update");
            file.contentsUnavailable = action.equals("Delete");
            file.type = action.equals("Add") ? "new" : action.equals("Delete") ? "deleted" : "change";
            if (action.equals("Update") && index < lines.length - 1 && lines[index].startsWith("*** Move to: ")) {
                file.name = lines[index++].substring(13);
                require(!file.name.trim().isEmpty());
                if (!oldName.equals(file.name)) { file.prevName = oldName; file.type = "rename-changed"; }
            }
            Hunk hunk = null;
            boolean eof = false, changed = false;
            while (index < lines.length - 1 && !APPLY_HEADER.matcher(lines[index]).matches()) {
                String line = lines[index++];
                require(!eof && !action.equals("Delete"));
                if (action.equals("Update") && (line.equals("@@") || line.startsWith("@@ "))) {
                    require(hunk == null || !hunk.lines.isEmpty());
                    hunk = new Hunk();
                    hunk.context = line.substring(2).replaceFirst("^\\s+", "");
                    file.hunks.add(hunk);
                } else if (action.equals("Update") && line.equals("*** End of File")) {
                    require(hunk != null && !hunk.lines.isEmpty());
                    eof = true;
                } else {
                    if (action.equals("Update") && line.isEmpty()) line = " ";
                    require(!line.isEmpty() && (action.equals("Add") ? line.charAt(0) == '+' : " +-".indexOf(line.charAt(0)) >= 0));
                    if (hunk == null) { hunk = new Hunk(); file.hunks.add(hunk); }
                    char kind = line.charAt(0);
                    changed |= kind == '+' || kind == '-';
                    hunk.lines.add(new Line(kind, line.substring(1) + "\n"));
                }
            }
            require(!action.equals("Update") || hunk != null && !hunk.lines.isEmpty() && changed);
            int oldOffset = 0, newOffset = 0;
            for (Hunk chunk : file.hunks) {
                for (Line line : chunk.lines) {
                    if (line.kind != '+') chunk.deletionCount++;
                    if (line.kind != '-') chunk.additionCount++;
                }
                chunk.additionStart = newOffset + (chunk.additionCount > 0 ? 1 : 0);
                chunk.deletionStart = oldOffset + (chunk.deletionCount > 0 ? 1 : 0);
                if (!file.lineNumbersUnavailable) chunk.specs = "@@ -" + chunk.deletionStart + "," + chunk.deletionCount
                        + " +" + chunk.additionStart + "," + chunk.additionCount + " @@ " + chunk.context + "\n";
                oldOffset += chunk.deletionCount + 1;
                newOffset += chunk.additionCount + 1;
                collect(file, chunk);
            }
            files.add(file);
        }
        return files;
    }

    private static List<File> unified(String patch) {
        String[] lines = patch.replace("\r\n", "\n").split("\n", -1);
        boolean git = lines[0].startsWith("diff --git ");
        List<File> files = new ArrayList<>();
        int index = 0;
        while (index < lines.length) {
            if (lines[index].isEmpty()) { index++; continue; }
            File file = new File();
            if (git) {
                Matcher header = GIT_HEADER.matcher(lines[index++]);
                require(header.matches());
                file.prevName = path(header.group(1), true);
                file.name = path(header.group(2), true);
            } else require(lines[index].startsWith("--- "));
            boolean headerSeen = git;
            while (index < lines.length && !lines[index].startsWith("@@")) {
                String line = lines[index];
                if (git && line.startsWith("diff --git ")) break;
                if (line.startsWith("--- ")) {
                    require(index + 1 < lines.length && lines[index + 1].startsWith("+++ "));
                    String oldName = path(line.substring(4).split("\t", 2)[0], git);
                    String newName = path(lines[index + 1].substring(4).split("\t", 2)[0], git);
                    if (!oldName.equals("/dev/null")) { file.prevName = oldName; file.name = oldName; }
                    if (!newName.equals("/dev/null")) file.name = newName;
                    headerSeen = true;
                    index += 2;
                    continue;
                }
                if (line.startsWith("new file mode ")) file.type = "new";
                else if (line.startsWith("deleted file mode ")) file.type = "deleted";
                else if (line.startsWith("similarity index ")) file.type = line.equals("similarity index 100%") ? "rename-pure" : "rename-changed";
                else if (line.startsWith("rename from ")) file.prevName = path(line.substring(12), false);
                else if (line.startsWith("rename to ")) file.name = path(line.substring(10), false);
                else require(line.isEmpty() || git && (line.startsWith("index ") || line.startsWith("old mode ") || line.startsWith("new mode ")));
                index++;
            }
            require(headerSeen && !file.name.isEmpty());
            while (index < lines.length && lines[index].startsWith("@@")) {
                Hunk hunk = new Hunk();
                String spec = lines[index++];
                Matcher header = HUNK.matcher(spec);
                require(header.matches());
                hunk.deletionStart = Integer.parseInt(header.group(1));
                hunk.deletionCount = header.group(2) == null ? 1 : Integer.parseInt(header.group(2));
                hunk.additionStart = Integer.parseInt(header.group(3));
                hunk.additionCount = header.group(4) == null ? 1 : Integer.parseInt(header.group(4));
                hunk.context = header.group(5) == null ? "" : header.group(5);
                hunk.specs = spec + "\n";
                int oldCount = 0, newCount = 0;
                while (index < lines.length) {
                    String line = lines[index];
                    if (line.equals("\\ No newline at end of file")) {
                        require(!hunk.lines.isEmpty());
                        Line previous = hunk.lines.get(hunk.lines.size() - 1);
                        previous.text = previous.text.replaceFirst("\\n$", "");
                        hunk.noEOFCRAdditions |= previous.kind != '-';
                        hunk.noEOFCRDeletions |= previous.kind != '+';
                        index++;
                        continue;
                    }
                    if (oldCount == hunk.deletionCount && newCount == hunk.additionCount) break;
                    require(!line.isEmpty() && " +-".indexOf(line.charAt(0)) >= 0);
                    char kind = line.charAt(0);
                    if (kind != '+') oldCount++;
                    if (kind != '-') newCount++;
                    require(oldCount <= hunk.deletionCount && newCount <= hunk.additionCount);
                    hunk.lines.add(new Line(kind, line.substring(1) + (index < lines.length - 1 ? "\n" : "")));
                    index++;
                }
                require(oldCount == hunk.deletionCount && newCount == hunk.additionCount);
                file.hunks.add(hunk);
                collect(file, hunk);
                while (index < lines.length && lines[index].isEmpty()) index++;
            }
            require(git || !file.hunks.isEmpty());
            if (!git && !file.name.equals(file.prevName) && !file.prevName.isEmpty()) file.type = "rename-changed";
            if (!file.type.startsWith("rename-")) file.prevName = "";
            files.add(file);
            if (index < lines.length) require(git ? lines[index].startsWith("diff --git ") : lines[index].startsWith("--- "));
        }
        return files;
    }

    private static void collect(File file, Hunk hunk) {
        for (Line line : hunk.lines) {
            if (line.kind != '-') file.additionLines.add(line.text);
            if (line.kind != '+') file.deletionLines.add(line.text);
            if (line.kind == '+') hunk.additions++;
            if (line.kind == '-') hunk.deletions++;
        }
    }

    private static String path(String value, boolean git) {
        String name = value.trim();
        if (name.startsWith("\"")) {
            require(name.endsWith("\"") && name.length() > 1);
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            String body = name.substring(1, name.length() - 1);
            for (int i = 0; i < body.length();) {
                int point = body.codePointAt(i); i += Character.charCount(point);
                if (point == '\\') {
                    require(i < body.length());
                    char next = body.charAt(i++);
                    if (next >= '0' && next <= '7') {
                        int octal = next - '0', count = 1;
                        while (count < 3 && i < body.length() && body.charAt(i) >= '0' && body.charAt(i) <= '7') { octal = octal * 8 + body.charAt(i++) - '0'; count++; }
                        bytes.write(octal);
                        continue;
                    }
                    switch (next) {
                        case 'n': point = '\n'; break;
                        case 'r': point = '\r'; break;
                        case 't': point = '\t'; break;
                        case '\\': case '"': point = next; break;
                        default: throw new IllegalArgumentException("Unsupported quoted path");
                    }
                }
                byte[] encoded = new String(Character.toChars(point)).getBytes(StandardCharsets.UTF_8);
                bytes.write(encoded, 0, encoded.length);
            }
            name = new String(bytes.toByteArray(), StandardCharsets.UTF_8);
        }
        return git && (name.startsWith("a/") || name.startsWith("b/")) ? name.substring(2) : name;
    }

    private static void require(boolean valid) { if (!valid) throw new IllegalArgumentException("Incomplete or unsupported patch"); }
}
