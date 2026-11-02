package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.os.Looper;
import android.text.Spanned;
import android.text.style.ForegroundColorSpan;
import android.text.style.StrikethroughSpan;
import android.text.style.StyleSpan;
import android.text.style.UnderlineSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.util.ReflectionHelpers;

/** Separately validates the render adapters with the Web tokens; engine conformance is pure JVM. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeSyntaxRenderTest {
    private JSONObject fixtures() throws Exception {
        try (var input = getClass().getResourceAsStream("/web-syntax.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }
    private List<NativeSyntaxEngine.Line> tokens(JSONObject data, String source, boolean dark) throws Exception {
        JSONArray cases = data.getJSONArray("fixtures"); String theme = dark ? "pierre-dark" : "pierre-light";
        for (int i = 0; i < cases.length(); i++) {
            JSONObject fixture = cases.getJSONObject(i); if (!fixture.getString("code").equals(source)) continue;
            JSONArray lines = fixture.getJSONObject("themes").getJSONArray(theme); List<NativeSyntaxEngine.Line> result = new ArrayList<>();
            int offset = 0;
            for (int line = 0; line < lines.length(); line++) {
                int end = source.indexOf('\n', offset); if (end < 0) end = source.length();
                List<NativeSyntaxEngine.Run> runs = new ArrayList<>(); JSONArray expected = lines.getJSONArray(line);
                for (int token = 0; token < expected.length(); token++) {
                    JSONObject value = expected.getJSONObject(token); int start = value.getInt("offset") - offset, length = value.getString("content").length();
                    String color = value.optString("color"); if (color.isEmpty()) color = data.getJSONObject("foreground").getString(theme);
                    if (length > 0) runs.add(new NativeSyntaxEngine.Run(start, start + length, NativeSyntaxEngine.color(color), value.optInt("fontStyle")));
                }
                result.add(new NativeSyntaxEngine.Line(offset, end - offset, runs)); offset = end + 1;
            }
            return result;
        }
        throw new IllegalArgumentException("Missing Web token fixture");
    }
    private JSONObject cell(int color, int style, float x, float end) throws Exception {
        return new JSONObject().put("color", Integer.toUnsignedLong(color)).put("style", style).put("x", x).put("end", end);
    }
    private JSONArray source(NativeFileSource source) throws Exception {
        JSONArray lines = new JSONArray(); Spanned text = (Spanned) source.getText(); android.text.Layout layout = source.getLayout();
        for (int line = 0; line < layout.getLineCount(); line++) {
            int start = layout.getLineStart(line), end = layout.getLineEnd(line); JSONArray cells = new JSONArray();
            for (int i = start; i < end; i++) if (text.charAt(i) != '\n') {
                int color = source.getCurrentTextColor(), style = 0;
                for (ForegroundColorSpan span : text.getSpans(i, i + 1, ForegroundColorSpan.class)) color = span.getForegroundColor();
                for (StyleSpan span : text.getSpans(i, i + 1, StyleSpan.class)) style |= ((span.getStyle() & Typeface.ITALIC) != 0 ? 1 : 0) | ((span.getStyle() & Typeface.BOLD) != 0 ? 2 : 0);
                if (text.getSpans(i, i + 1, UnderlineSpan.class).length > 0) style |= 4;
                if (text.getSpans(i, i + 1, StrikethroughSpan.class).length > 0) style |= 8;
                cells.put(cell(color, style, layout.getPrimaryHorizontal(i) - layout.getPrimaryHorizontal(start), layout.getPrimaryHorizontal(i + 1) - layout.getPrimaryHorizontal(start)));
            }
            lines.put(cells);
        }
        return lines;
    }
    private View code(View view, boolean additions) {
        if (view.getClass().getSimpleName().equals("Code") && !(boolean) ReflectionHelpers.getField(view, "gutter") && (boolean) ReflectionHelpers.getField(view, "additions") == additions) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) { View result = code(((ViewGroup) view).getChildAt(i), additions); if (result != null) return result; }
        return null;
    }
    private JSONArray diff(View code, boolean additions) throws Exception {
        JSONArray lines = new JSONArray(); Paint paint = ReflectionHelpers.getField(code, "text");
        List<NativeDiffLayout.Row> rows = ReflectionHelpers.getField(code, "rows");
        for (NativeDiffLayout.Row row : rows) {
            NativeDiffLayout.Cell value = additions ? row.next : row.old; if (value == null || value.kind == 'n') continue;
            JSONArray cells = new JSONArray(); String text = NativeWordDiff.cleanLastNewline(value.text);
            for (int i = 0; i < text.length(); i++) {
                int color = paint.getColor(), style = 0;
                for (NativeSyntaxEngine.Run run : value.syntax) if (i >= run.start && i < run.end) { color = run.color; style = run.style; break; }
                cells.put(cell(color, style, NativeCodeTabs.width(paint, text, i), NativeCodeTabs.width(paint, text, i + 1)));
            }
            lines.put(cells);
        }
        return lines;
    }

    @Test public void renderOriginalWebTokensAtEveryMobileWidthAndThemeWithoutChangingTextOrGeometry() throws Exception {
        JSONObject data = fixtures(); JSONArray fixtures = data.getJSONArray("fixtures"); String contents = fixtures.getJSONObject(fixtures.length() - 2).getString("code");
        File directory = new File(System.getProperty("spool.ui.artifacts")); assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320,360,412}) for (boolean dark : new boolean[] {false,true}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + (dark ? "night" : "notnight") + "-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark); List<Runnable> jobs = new ArrayList<>(); ui.codeWorker = jobs::add;
                ui.syntax = new NativeSyntax(jobs::add, (path, text, theme) -> tokens(data, text, theme), () -> {});
                LinearLayout page = ui.column(); page.setBackgroundColor(ui.elevated);
                NativeFileSource source = new NativeFileSource(ui, "render.ts", contents, 0); page.addView(source, new LinearLayout.LayoutParams(-1, -2));
                NativePatchView patch = (NativePatchView) NativePatchView.render(ui, data.getString("syntaxPatchContents"), false);
                LinearLayout.LayoutParams diffParams = new LinearLayout.LayoutParams(-1, -2); diffParams.topMargin = ui.dp(8); page.addView(patch, diffParams);
                controller.get().setContentView(page);
                page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED)); page.layout(0, 0, width, page.getMeasuredHeight());
                int height = page.getMeasuredHeight(); for (Runnable job : List.copyOf(jobs)) job.run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
                page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED)); page.layout(0, 0, width, page.getMeasuredHeight());
                assertEquals(contents, source.getText().toString()); assertEquals(height, page.getMeasuredHeight());
                assertTrue((boolean) ReflectionHelpers.getField(patch, "precise"));
                JSONObject report = new JSONObject().put("source", source(source)).put("old", diff(code(patch, false), false)).put("next", diff(code(patch, true), true));
                String suffix = width + (dark ? "-dark" : "-light"); Files.writeString(new File(directory, "native-syntax-" + suffix + ".json").toPath(), report.toString(2));
                Bitmap bitmap = Bitmap.createBitmap(width, page.getMeasuredHeight(), Bitmap.Config.ARGB_8888);
                try (FileOutputStream output = new FileOutputStream(new File(directory, "native-syntax-" + suffix + ".png"))) { page.draw(new Canvas(bitmap)); assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                finally { bitmap.recycle(); }
            }
        }
    }
}
