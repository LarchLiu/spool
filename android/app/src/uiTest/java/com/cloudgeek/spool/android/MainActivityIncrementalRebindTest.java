package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import android.text.Selection;
import android.text.Spannable;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Live events rebind only the turns they changed; a full re-render stays available to callers that need it. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityIncrementalRebindTest {
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private static void event(String kind, JSONObject value) { MainActivityGoalTest.ShadowClient.chatListener.onEvent(kind, value.toString()); idle(); }
    private static List<Object> renders(LinearLayout content) {
        List<Object> result = new ArrayList<>();
        for (int i = 0; i < content.getChildCount(); i++) result.add(ReflectionHelpers.getField(content.getChildAt(i), "render"));
        return result;
    }
    private static TextView text(android.view.View parent, String value) {
        for (TextView text : MainActivityTranscriptPrepareTest.views(parent, TextView.class)) if (text.getText().toString().startsWith(value)) return text;
        throw new AssertionError("Missing text " + value);
    }

    @Test public void turnEventsRebindOnlyTheirTurnAndUnchangedSnapshotsRebindNothing() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            JSONObject value = MainActivityTranscriptPrepareTest.longChat(100);
            value.put("status", "running"); value.getJSONArray("turns").getJSONObject(99).put("status", "running").put("finished_at", 0);
            h.call("startChat"); event("snapshot", value); MainActivityTranscriptPrepareTest.settle(h);
            LinearLayout content = h.get("historyContent"); assertEquals(100, content.getChildCount());
            List<Object> before = renders(content);
            TextView answer = text(content, "Answer 98"); Object document = answer.getText(); Selection.setSelection((Spannable) document, 1, 5);
            // The server streams the last turn: a whole window re-send that names the changed turn.
            JSONObject data = new JSONObject(value.toString()).put("updated_at", 21);
            JSONObject streamed = data.getJSONObject("transcript").getJSONArray("parts").getJSONObject(199);
            streamed.put("text", streamed.getString("text") + " streaming");
            data.put("turn", data.getJSONArray("turns").getJSONObject(99));
            event("turn", data); MainActivityTranscriptPrepareTest.settle(h);
            List<Object> after = renders(content);
            for (int i = 0; i < 99; i++) assertSame("Turn " + i + " was not part of the event and must keep its closure", before.get(i), after.get(i));
            assertNotSame("The streamed turn is rebound", before.get(99), after.get(99));
            assertNotNull(text(content, "Second paragraph 99 streaming"));
            assertSame(document, answer.getText()); assertEquals(1, answer.getSelectionStart()); assertEquals(5, answer.getSelectionEnd());
            assertTrue(h.<JSONObject>get("chat").getJSONObject("transcript").getJSONArray("parts").getJSONObject(199).getString("text").endsWith(" streaming"));
            // An identical snapshot changes nothing: no render pass, no closures replaced.
            JSONObject current = h.get("chat");
            event("snapshot", new JSONObject(current.toString()));
            assertSame(current, h.get("chat")); assertEquals(after, renders(content));
            // A snapshot editing two turns rebinds exactly those two.
            JSONObject edited = new JSONObject(current.toString()).put("updated_at", 22);
            for (int index : new int[] {1, 11}) { JSONObject part = edited.getJSONObject("transcript").getJSONArray("parts").getJSONObject(index); part.put("text", part.getString("text") + " revised"); }
            event("snapshot", edited); MainActivityTranscriptPrepareTest.settle(h);
            List<Object> revised = renders(content);
            for (int i = 0; i < 100; i++) {
                if (i == 0 || i == 5) assertNotSame("Edited turn " + i, after.get(i), revised.get(i));
                else assertSame("Unedited turn " + i, after.get(i), revised.get(i));
            }
            // Callers outside the event path still re-render every turn.
            h.call("refreshProfileViews"); MainActivityTranscriptPrepareTest.settle(h);
            List<Object> full = renders(content);
            for (int i = 0; i < 100; i++) assertNotSame("Full render rebinds turn " + i, revised.get(i), full.get(i));
            assertNull(h.get("pendingRebinds"));
        }
    }
}
