package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.text.Spanned;
import android.text.style.ClickableSpan;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
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
public class NativeCodexFileCitationTest {
    private void links(View root, List<String> names) {
        if (root instanceof TextView && ((TextView) root).getText() instanceof Spanned) {
            Spanned text = (Spanned) ((TextView) root).getText();
            for (ClickableSpan link : text.getSpans(0, text.length(), ClickableSpan.class)) {
                String name = text.subSequence(text.getSpanStart(link), text.getSpanEnd(link)).toString();
                if (name.startsWith(":codex-file-citation")) continue; // Existing #section link label.
                names.add(name);
                link.onClick(root);
            }
        }
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) links(((ViewGroup) root).getChildAt(i), names);
    }

    @Test public void citationsUseTheSameFileActionsAndLiteralRulesAsWeb() throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/file-citations.json")) {
            assertNotNull(input); cases = new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            for (int i = 0; i < cases.length(); i++) {
                JSONObject item = cases.getJSONObject(i);
                List<String> opened = new ArrayList<>(), names = new ArrayList<>();
                View markdown = NativeMarkdown.view(ui, item.getString("source"), false, opened::add);
                assertTrue(opened.isEmpty()); links(markdown, names);
                JSONArray expected = item.getJSONArray("links");
                assertEquals(item.getString("id"), expected.length(), opened.size());
                for (int j = 0; j < expected.length(); j++) {
                    assertEquals(item.getString("id"), expected.getJSONObject(j).getString("name"), names.get(j));
                    NativeFileTarget target = NativeFileTarget.parse(opened.get(j));
                    assertNotNull(item.getString("id") + ": " + opened.get(j), target);
                    assertEquals(item.getString("id"), NativeFileTarget.parse(expected.getJSONObject(j).getString("path")).path, target.path);
                }
                Spanned preview = (Spanned) NativeMarkdown.preview(ui, item.getString("source"));
                assertEquals(0, preview.getSpans(0, preview.length(), ClickableSpan.class).length);
                if (expected.length() > 0) assertFalse(item.getString("id"), preview.toString().contains(":codex-file-citation"));
                if (item.getString("id").equals("existing-link-label")) assertTrue(preview.toString().contains(":codex-file-citation"));
            }
        }
    }
}
