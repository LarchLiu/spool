package com.cloudgeek.spool.android;

import java.util.HashMap;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.commonmark.node.CustomNode;
import org.commonmark.parser.beta.InlineContentParser;
import org.commonmark.parser.beta.InlineContentParserFactory;
import org.commonmark.parser.beta.InlineParserState;
import org.commonmark.parser.beta.ParsedInline;
import org.commonmark.parser.beta.Position;
import org.commonmark.parser.beta.Scanner;
import org.json.JSONTokener;

/** Codex file citations are inline file actions; code and escaped markers stay literal. */
final class NativeCodexFileCitation extends CustomNode {
    private static final String MARKER = ":codex-file-citation{";
    private static final Pattern ATTRIBUTE = Pattern.compile("[ \\t]*([A-Za-z_][\\w-]*)=(\"(?:[^\"\\\\\\r\\n]|\\\\.)*\")");
    final String source, path, name;

    private NativeCodexFileCitation(String source, String path) {
        this.source = source;
        this.path = path;
        String normalized = path.replace('\\', '/');
        String leaf = normalized.substring(normalized.lastIndexOf('/') + 1);
        name = leaf.isEmpty() ? path : leaf;
    }

    private static NativeCodexFileCitation parse(String source) {
        String attributes = source.substring(MARKER.length(), source.length() - 1);
        Matcher match = ATTRIBUTE.matcher(attributes);
        Map<String, String> values = new HashMap<>();
        int cursor = 0;
        while (cursor < attributes.length()) {
            if (attributes.substring(cursor).matches("[ \\t]*")) break;
            match.region(cursor, attributes.length());
            if (!match.lookingAt() || values.containsKey(match.group(1))) return null;
            try {
                String quoted = match.group(2);
                // JSONTokener is lenient with unknown escapes: require JSON's actual escape set.
                if (!quoted.matches("\"(?:[^\"\\\\\\x00-\\x1f]|\\\\(?:[\"\\\\/bfnrt]|u[0-9a-fA-F]{4}))*\"")) return null;
                Object value = new JSONTokener(quoted).nextValue();
                if (!(value instanceof String)) return null;
                values.put(match.group(1), (String) value);
            } catch (Exception invalid) { return null; }
            cursor = match.end();
            if (cursor < attributes.length() && attributes.charAt(cursor) != ' ' && attributes.charAt(cursor) != '\t') return null;
        }
        String path = values.get("path");
        return path == null || NativeFileTarget.parse(path) == null ? null : new NativeCodexFileCitation(source, path);
    }

    static final class Factory implements InlineContentParserFactory {
        @Override public Set<Character> getTriggerCharacters() { return Set.of(':'); }
        @Override public InlineContentParser create() { return NativeCodexFileCitation::tryParse; }
    }

    private static ParsedInline tryParse(InlineParserState state) {
        Scanner scanner = state.scanner();
        Position start = scanner.position();
        if (!scanner.next(MARKER)) return ParsedInline.none();
        boolean quoted = false, escaped = false;
        while (scanner.hasNext()) {
            char next = scanner.peek();
            if (next == '\r' || next == '\n') return ParsedInline.none();
            scanner.next();
            if (escaped) escaped = false;
            else if (quoted && next == '\\') escaped = true;
            else if (next == '"') quoted = !quoted;
            else if (!quoted && next == '}') {
                NativeCodexFileCitation citation = parse(scanner.getSource(start, scanner.position()).getContent());
                return citation == null ? ParsedInline.none() : ParsedInline.of(citation, scanner.position());
            }
        }
        return ParsedInline.none();
    }
}
