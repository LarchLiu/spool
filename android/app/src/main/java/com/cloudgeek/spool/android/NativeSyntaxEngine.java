package com.cloudgeek.spool.android;

import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.cloudgeek.spool.textmate.NativeOniguruma;
import java.io.IOException;
import java.io.Reader;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.eclipse.tm4e.core.grammar.IGrammar;
import org.eclipse.tm4e.core.grammar.IStateStack;
import org.eclipse.tm4e.core.internal.grammar.tokenattrs.EncodedTokenAttributes;
import org.eclipse.tm4e.core.registry.IGrammarSource;
import org.eclipse.tm4e.core.registry.IRegistryOptions;
import org.eclipse.tm4e.core.registry.IThemeSource;
import org.eclipse.tm4e.core.registry.Registry;

/** Native TextMate tokenization of the original Web grammars/themes; no UI or network access. */
final class NativeSyntaxEngine implements AutoCloseable {
    interface Source { Reader open(String path) throws IOException; }
    static final class Run {
        final int start, end, color, style;
        Run(int start, int end, int color, int style) { this.start = start; this.end = end; this.color = color; this.style = style; }
    }
    static final class Line {
        final int offset, length;
        final List<Run> runs;
        Line(int offset, int length, List<Run> runs) { this.offset = offset; this.length = length; this.runs = runs; }
    }
    private static final Pattern COMPOUND = Pattern.compile("\\.([^/\\\\]+\\.[^/\\\\]+)$"), SIMPLE = Pattern.compile("\\.([^.]+)$");
    private final Source source;
    private JsonObject manifest;
    private Registry registry;
    private boolean dark;
    private String loadedLanguage;
    private NativeOniguruma.Pool nativePool;
    private final java.util.Set<String> registeredScopes = new java.util.HashSet<>();

    NativeSyntaxEngine(Source source) { this.source = source; }
    private JsonObject manifest() throws IOException {
        if (manifest == null) try (Reader input = source.open("manifest.json")) { manifest = JsonParser.parseReader(input).getAsJsonObject(); }
        return manifest;
    }
    synchronized String language(String path) throws IOException {
        JsonObject files = manifest().getAsJsonObject("files");
        String language = match(path, files);
        if (language != null) return language;
        String basename = path.substring(Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\')) + 1);
        language = match(basename, files);
        return language == null ? "text" : language;
    }
    private static String match(String file, JsonObject files) {
        if (files.has(file)) return files.get(file).getAsString();
        Matcher compound = COMPOUND.matcher(file);
        if (compound.find() && files.has(compound.group(1))) return files.get(compound.group(1)).getAsString();
        Matcher simple = SIMPLE.matcher(file);
        return simple.find() && files.has(simple.group(1)) ? files.get(simple.group(1)).getAsString() : null;
    }

    synchronized List<Line> highlight(String path, String contents, boolean dark) throws IOException {
        return highlightLanguage(language(path), contents, dark);
    }

    synchronized List<Line> highlightCode(String info, String contents, boolean dark) throws IOException {
        String language = info == null ? "" : info.trim().split("\\s+", 2)[0].toLowerCase(java.util.Locale.ROOT);
        return highlightLanguage(language, contents, dark);
    }

    private List<Line> highlightLanguage(String language, String contents, boolean dark) throws IOException {
        JsonElement scope = manifest().getAsJsonObject("languages").get(language);
        IGrammar grammar = null;
        if (scope != null) {
            for (JsonElement value : manifest.getAsJsonObject("bundles").getAsJsonArray(language)) registeredScopes.add(value.getAsString());
            if (registry == null || this.dark != dark || !language.equals(loadedLanguage)) initialize(dark, language);
            grammar = registry.loadGrammar(scope.getAsString());
        }
        int foreground = dark ? 0xfffafafa : 0xff0a0a0a;
        List<String> colors = grammar == null ? Collections.emptyList() : registry.getColorMap();
        List<Line> result = new ArrayList<>();
        IStateStack state = null;
        try (NativeOniguruma.Scope ignored = nativePool == null ? null : nativePool.activate()) {
        for (int offset = 0; offset <= contents.length();) {
            if (Thread.currentThread().isInterrupted()) throw new java.util.concurrent.CancellationException();
            int newline = contents.indexOf('\n', offset), end = newline < 0 ? contents.length() : newline;
            int length = end - offset;
            if (length > 0 && contents.charAt(end - 1) == '\r' && newline >= 0) length--;
            List<Run> runs = new ArrayList<>();
            if (grammar != null && length <= 1000) {
                var tokens = grammar.tokenizeLine2(contents.substring(offset, offset + length), state, Duration.ZERO);
                state = tokens.getRuleStack();
                int[] values = tokens.getTokens();
                for (int i = 0; i < values.length; i += 2) {
                    int from = values[i], to = i + 2 < values.length ? Math.min(length, values[i + 2]) : length;
                    if (to <= from) continue;
                    int colorID = EncodedTokenAttributes.getForeground(values[i + 1]);
                    int color = colorID < colors.size() && colors.get(colorID) != null ? color(colors.get(colorID)) : foreground;
                    int style = EncodedTokenAttributes.getFontStyle(values[i + 1]);
                    runs.add(new Run(from, to, color, style));
                }
            } else if (length > 0) runs.add(new Run(0, length, foreground, 0));
            result.add(new Line(offset, length, Collections.unmodifiableList(runs)));
            if (newline < 0) break;
            offset = newline + 1;
        }
        }
        return Collections.unmodifiableList(result);
    }

    private void initialize(boolean dark, String language) throws IOException {
        if (nativePool != null) nativePool.close();
        nativePool = null; registry = null;
        this.dark = dark; loadedLanguage = language;
        java.util.Set<String> bundled = new java.util.HashSet<>(registeredScopes);
        Registry next = new Registry(new IRegistryOptions() {
            public IGrammarSource getGrammarSource(String scope) {
                if (!bundled.contains(scope)) return null;
                JsonElement path = manifest.getAsJsonObject("scopes").get(scope);
                if (path == null) return null;
                return new IGrammarSource() {
                    public String getFilePath() { return path.getAsString(); }
                    public Reader getReader() throws IOException { return source.open(path.getAsString()); }
                };
            }
            public Collection<String> getInjections(String scope) {
                JsonElement values = manifest.getAsJsonObject("injections").get(scope);
                if (values == null) return Collections.emptyList();
                List<String> result = new ArrayList<>();
                for (JsonElement value : values.getAsJsonArray()) if (bundled.contains(value.getAsString())) result.add(value.getAsString());
                return result;
            }
        });
        try (Reader input = source.open(dark ? "pierre-dark.json" : "pierre-light.json")) {
            StringBuilder json = new StringBuilder(); char[] buffer = new char[4096]; int length;
            while ((length = input.read(buffer)) >= 0) json.append(buffer, 0, length);
            next.setTheme(IThemeSource.fromString(IThemeSource.ContentType.JSON, json.toString()));
        }
        registry = next; nativePool = new NativeOniguruma.Pool();
    }
    static int color(String hex) {
        String value = hex.substring(1);
        long color = Long.parseLong(value, 16);
        if (value.length() == 6) return (int) (0xff000000L | color);
        if (value.length() == 8) return (int) ((color & 255) << 24 | color >>> 8);
        throw new IllegalArgumentException("Unsupported syntax color: " + hex);
    }
    @Override public synchronized void close() {
        if (nativePool != null) nativePool.close();
        registry = null; nativePool = null; manifest = null; loadedLanguage = null; registeredScopes.clear();
    }
}
