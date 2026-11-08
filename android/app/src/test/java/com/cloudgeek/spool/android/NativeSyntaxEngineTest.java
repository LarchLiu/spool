package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;

public class NativeSyntaxEngineTest {
    static Path root() {
        Path path = Path.of("").toAbsolutePath();
        while (path != null && !Files.isRegularFile(path.resolve("android/settings.gradle"))) path = path.getParent();
        if (path == null) throw new IllegalStateException("Repository not found");
        return path;
    }
    static JsonObject fixtures() throws Exception {
        try (var input = Files.newBufferedReader(root().resolve("android/app/src/test/resources/web-syntax.json"))) { return JsonParser.parseReader(input).getAsJsonObject(); }
    }
    static NativeSyntaxEngine engine() {
        Path assets = root().resolve("android/app/src/main/assets/syntax");
        return new NativeSyntaxEngine(path -> Files.newBufferedReader(assets.resolve(path), StandardCharsets.UTF_8));
    }
    @Test public void filenameDetectionUsesTheActualCaseSensitivePierreMapping() throws Exception {
        NativeSyntaxEngine engine = engine();
        for (JsonElement item : fixtures().getAsJsonArray("filenames")) {
            JsonObject value = item.getAsJsonObject();
            assertEquals(value.get("file").getAsString(), value.get("lang").getAsString(), engine.language(value.get("file").getAsString()));
        }
    }
    @Test public void fencedLanguageNamesAndAliasesUseTheSameGrammarAsSourceFiles() throws Exception {
        try (NativeSyntaxEngine engine = engine()) {
            for (String[] pair : new String[][] {{"typescript file.ts", "fixture.ts"}, {"TS", "fixture.ts"},
                    {"javascript", "fixture.js"}, {"python", "fixture.py"}, {"c++", "fixture.cpp"}, {"c#", "fixture.cs"}}) {
                String source = "const value = \"hello\"; // comment\n";
                for (boolean dark : new boolean[] {false, true}) {
                    List<NativeSyntaxEngine.Line> expected = engine.highlight(pair[1], source, dark);
                    List<NativeSyntaxEngine.Line> actual = engine.highlightCode(pair[0], source, dark);
                    assertEquals(expected.size(), actual.size());
                    for (int line = 0; line < expected.size(); line++) {
                        assertEquals(expected.get(line).runs.size(), actual.get(line).runs.size());
                        for (int i = 0; i < expected.get(line).runs.size(); i++) {
                            NativeSyntaxEngine.Run a = expected.get(line).runs.get(i), b = actual.get(line).runs.get(i);
                            assertArrayEquals(pair[0], new int[] {a.start,a.end,a.color,a.style}, new int[] {b.start,b.end,b.color,b.style});
                        }
                    }
                }
            }
            assertEquals(0xff0a0a0a, engine.highlightCode("unknown-language", "literal", false).get(0).runs.get(0).color);
            assertEquals(0xfffafafa, engine.highlightCode(null, "literal", true).get(0).runs.get(0).color);
        }
    }
    @Test public void nativeTextMateColorsAndStylesMatchTheActualWebForEveryUtf16Unit() throws Exception {
        try (NativeSyntaxEngine engine = engine()) {
        JsonObject data = fixtures();
        List<String> mismatches = new ArrayList<>();
        for (JsonElement element : data.getAsJsonArray("fixtures")) for (String theme : new String[] {"pierre-light", "pierre-dark"}) {
            JsonObject fixture = element.getAsJsonObject();
            String file = fixture.get("file").getAsString(), code = fixture.get("code").getAsString();
            int[] expected = new int[code.length()], styles = new int[code.length()], actual = new int[code.length()], actualStyles = new int[code.length()];
            JsonArray lines = fixture.getAsJsonObject("themes").getAsJsonArray(theme);
            for (JsonElement line : lines) for (JsonElement item : line.getAsJsonArray()) {
                JsonObject token = item.getAsJsonObject();
                int start = token.get("offset").getAsInt(), size = token.get("content").getAsString().length();
                for (int i = start; i < start + size; i++) {
                    String color = token.has("color") ? token.get("color").getAsString() : "";
                    expected[i] = NativeSyntaxEngine.color(color.isEmpty() ? data.getAsJsonObject("foreground").get(theme).getAsString() : color); styles[i] = token.has("fontStyle") ? token.get("fontStyle").getAsInt() : 0;
                }
            }
            List<NativeSyntaxEngine.Line> nativeLines = engine.highlight(file, code, theme.endsWith("dark"));
            assertEquals(file + " line count", lines.size(), nativeLines.size());
            for (NativeSyntaxEngine.Line line : nativeLines) for (NativeSyntaxEngine.Run run : line.runs) {
                assertTrue(run.start >= 0 && run.end <= line.length && run.start < run.end);
                assertFalse(Character.isLowSurrogate(code.charAt(line.offset + run.start)));
                for (int i = line.offset + run.start; i < line.offset + run.end; i++) { actual[i] = run.color; actualStyles[i] = run.style; }
            }
            for (int i = 0; i < code.length(); i++) if (expected[i] != 0 && (expected[i] != actual[i] || styles[i] != actualStyles[i])) {
                mismatches.add(theme + " " + file + " UTF16@" + i + " expected=" + Integer.toHexString(expected[i]) + "/" + styles[i] + " actual=" + Integer.toHexString(actual[i]) + "/" + actualStyles[i]);
                break;
            }
        }
        assertTrue(String.join("\n", mismatches), mismatches.isEmpty());
        }
    }
}
