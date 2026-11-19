package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivitySubagentTest {
    private JSONObject task(String id, String name, String summary) throws Exception {
        return new JSONObject().put("id", id).put("role", "assistant").put("kind", "tool_use").put("name", name).put("text", name + "\n" + summary)
                .put("tool", new JSONObject().put("kind", "task").put("status", "running")
                        .put("task", new JSONObject().put("id", id).put("summary", summary)));
    }

    private <T> List<T> views(View view, Class<T> type) {
        List<T> result = new ArrayList<>();
        if (type.isInstance(view)) result.add(type.cast(view));
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++)
            result.addAll(views(((ViewGroup) view).getChildAt(i), type));
        return result;
    }

    private long occurrences(View view, String text) {
        return views(view, TextView.class).stream().filter(value -> value.getText().toString().contains(text)).count();
    }

    private View history(MainActivityGoalTest.Harness harness, boolean markwon) throws Exception {
        if (markwon) {
            NativeMarkwonHistory history = harness.get("markwonHistory");
            NativeMarkwonHistoryTest.await(history);
            return history;
        }
        View history = harness.get("historyContent");
        NativeMarkwonHistoryTest.layout(harness.get("root"));
        return history;
    }

    private void toggleProcess(View history) {
        NativeTurn response = views(history, NativeTurn.class).get(0);
        assertTrue(((View) response.activity.summaryLabel.getParent()).performClick());
    }

    @Test public void subagentsRemainVisibleBesideFinalAnswersInBothRenderers() throws Exception {
        for (boolean markwon : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, markwon)) {
            JSONObject first = task("first", "Inspect parser", "Checking syntax");
            JSONObject second = task("second", "Inspect schema", "Checking migrations");
            JSONArray parts = new JSONArray().put(new JSONObject("{role:'user',kind:'text',text:'Inspect project'}"))
                    .put(new JSONObject("{role:'assistant',kind:'tool_use',name:'Read',text:'hidden-work.txt',tool:{kind:'read',path:'hidden-work.txt'}}"))
                    .put(first)
                    .put(new JSONObject("{role:'user',kind:'text',text:'Also inspect tests',timestamp:2000}"))
                    .put(new JSONObject("{role:'user',kind:'context',name:'notes.txt',file_path:'/fixture/notes.txt'}"));
            JSONObject turn = new JSONObject("{id:'turn',user_id:'alice',backend:'codex',status:'running',part_start:0,input_end:1}")
                    .put("part_end", parts.length());
            JSONObject chat = new JSONObject("{id:'chat-one',title:'Chat',project_id:'project-one',backend:'codex',can_control:true,status:'running'}")
                    .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", parts));
            h.set("chat", chat); h.call("render");
            View history = history(h, markwon);
            assertEquals(1, occurrences(history, "Inspect parser"));
            parts.put(new JSONObject("{role:'assistant',kind:'text',phase:'final_answer',text:'Main report'}")).put(second);
            turn.put("part_end", parts.length()); h.call("renderChat"); history = history(h, markwon);
            assertEquals(1, occurrences(history, "Main report"));
            assertEquals(1, occurrences(history, "Inspect parser")); assertEquals(1, occurrences(history, "Inspect schema"));
            assertEquals(0, occurrences(history, "hidden-work.txt")); assertEquals(0, occurrences(history, "Also inspect tests"));
            toggleProcess(history); history = history(h, markwon);
            assertEquals(1, occurrences(history, "hidden-work.txt")); assertEquals(1, occurrences(history, "Also inspect tests"));
            assertEquals(1, occurrences(history, "notes.txt"));
            assertEquals(1, occurrences(history, "Inspect parser")); assertEquals(1, occurrences(history, "Inspect schema"));
            toggleProcess(history); history = history(h, markwon);
            first.getJSONObject("tool").put("status", "completed").getJSONObject("task").put("summary", "Parser checked");
            h.call("renderChat"); history = history(h, markwon);
            assertEquals(1, occurrences(history, "Parser checked")); assertEquals(1, occurrences(history, "Inspect schema"));
            assertEquals(0, occurrences(history, "hidden-work.txt"));
            second.getJSONObject("tool").put("status", "completed"); turn.put("status", "completed"); chat.put("status", "completed");
            h.call("renderChat"); history = history(h, markwon);
            assertEquals(1, occurrences(history, "Main report"));
            assertEquals(0, occurrences(history, "Inspect parser")); assertEquals(0, occurrences(history, "Inspect schema"));
            toggleProcess(history); history = history(h, markwon);
            assertEquals(1, occurrences(history, "Inspect parser")); assertEquals(1, occurrences(history, "Inspect schema"));
        }
    }
}
