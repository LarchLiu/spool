package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.text.Selection;
import android.text.Spannable;
import android.text.Spanned;
import android.text.style.ClickableSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
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
public class NativeLinkDecorationTest {
    private TextView text(View view) {
        if (view instanceof TextView) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            TextView found = text(((ViewGroup) view).getChildAt(i)); if (found != null) return found;
        }
        return null;
    }

    @Test public void wrappedLinkInkKeepsActualWebLineExtentsAndNativeClickSelectionBehavior() throws Exception {
        JSONArray fixtures;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-line-metrics.json")) {
            assertNotNull(input); fixtures = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("wrappingCases");
        }
        assertEquals(3, fixtures.length());
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h1200dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                float scale = ui.context.getResources().getDisplayMetrics().density;
                for (int i = 0; i < fixtures.length(); i++) {
                    JSONObject fixture = fixtures.getJSONObject(i); java.util.List<String> opened = new java.util.ArrayList<>();
                    View markdown = NativeMarkdown.view(ui, fixture.getString("source"), false, opened::add);
                    controller.get().setContentView(markdown);
                    markdown.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(324), View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(ui.dp(400), View.MeasureSpec.AT_MOST));
                    markdown.layout(0, 0, markdown.getMeasuredWidth(), markdown.getMeasuredHeight());
                    TextView text = text(markdown); assertNotNull(text); assertEquals(fixture.getString("text"), text.getText().toString());
                    java.util.List<RectF> ink = new java.util.ArrayList<>();
                    Bitmap image = Bitmap.createBitmap(markdown.getWidth(), markdown.getHeight(), Bitmap.Config.ARGB_8888);
                    try {
                        markdown.draw(new Canvas(image) {
                            @Override public void drawRect(float left, float top, float right, float bottom, Paint paint) {
                                if (paint.getColor() == ui.primary) {
                                    RectF rect = new RectF(left, top, right, bottom); getMatrix().mapRect(rect); ink.add(rect);
                                }
                                super.drawRect(left, top, right, bottom, paint);
                            }
                        });
                    } finally { image.recycle(); }
                    JSONArray lines = fixture.getJSONArray("lines");
                    assertEquals(fixture.getString("id"), lines.length(), ink.size());
                    assertEquals(fixture.getInt("lineCount"), text.getLineCount());
                    for (int line = 0; line < lines.length(); line++) {
                        JSONObject expected = lines.getJSONObject(line); RectF actual = ink.get(line);
                        assertEquals(fixture.getString("id") + "/left/" + density, expected.getDouble("left") * scale, actual.left, 1);
                        assertEquals(fixture.getString("id") + "/right/" + density, expected.getDouble("right") * scale, actual.right, 1);
                        int textLine = expected.getInt("line");
                        float x = (actual.left + actual.right) / 2, y = (text.getLayout().getLineTop(textLine) + text.getLayout().getLineBottom(textLine)) / 2f;
                        for (int action : new int[] {android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_UP}) {
                            android.view.MotionEvent event = android.view.MotionEvent.obtain(0, 0, action, x, y, 0);
                            try { assertTrue(text.getMovementMethod().onTouchEvent(text, (Spannable) text.getText(), event)); }
                            finally { event.recycle(); }
                        }
                        assertEquals(line + 1, opened.size()); assertEquals("https://example.com", opened.get(line));
                    }
                    Object document = text.getText(); Selection.setSelection((Spannable) document, 1, text.length() - 1);
                    markdown.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(270), View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(ui.dp(400), View.MeasureSpec.AT_MOST));
                    markdown.layout(0, 0, markdown.getMeasuredWidth(), markdown.getMeasuredHeight());
                    assertSame(document, text.getText()); assertEquals(1, text.getSelectionStart()); assertEquals(text.length() - 1, text.getSelectionEnd());
                }
            }
        }
    }

    @Test public void actualProductionLinksRenderWithCssDecorationsAtAllDensitiesWithoutReplacingTextOrSelection() throws Exception {
        JSONArray fixtures;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-markdown-links.json")) {
            assertNotNull(input); fixtures = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("decorationCases");
        }
        Path directory = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(directory);
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) for (boolean dark : new boolean[] {false, true}) {
            RuntimeEnvironment.setQualifiers("w360dp-h1200dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark);
                NativeMarkdownFlow page = new NativeMarkdownFlow(ui); page.setBackgroundColor(ui.elevated);
                float scale = ui.context.getResources().getDisplayMetrics().density;
                page.collapseTop = page.collapseBottom = false; page.inset(18 * scale, 25 * scale, 18 * scale, 0);
                for (int i = 0; i < fixtures.length(); i++) {
                    JSONObject item = fixtures.getJSONObject(i);
                    NativeUi locale = new NativeUi(controller.get(), dark, item.optString("locale", "en"));
                    page.add(NativeMarkdown.view(locale, item.getString("text"), false, ignored -> fail("Drawing cannot open a link")), 0, 14 * scale);
                }
                controller.get().setContentView(page);
                int width = ui.dp(360), height = ui.dp(1200);
                page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
                page.layout(0, 0, width, height);
                JSONArray rects = new JSONArray(), rows = new JSONArray();
                Bitmap image = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                Canvas canvas = new Canvas(image) {
                    @Override public void drawRect(float left, float top, float right, float bottom, Paint paint) {
                        if (paint.getColor() == ui.primary) {
                            RectF bounds = new RectF(left, top, right, bottom); getMatrix().mapRect(bounds);
                            try { rects.put(new JSONObject().put("left", bounds.left).put("top", bounds.top).put("right", bounds.right).put("bottom", bounds.bottom)); }
                            catch (Exception error) { throw new AssertionError(error); }
                        }
                        super.drawRect(left, top, right, bottom, paint);
                    }
                };
                page.draw(canvas); assertTrue(rects.length() > fixtures.length());
                for (int i = 0; i < fixtures.length(); i++) {
                    View row = page.getChildAt(i); TextView text = text(row); assertNotNull(text); assertTrue(text.isTextSelectable());
                    Spanned styled = (Spanned) text.getText(); int links = styled.getSpans(0, styled.length(), ClickableSpan.class).length; assertTrue(links > 0);
                    Selection.setSelection((Spannable) styled, 0, Math.min(3, styled.length()));
                    Object document = text.getText(); page.requestLayout();
                    page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
                    page.layout(0, 0, width, height);
                    assertSame(document, text.getText()); assertEquals(0, text.getSelectionStart()); assertEquals(Math.min(3, styled.length()), text.getSelectionEnd());
                    JSONArray baselines = new JSONArray();
                    for (int line = 0; line < text.getLineCount(); line++) baselines.put(row.getTop() + text.getTop() + text.getTotalPaddingTop() + text.getLayout().getLineBaseline(line));
                    android.text.TextPaint cssPaint = new android.text.TextPaint(text.getPaint()); cssPaint.setTextSize(text.getTextSize() / scale);
                    Paint.FontMetrics metrics = cssPaint.getFontMetrics();
                    JSONArray positions = new JSONArray();
                    for (int offset = 0; offset <= styled.length(); offset++) positions.put(text.getLayout().getPrimaryHorizontal(offset));
                    String value = styled.toString();
                    rows.put(new JSONObject().put("id", fixtures.getJSONObject(i).getString("id")).put("y", row.getTop()).put("height", row.getHeight()).put("text", value).put("baselines", baselines)
                            .put("textSize", text.getTextSize()).put("letterSpacing", text.getLetterSpacing()).put("positions", positions)
                            .put("metrics", new JSONObject().put("ascent", -metrics.ascent).put("descent", metrics.descent))
                            .put("plainPaintAdvance", text.getPaint().getRunAdvance(value, 0, value.length(), 0, value.length(), false, value.length()))
                            .put("cssAdvance", cssPaint.getRunAdvance(value, 0, value.length(), 0, value.length(), false, value.length())));
                }
                String suffix = "api" + android.os.Build.VERSION.SDK_INT + "-dpr" + (density.equals("mdpi") ? "1" : density.equals("420dpi") ? "2.625" : "3") + (dark ? "-dark" : "-light");
                try (FileOutputStream output = new FileOutputStream(directory.resolve("native-link-decoration-" + suffix + ".png").toFile())) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                finally { image.recycle(); }
                Files.writeString(directory.resolve("native-link-decoration-" + suffix + ".json"), new JSONObject().put("rows", rows).put("rects", rects).toString(2));
            }
        }
    }
}
