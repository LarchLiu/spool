package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.text.Spanned;
import android.text.TextPaint;
import android.text.style.ClickableSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeMarkdownLinksTest {
    private TextView paragraph(View view) {
        if (view instanceof TextView) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            TextView found = paragraph(((ViewGroup) view).getChildAt(i)); if (found != null) return found;
        }
        return null;
    }

    @Test public void productionMarkdownMatchesActualWebInlineTextLinksAndClickDestinations() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-markdown-links.json")) {
            assertNotNull(input); cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("cases");
        }
        List<String> errors = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (boolean dark : new boolean[] {false, true}) {
                NativeUi ui = new NativeUi(controller.get(), dark);
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject item = cases.getJSONObject(i);
                    List<String> opened = new ArrayList<>();
                    View markdown = NativeMarkdown.view(ui, item.getString("source"), false, opened::add);
                    assertTrue("Rendering cannot dispatch a link", opened.isEmpty());
                    TextView view = paragraph(markdown); assertNotNull(item.getString("id"), view);
                    Spanned text = (Spanned) view.getText();
                    JSONArray actual = new JSONArray();
                    List<ClickableSpan> links = new ArrayList<>(java.util.Arrays.asList(text.getSpans(0, text.length(), ClickableSpan.class)));
                    links.sort(Comparator.comparingInt(text::getSpanStart).thenComparingInt(text::getSpanEnd));
                    for (ClickableSpan link : links) {
                        link.onClick(view);
                        TextPaint paint = new TextPaint(view.getPaint()); link.updateDrawState(paint);
                        assertEquals(ui.primary, paint.getColor()); assertFalse("CSS decoration must replace the platform underline", paint.isUnderlineText());
                        actual.put(new JSONObject().put("start", text.getSpanStart(link)).put("end", text.getSpanEnd(link)).put("target", opened.get(opened.size() - 1)));
                    }
                    if (!item.getString("text").equals(text.toString()) || !item.getJSONArray("actionable").toString().equals(actual.toString()))
                        errors.add(item.getString("id") + " expected=" + item.getJSONArray("actionable") + " actual=" + actual + " text=" + text);
                    assertEquals("Summaries must not contain nested links", 0, ((Spanned) NativeMarkdown.preview(ui, item.getString("source"))).getSpans(0, NativeMarkdown.preview(ui, item.getString("source")).length(), ClickableSpan.class).length);
                }
            }
        }
        assertTrue(String.join("\n", errors), errors.isEmpty());
    }
}
