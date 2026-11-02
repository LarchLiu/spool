package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ScrollView;
import android.widget.TextView;
import java.nio.charset.StandardCharsets;
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
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativePatchTest {
    private JSONArray fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("patches");
        }
    }

    @Test public void strictPatchParsingMatchesActualWebIncludingMovesNumbersAndMalformedFallbacks() throws Exception {
        JSONArray cases = fixtures();
        for (int i = 0; i < cases.length(); i++) {
            JSONObject test = cases.getJSONObject(i);
            List<NativePatch.File> files = NativePatch.parse(test.getString("text"));
            JSONArray expected = test.getJSONArray("expected");
            assertEquals(test.getString("name"), expected.length(), files.size());
            for (int j = 0; j < files.size(); j++) {
                NativePatch.File file = files.get(j);
                JSONObject reference = expected.getJSONObject(j);
                assertEquals(reference.getString("name"), file.name);
                assertEquals(reference.getString("prevName"), file.prevName);
                assertEquals(reference.getString("type"), file.type);
                assertEquals(reference.getBoolean("lineNumbersUnavailable"), file.lineNumbersUnavailable);
                assertEquals(reference.getBoolean("contentsUnavailable"), file.contentsUnavailable);
                assertEquals(reference.getJSONArray("additionLines").toString(), new JSONArray(file.additionLines).toString());
                assertEquals(reference.getJSONArray("deletionLines").toString(), new JSONArray(file.deletionLines).toString());
                JSONArray hunks = reference.getJSONArray("hunks");
                assertEquals(hunks.length(), file.hunks.size());
                for (int k = 0; k < hunks.length(); k++) {
                    JSONObject target = hunks.getJSONObject(k);
                    NativePatch.Hunk actual = file.hunks.get(k);
                    assertEquals(target.getInt("additionStart"), actual.additionStart);
                    assertEquals(target.getInt("deletionStart"), actual.deletionStart);
                    assertEquals(target.getInt("additionCount"), actual.additionCount);
                    assertEquals(target.getInt("deletionCount"), actual.deletionCount);
                    assertEquals(target.getInt("additionLines"), actual.additions);
                    assertEquals(target.getInt("deletionLines"), actual.deletions);
                    assertEquals(target.getString("hunkContext"), actual.context);
                    assertEquals(target.getString("hunkSpecs"), actual.specs);
                    assertEquals(target.getBoolean("noEOFCRAdditions"), actual.noEOFCRAdditions);
                    assertEquals(target.getBoolean("noEOFCRDeletions"), actual.noEOFCRDeletions);
                }
            }
        }
    }

    private TextView text(View root, String value) {
        if (root instanceof TextView && ((TextView) root).getText().toString().equals(value)) return (TextView) root;
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) {
            TextView found = text(((ViewGroup) root).getChildAt(i), value);
            if (found != null) return found;
        }
        return null;
    }

    private int views(View root) {
        int count = 1;
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) count += views(((ViewGroup) root).getChildAt(i));
        return count;
    }

    @Test public void longPatchesUseBoundedViewCountsAndKeepEveryRawSourceCharacterSelectable() throws Exception {
        String source = "*** Begin Patch\n*** Add File: folder/long game.html\n" + "+<p>complete source</p>\n".repeat(25_000) + "*** End Patch";
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePatchView.State state = new NativePatchView.State();
            View patch = NativePatchView.render(ui, source, true, state);
            assertTrue(patch instanceof NativePatchView);
            assertTrue(views(patch) < 30);
            patch.measure(View.MeasureSpec.makeMeasureSpec(240, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            patch.layout(0, 0, patch.getMeasuredWidth(), patch.getMeasuredHeight());
            assertTrue(patch.getMeasuredHeight() > 25_000 * 19);
            TextView toggle = text(patch, "\u25b6 Raw input");
            assertNotNull(toggle);
            toggle.performClick();
            TextView raw = text(patch, source);
            assertNotNull(raw);
            assertTrue(raw.isTextSelectable());
            View snapshot = NativePatchView.render(ui, source, true, state);
            assertTrue(state.rawOpen);
            assertNotNull(text(snapshot, source));
            toggle.performClick();
            assertSame("Web details keeps its raw pre mounted when closed",raw,text(patch, source));
            assertEquals(View.GONE,((View)raw.getParent().getParent()).getVisibility());
            String malformed = source.replace("*** End Patch", "");
            View fallback = NativePatchView.render(ui, malformed, true);
            assertTrue(fallback instanceof ScrollView);
            assertNotNull(text(fallback, malformed));
            toggle.performClick();
            assertTrue(state.rawOpen);
            NativePatchView.render(ui, source + "\n", true, state);
            assertFalse(state.rawOpen);
        }
    }

    @Test public void invalidRangesAndAmbiguousQuotedPathsNeverCrashOrProducePartialDiffs() {
        for (String source : new String[] {
                "diff --git a/a b/a\n@@ -99999999999999999 +1 @@\n-old\n+new\n",
                "diff --git \"a/unterminated b/a\n@@ -1 +1 @@\n-old\n+new\n",
                "*** Begin Patch\n*** Update File: a\n*** Move to: \n@@\n-old\n+new\n*** End Patch"}) assertTrue(NativePatch.parse(source).isEmpty());
    }
}
