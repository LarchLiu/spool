package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.widget.TextView;
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
public class MainActivityMarkwonTest {
    private static JSONObject page(String prefix, String before, boolean more) throws Exception {
        JSONArray parts = new JSONArray().put(new JSONObject().put("role", "user").put("kind", "text").put("text", prefix + " prompt"))
                .put(new JSONObject().put("role", "assistant").put("kind", "text").put("phase", "final_answer").put("text", (prefix + " **answer**.\n\n").repeat(20)));
        JSONObject turn = new JSONObject().put("id", prefix).put("backend", "codex").put("status", "completed")
                .put("part_start", 0).put("input_end", 1).put("part_end", 2);
        return new JSONObject().put("id", "chat-one").put("title", "Title").put("project_id", "project-one").put("backend", "codex")
                .put("can_control", true).put("status", "completed").put("turns", new JSONArray().put(turn))
                .put("transcript", new JSONObject().put("parts", parts))
                .put("history", new JSONObject().put("before", before).put("has_more", more));
    }

    private static void paint(MainActivityGoalTest.Harness h) {
        android.view.View root = h.get("root"); NativeMarkwonHistoryTest.layout(root);
        android.graphics.Bitmap bitmap = android.graphics.Bitmap.createBitmap(360, 800, android.graphics.Bitmap.Config.ARGB_8888);
        try { root.getViewTreeObserver().dispatchOnDraw(); root.draw(new android.graphics.Canvas(bitmap)); }
        finally { bitmap.recycle(); }
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
    }

    @Test public void longHistoryAutomaticallyLoadsEveryPageAfterPaintAndRetainsTheComposer() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.Queue reads = new MainActivityGoalTest.Queue();
            h.call("stopHistory"); h.set("historyLoader", null); h.set("workspaceWorker", reads);
            h.set("markwonTrial", true); h.set("chat", page("recent", "middle", true)); h.call("render");
            NativeMarkwonHistory recycler = h.get("markwonHistory"); NativeMarkwonHistoryTest.await(recycler);
            assertTrue("History must wait for a committed frame", reads.jobs.isEmpty());
            TextView composer = h.get("composer"); composer.setText("Keep this draft"); composer.requestFocus(); Object document = composer.getText();
            paint(h); assertEquals("Markwon must schedule the first earlier page", 1, reads.jobs.size());
            MainActivityGoalTest.ShadowClient.response = new JSONObject().put("status", 200).put("body", page("middle", "oldest", true).toString()).toString();
            reads.run(); NativeMarkwonHistoryTest.await(recycler);
            assertEquals(2, h.<JSONObject>get("chat").getJSONArray("turns").length());
            assertTrue("The next page must wait for another committed frame", reads.jobs.isEmpty());
            paint(h); assertEquals("History loading must continue after a Markwon prepend", 1, reads.jobs.size());
            MainActivityGoalTest.ShadowClient.response = new JSONObject().put("status", 200).put("body", page("oldest", "", false).toString()).toString();
            reads.run(); NativeMarkwonHistoryTest.await(recycler); paint(h);
            JSONObject chat = h.get("chat");
            assertEquals(3, chat.getJSONArray("turns").length()); assertFalse(chat.getJSONObject("history").getBoolean("has_more"));
            assertEquals("oldest", chat.getJSONArray("turns").getJSONObject(0).getString("id")); assertTrue(reads.jobs.isEmpty());
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
            assertEquals("/api/chats/chat-one/history?before=middle", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            assertEquals("/api/chats/chat-one/history?before=oldest", MainActivityGoalTest.ShadowClient.calls.get(1).get(1));
            assertSame(recycler, h.get("markwonHistory")); assertSame(composer, h.get("composer")); assertSame(document, composer.getText());
            assertEquals("Keep this draft", composer.getText().toString()); assertTrue(composer.hasFocus());
        }
    }

    @Test public void trialUsesTheProductionTranscriptAndKeepsComposerAcrossStreamedAnswers() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONArray parts = new JSONArray().put(new JSONObject("{role:'user',kind:'text',text:'Question'}"))
                    .put(new JSONObject("{role:'assistant',kind:'text',phase:'final_answer',text:'First **answer**.\\n\\nNext paragraph.'}"));
            JSONObject turn = new JSONObject("{id:'turn',user_id:'alice',backend:'codex',status:'running',part_start:0,input_end:1,part_end:2}");
            JSONObject chat = new JSONObject("{id:'chat-one',title:'Trial',project_id:'project-one',backend:'codex',can_control:true,status:'running'}")
                    .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", parts));
            h.set("chat", chat); h.set("markwonTrial", true); h.call("render");
            NativeMarkwonHistory recycler = h.get("markwonHistory");
            assertNotNull(recycler); assertNull(h.get("history"));
            NativeMarkwonHistoryTest.await(recycler);
            assertTrue(NativeMarkwonHistoryTest.texts(recycler).stream().anyMatch(t -> t.getText().toString().contains("First answer")));
            TextView composer = h.get("composer"); composer.setText("Unsent draft"); composer.requestFocus();
            Object document = composer.getText();
            parts.getJSONObject(1).put("text", "Updated **answer**.\n\nNext paragraph.");
            h.call("renderChat"); NativeMarkwonHistoryTest.await(recycler);
            assertSame(recycler, h.get("markwonHistory")); assertSame(composer, h.get("composer"));
            assertSame(document, composer.getText()); assertEquals("Unsent draft", composer.getText().toString());
            assertTrue(composer.hasFocus());
            assertTrue(NativeMarkwonHistoryTest.texts(recycler).stream().anyMatch(t -> t.getText().toString().contains("Updated answer")));
        }
    }
}
