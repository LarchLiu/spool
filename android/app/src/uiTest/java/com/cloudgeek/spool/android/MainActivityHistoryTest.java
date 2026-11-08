package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.Looper;
import android.view.View;
import android.widget.LinearLayout;
import androidx.core.widget.NestedScrollView;
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

/** Actual history Views/API callbacks; no real account, server or native socket is used. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityHistoryTest {
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private JSONObject chat(String prefix, int count, String before, boolean more) throws Exception {
        JSONArray turns = new JSONArray(), parts = new JSONArray();
        for (int i = 0; i < count; i++) {
            int start = parts.length();
            parts.put(new JSONObject().put("role", "user").put("kind", "text").put("text", prefix + " prompt " + i));
            parts.put(new JSONObject().put("role", "assistant").put("kind", "text").put("text", prefix + " response " + i + "\n" + "More content.\n".repeat(3)));
            turns.put(new JSONObject().put("id", prefix + i).put("status", "completed").put("backend", "codex")
                    .put("part_start", start).put("input_end", start + 1).put("part_end", parts.length()));
        }
        return new JSONObject().put("id", "chat-one").put("title", "Current title").put("project_id", "project-one").put("backend", "codex")
                .put("can_control", true).put("status", "completed").put("updated_at", 30).put("turns", turns)
                .put("transcript", new JSONObject().put("source", "codex-native").put("parts", parts))
                .put("history", new JSONObject().put("before", before).put("has_more", more));
    }
    private String response(int status, JSONObject value) throws Exception {
        return new JSONObject().put("status", status).put("body", value.toString()).toString();
    }
    private void layout(MainActivityGoalTest.Harness h) {
        View root = h.get("root"); root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
    }
    private void paint(MainActivityGoalTest.Harness h) {
        layout(h); Bitmap image = Bitmap.createBitmap(360, 800, Bitmap.Config.ARGB_8888);
        h.<View>get("root").getViewTreeObserver().dispatchOnDraw();
        h.<View>get("root").draw(new Canvas(image)); image.recycle(); idle();
    }
    private MainActivityGoalTest.Queue prepare(MainActivityGoalTest.Harness h, JSONObject chat) {
        MainActivityGoalTest.Queue reads = new MainActivityGoalTest.Queue(); h.set("workspaceWorker", reads);
        h.call("stopHistory"); h.set("historyLoader", null);
        h.set("chat", chat); h.call("renderChat"); return reads;
    }
    private void open(MainActivityGoalTest.Harness h, String id) {
        ReflectionHelpers.callInstanceMethod(h.activity, "openChat", ReflectionHelpers.ClassParameter.from(String.class, id));
    }

    @Test public void automaticallyHydratesSequentiallyOnlyAfterPaintAndRetainsLiveMetadata() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 4, "middle/日本 ?&", true));
            assertTrue(reads.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            assertTrue(h.<LinearLayout>get("historyContent").getChildAt(0) instanceof NativeHistoryNotice);
            NativeHistoryNotice notice = (NativeHistoryNotice) h.<LinearLayout>get("historyContent").getChildAt(0);
            assertEquals("Loading earlier messages…", notice.loading.getText().toString()); assertEquals(1, notice.getChildCount());
            paint(h); assertEquals(1, reads.jobs.size()); h.call("renderChat"); paint(h); assertEquals(1, reads.jobs.size());
            MainActivityGoalTest.ShadowClient.response = response(200, chat("middle", 3, "oldest", true).put("updated_at", 1).put("title", "Old title"));
            reads.run(); assertEquals(7, h.<JSONObject>get("chat").getJSONArray("turns").length());
            assertEquals("Current title", h.<JSONObject>get("chat").getString("title")); assertEquals(30, h.<JSONObject>get("chat").getInt("updated_at"));
            assertTrue("The next page must wait for another paint", reads.jobs.isEmpty());
            assertEquals("/api/chats/chat-one/history?before=middle%2F%E6%97%A5%E6%9C%AC%20%3F%26", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            assertEquals(List.of("GET", "/api/chats/chat-one/history?before=middle%2F%E6%97%A5%E6%9C%AC%20%3F%26", "", "test-csrf", "en"), MainActivityGoalTest.ShadowClient.calls.get(0));
            paint(h); MainActivityGoalTest.ShadowClient.response = response(200, chat("oldest", 2, "", false)); reads.run(); paint(h);
            assertEquals(9, h.<JSONObject>get("chat").getJSONArray("turns").length()); assertTrue(reads.jobs.isEmpty());
            assertFalse(h.<LinearLayout>get("historyContent").getChildAt(0) instanceof NativeHistoryNotice);
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size()); assertEquals("oldest0", h.<JSONObject>get("chat").getJSONArray("turns").getJSONObject(0).getString("id"));
        }
    }

    @Test public void prependPreservesTheReadingAnchorAndEditorCompositionWhileFollowingStaysAtTheBottom() throws Exception {
        for (boolean following : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 9, "older", true)); paint(h);
            NestedScrollView scroll = h.get("history"); LinearLayout content = h.get("historyContent");
            assertEquals(content.getHeight() - scroll.getHeight(), scroll.getScrollY());
            if (!following) scroll.scrollTo(0, 140);
            h.composer().input.requestFocus(); android.view.inputmethod.InputConnection connection = h.composer().input.onCreateInputConnection(new android.view.inputmethod.EditorInfo());
            assertNotNull(connection); connection.setComposingText("日本語", 1); layout(h);
            NativeUi.Composer editor = h.composer(); Object text = editor.input.getText();
            NativeUserRow existing = null;
            for (int i = 0; i < content.getChildCount(); i++) if (content.getChildAt(i) instanceof NativeUserRow) { existing = (NativeUserRow) content.getChildAt(i); break; }
            assertNotNull(existing); int offset = existing.getTop() - scroll.getScrollY(); int oldTop = scroll.getScrollY();
            MainActivityGoalTest.ShadowClient.response = response(200, chat("older", 5, "", false)); reads.run(); paint(h);
            if (following) assertEquals(content.getHeight() - scroll.getHeight(), scroll.getScrollY());
            else {
                NativeUserRow restored = null;
                int seen = 0;
                for (int i = 0; i < content.getChildCount(); i++) if (content.getChildAt(i) instanceof NativeUserRow && seen++ == 5) { restored = (NativeUserRow) content.getChildAt(i); break; }
                assertNotNull(restored); assertEquals(offset, restored.getTop() - scroll.getScrollY()); assertTrue(scroll.getScrollY() > oldTop);
            }
            assertSame(editor, h.composer()); assertSame(text, editor.input.getText()); assertTrue(editor.input.hasFocus());
            assertEquals(0, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(editor.input.getText())); assertEquals(3, editor.input.getSelectionEnd());
        }
    }

    @Test public void invalidCursorAndNetworkErrorsStopAutomaticLoadingUntilTheVisibleRetry() throws Exception {
        for (int kind = 0; kind < 4; kind++) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 2, "older", true)); paint(h);
            JSONObject invalid = chat("invalid", 1, kind == 1 ? "" : "older", true);
            if (kind == 2) invalid.put("id", "other-chat");
            MainActivityGoalTest.ShadowClient.response = kind == 3 ? response(503, new JSONObject().put("error", "Offline")) : response(200, invalid);
            reads.run(); paint(h); assertTrue(reads.jobs.isEmpty());
            assertEquals(2, h.<JSONObject>get("chat").getJSONArray("turns").length());
            NativeHistoryNotice notice = (NativeHistoryNotice) h.<LinearLayout>get("historyContent").getChildAt(0);
            assertEquals("Retry loading earlier messages", notice.retry.getText().toString()); assertEquals(1, notice.getChildCount()); assertTrue(notice.retry.isFocusable());
            h.call("renderChat"); paint(h); assertTrue(reads.jobs.isEmpty());
            notice.retry.performClick(); assertTrue(reads.jobs.isEmpty()); paint(h); assertEquals(1, reads.jobs.size());
            MainActivityGoalTest.ShadowClient.response = response(200, chat("older", 1, "", false)); reads.run(); paint(h);
            assertEquals(3, h.<JSONObject>get("chat").getJSONArray("turns").length()); assertTrue(reads.jobs.isEmpty());
        }
    }

    @Test public void navigationRevokesUndrawnQueuedAndPostedHistoryEvenWhenReturningToTheSameChat() throws Exception {
        for (int stage = 0; stage < 3; stage++) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 2, "older", true));
            if (stage >= 1) paint(h);
            MainActivityGoalTest.ShadowClient.response = response(401, new JSONObject().put("error", "Late expiration"));
            if (stage == 2) reads.jobs.remove(0).run();
            List<Runnable> queued = new ArrayList<>(reads.jobs); reads.jobs.clear(); open(h, "other-chat"); open(h, "chat-one");
            for (Runnable task : queued) task.run(); idle(); paint(h);
            assertNotNull(h.get("user")); assertNull(h.get("chat")); assertEquals("chat-one", h.<String>get("chatID"));
            assertEquals(stage == 2 ? 1 : 0, MainActivityGoalTest.ShadowClient.calls.size()); assertTrue(reads.jobs.isEmpty());
        }
    }

    @Test public void liveCursorChangesFenceOldPagesAndDoNotLoseTheNewTranscript() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 3, "old", true)); paint(h);
            MainActivityGoalTest.ShadowClient.response = response(200, chat("stale", 2, "older", true)); reads.jobs.remove(0).run();
            h.set("chat", chat("live", 4, "new", true).put("updated_at", 40)); h.call("renderChat"); idle();
            assertEquals("live0", h.<JSONObject>get("chat").getJSONArray("turns").getJSONObject(0).getString("id"));
            paint(h); MainActivityGoalTest.ShadowClient.response = response(200, chat("older", 2, "", false)); reads.run(); paint(h);
            assertEquals(6, h.<JSONObject>get("chat").getJSONArray("turns").length()); assertEquals(40, h.<JSONObject>get("chat").getInt("updated_at"));
        }
    }

    @Test public void unavailableHistoryHasAStatusButNoRequestAndCurrentAuthenticationFailureExpires() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            JSONObject value = chat("recent", 2, "older", true); value.getJSONObject("history").put("error", "unavailable");
            MainActivityGoalTest.Queue reads = prepare(h, value); paint(h); assertTrue(reads.jobs.isEmpty());
            NativeHistoryNotice notice = (NativeHistoryNotice) h.<LinearLayout>get("historyContent").getChildAt(0);
            assertEquals("Native session history is unavailable", notice.unavailable.getText().toString());
            value.getJSONObject("history").remove("error"); h.call("renderChat"); paint(h);
            MainActivityGoalTest.ShadowClient.response = response(401, new JSONObject().put("error", "Expired")); reads.run();
            assertNull(h.get("user")); assertNull(h.get("chat")); assertNull(h.get("composerView"));
        }
    }

    @Test public void rebuildingTheWorkspaceMovesUndrawnWorkAndLocaleRebindingRevokesOldRequests() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 2, "older", true));
            h.call("render"); paint(h); assertEquals(1, reads.jobs.size());
            MainActivityGoalTest.ShadowClient.response = response(401, new JSONObject().put("error", "Old locale")); reads.jobs.remove(0).run();
            h.set("language", "ja"); h.call("render"); idle(); assertNotNull(h.get("user")); paint(h);
            MainActivityGoalTest.ShadowClient.response = response(200, chat("older", 1, "", false)); reads.run();
            assertEquals("ja", MainActivityGoalTest.ShadowClient.calls.get(1).get(4)); assertEquals(3, h.<JSONObject>get("chat").getJSONArray("turns").length());
        }
    }

    @Test public void aSameCursorLiveSnapshotDuringTheReadSurvivesPrependAndLaterStatusRefresh() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 3, "older", true)); paint(h);
            JSONObject live = chat("recent", 3, "older", true).put("updated_at", 50).put("title", "Live title");
            live.getJSONObject("transcript").getJSONArray("parts").getJSONObject(5).put("text", "Newest live output");
            ReflectionHelpers.callInstanceMethod(h.activity, "receiveChatEvent", ReflectionHelpers.ClassParameter.from(String.class, "snapshot"), ReflectionHelpers.ClassParameter.from(JSONObject.class, live));
            paint(h); assertEquals(1, reads.jobs.size());
            MainActivityGoalTest.ShadowClient.response = response(200, chat("older", 2, "", false).put("updated_at", 1)); reads.run(); paint(h);
            JSONObject hydrated = h.get("chat"); assertEquals("Live title", hydrated.getString("title")); assertEquals(50, hydrated.getInt("updated_at"));
            assertEquals(5, hydrated.getJSONArray("turns").length()); assertEquals("Newest live output", hydrated.getJSONObject("transcript").getJSONArray("parts").getJSONObject(9).getString("text"));
            live.put("updated_at", 60);
            ReflectionHelpers.callInstanceMethod(h.activity, "receiveChatEvent", ReflectionHelpers.ClassParameter.from(String.class, "snapshot"), ReflectionHelpers.ClassParameter.from(JSONObject.class, live));
            assertEquals(5, h.<JSONObject>get("chat").getJSONArray("turns").length()); assertFalse(h.<JSONObject>get("chat").getJSONObject("history").getBoolean("has_more"));
            paint(h); assertTrue("Hydrated cursor must not regress to the server's latest-page cursor", reads.jobs.isEmpty());
        }
    }

    @Test public void backgroundingRevokesUndrawnAndQueuedPagesAndPreservesFailureUntilExplicitRetry() throws Exception {
        for (boolean failed : new boolean[] {false,true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 2, "older", true));
            if (failed) {
                paint(h); MainActivityGoalTest.ShadowClient.response = response(503, new JSONObject().put("error", "Offline")); reads.run(); paint(h);
            }
            h.controller.pause().stop(); paint(h); assertTrue(reads.jobs.isEmpty());
            h.controller.start().resume();
            // The workspace batches remain separate from the history callback under test.
            assertEquals(3, reads.jobs.size()); List<Runnable> workspace = new ArrayList<>(reads.jobs); reads.jobs.clear();
            paint(h); assertEquals(failed ? 0 : 1, reads.jobs.size());
            assertEquals(failed, h.<NativeHistoryLoader<JSONObject>>get("historyLoader").failed());
            h.call("stopHistory"); reads.jobs.clear(); workspace.clear();
        }
    }

    @Test public void themeRecreationBeforePrependLayoutRetainsThePendingContentAnchor() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            MainActivityGoalTest.Queue reads = prepare(h, chat("recent", 9, "older", true)); paint(h);
            NestedScrollView scroll = h.get("history"); scroll.scrollTo(0, 150);
            int oldHeight = h.<LinearLayout>get("historyContent").getHeight(), oldTop = scroll.getScrollY();
            MainActivityGoalTest.ShadowClient.response = response(200, chat("older", 3, "", false)); reads.run();
            h.set("theme", "dark"); h.call("render"); paint(h);
            assertEquals(oldTop + h.<LinearLayout>get("historyContent").getHeight() - oldHeight, h.<NestedScrollView>get("history").getScrollY());
            assertFalse(h.<NativeHistoryScroll.State>get("historyScrollState").following);
            assertTrue(reads.jobs.isEmpty());
        }
    }
}
