package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeTableOriginTest {
    private void cells(View view, List<TextView> output) {
        if (view instanceof TextView && ("markdown:th".equals(view.getTag()) || "markdown:td".equals(view.getTag()))) output.add((TextView) view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) cells(((ViewGroup) view).getChildAt(i), output);
    }

    private void layout(View view, int width, int height, int top) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, top, width, top + height);
    }

    @Test public void tablePaintBaselinesFollowActualWebAcrossFractionalRowsInsetsAndAncestorMoves() throws Exception {
        JSONObject reference;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-table-origins.json")) {
            assertNotNull(input); reference = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        JSONArray samples = new JSONArray(), cases = reference.getJSONArray("cases"), fixtures = reference.getJSONArray("fixtures");
        double maximum = 0, geometryMaximum = 0; int values = 0, clicks = 0, wrappingFailures = 0;
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) for (int f = 0; f < fixtures.length(); f++) {
            RuntimeEnvironment.setQualifiers("w360dp-h600dp-" + density);
            JSONObject fixture = fixtures.getJSONObject(f);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, fixture.getString("locale"));
                ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                float scale = ui.context.getResources().getDisplayMetrics().density;
                FrameLayout parent = new FrameLayout(ui.context); parent.setBackgroundColor(ui.elevated);
                NativeMarkdownFlow flow = new NativeMarkdownFlow(ui); flow.collapseTop = flow.collapseBottom = false;
                List<String> opened = new ArrayList<>();
                View markdown = NativeMarkdown.view(ui, fixture.getString("source"), false, opened::add); flow.add(markdown, 0, 0);
                parent.addView(flow, new FrameLayout.LayoutParams(-1, -2)); controller.get().setContentView(parent);
                List<TextView> cells = new ArrayList<>(); cells(markdown, cells); assertEquals(14, cells.size());
                List<Object> documents = new ArrayList<>();
                for (TextView cell : cells) { documents.add(cell.getText()); Selection.setSelection((Spannable) cell.getText(), 0, cell.length()); }
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject sample = cases.getJSONObject(i);
                    if (sample.getDouble("density") != scale || !sample.getString("id").equals(fixture.getString("id"))) continue;
                    float padding = (float) sample.getDouble("padding"); int offset = sample.getInt("offsetPhysical");
                    flow.inset(18 * scale, padding * scale, 18 * scale, 0);
                    layout(parent, ui.dp(360), ui.dp(600), offset);
                    boolean settled = false;
                    for (int pass = 0; pass < 6; pass++) {
                        if (!parent.getViewTreeObserver().dispatchOnPreDraw()) { settled = true; break; }
                        layout(parent, ui.dp(360), ui.dp(600), offset);
                    }
                    assertTrue("Table baseline alignment must converge", settled);
                    JSONArray measured = new JSONArray();
                    for (int c = 0; c < cells.size(); c++) {
                        TextView cell = cells.get(c); JSONObject expected = sample.getJSONArray("cells").getJSONObject(c);
                        assertSame(documents.get(c), cell.getText()); assertEquals(0, cell.getSelectionStart()); assertEquals(cell.length(), cell.getSelectionEnd());
                        assertEquals(expected.getString("text"), cell.getText().toString());
                        assertEquals(fixture.getString("id") + "/" + density + "/" + c, expected.getJSONArray("baselines").length(), cell.getLineCount());
                        int[] location = new int[2]; cell.getLocationInWindow(location);
                        JSONArray baselines = new JSONArray(), lines = new JSONArray();
                        for (int line = 0; line < cell.getLineCount(); line++) {
                            String painted = cell.getText().subSequence(cell.getLayout().getLineStart(line), cell.getLayout().getLineEnd(line)).toString().trim();
                            lines.put(painted);
                            if (!painted.equals(expected.getJSONArray("lines").getString(line))) wrappingFailures++;
                            int baseline = location[1] + cell.getTotalPaddingTop() + cell.getLayout().getLineBaseline(line);
                            double error = Math.abs(baseline - expected.getJSONArray("baselines").getDouble(line) * scale);
                            maximum = Math.max(maximum, error); values++;
                            baselines.put(new JSONObject().put("baseline", baseline).put("expected", expected.getJSONArray("baselines").getDouble(line) * scale).put("error", error));
                        }
                        double originError = Math.abs(location[1] - expected.getDouble("y") * scale), heightError = Math.abs(cell.getHeight() - expected.getDouble("height") * scale);
                        geometryMaximum = Math.max(geometryMaximum, Math.max(originError, heightError));
                        String borderline = "across several native text lines while";
                        measured.put(new JSONObject().put("text", cell.getText().toString()).put("lines", lines).put("baselines", baselines).put("originError", originError).put("heightError", heightError)
                                .put("width", cell.getWidth()).put("textSize", cell.getTextSize()).put("padding", cell.getCompoundPaddingLeft() + cell.getCompoundPaddingRight())
                                .put("lineWidths", lineWidths(cell)).put("borderlineAdvance", cell.getPaint().getRunAdvance(borderline, 0, borderline.length(), 0, borderline.length(), false, borderline.length())));
                        if (c >= 2 && c % 2 == 0) {
                            float x = cell.getTotalPaddingLeft() + scale;
                            float y = cell.getTotalPaddingTop() + (cell.getLayout().getLineTop(0) + cell.getLayout().getLineBottom(0)) / 2f;
                            int before = opened.size();
                            for (int action : new int[] {android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_UP}) {
                                android.view.MotionEvent event = android.view.MotionEvent.obtain(0, 0, action, x, y, 0);
                                try { assertTrue(cell.getMovementMethod().onTouchEvent(cell, (Spannable) cell.getText(), event)); }
                                finally { event.recycle(); }
                            }
                            assertEquals(before + 1, opened.size()); assertEquals("https://example.com/" + (c / 2 - 1), opened.get(before)); clicks++;
                            Selection.setSelection((Spannable) cell.getText(), 0, cell.length());
                        }
                    }
                    samples.put(new JSONObject().put("id", fixture.getString("id")).put("locale", fixture.getString("locale")).put("density", scale).put("padding", padding).put("offsetPhysical", offset).put("cells", measured));
                    if (padding == 25 && offset == 0) {
                        Bitmap image = Bitmap.createBitmap(parent.getWidth(), parent.getHeight(), Bitmap.Config.ARGB_8888);
                        try (java.io.OutputStream stream = Files.newOutputStream(output.resolve("native-table-origin-api" + android.os.Build.VERSION.SDK_INT + "-" + fixture.getString("id") + "-dpr" + scale + ".png"))) {
                            parent.draw(new Canvas(image)); assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                        } finally { image.recycle(); }
                    }
                }
            }
        }
        Files.writeString(output.resolve("native-table-origins-api" + android.os.Build.VERSION.SDK_INT + ".json"), new JSONObject().put("values", values).put("maximum", maximum).put("geometryMaximum", geometryMaximum).put("clicks", clicks).put("wrappingFailures", wrappingFailures).put("cases", samples).toString(2));
        assertEquals(cases.length(), samples.length()); assertEquals(cases.length() * 6, clicks);
        assertTrue("Actual Web table cell origin/height error: " + geometryMaximum, geometryMaximum <= 1);
        assertTrue("Actual Web table painted baseline error: " + maximum, maximum <= 1);
        assertEquals("Wrapped table line contents must match, not just line counts", 0, wrappingFailures);
    }

    private JSONArray lineWidths(TextView cell) throws org.json.JSONException {
        JSONArray output = new JSONArray();
        for (int line = 0; line < cell.getLineCount(); line++) output.put(cell.getLayout().getLineWidth(line));
        return output;
    }
}
