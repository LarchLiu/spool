package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import android.text.Spannable;
import android.text.Spanned;
import android.view.View;
import java.time.Duration;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowToast;
import org.robolectric.util.ReflectionHelpers;

/** Actual Activity timer/REST/SSE reconciliation; only the gomobile read boundary is replaced. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityRefreshTest {
    private final long startedAt = System.currentTimeMillis() - 5000;
    private String envelope(int status, JSONObject value) {
        return NativeChatState.put(NativeChatState.put(new JSONObject(), "status", status), "body", value.toString()).toString();
    }
    private JSONObject chat(String status, int timestamp) throws Exception {
        JSONArray parts = new JSONArray().put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Hello"));
        if (!status.equals("running")) parts.put(new JSONObject().put("role", "assistant").put("kind", "text").put("text", "Finished reply"));
        JSONObject turn = new JSONObject().put("id", "server-turn").put("user_id", "alice").put("backend", "codex")
                .put("status", status).put("started_at", startedAt).put("finished_at", status.equals("running") ? 0 : startedAt + 5000)
                .put("part_start", 0).put("input_end", 1).put("part_end", parts.length());
        return new JSONObject().put("id", "chat-one").put("title", "Hello").put("project_id", "project-one").put("backend", "codex")
                .put("can_control", true).put("updated_at", timestamp).put("status", status)
                .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", parts));
    }
    private void start(MainActivityGoalTest.Harness h) throws Exception {
        JSONObject running = chat("running", 20);
        h.set("chat", running); h.set("chats", new JSONArray().put(NativeChatState.copy(running)).put(new JSONObject().put("id", "other-chat")));
        h.call("renderChat"); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, h.refreshIO.jobs.size());
    }
    private String kind(MainActivityGoalTest.Harness h) { return ReflectionHelpers.getField(h.composer().send, "kind"); }
    private void tick() { Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1000)); }

    private void capture(MainActivityGoalTest.Harness h, String name) throws Exception {
        View root = h.get("root");
        root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, 360, 800);
        android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(360, 800, android.graphics.Bitmap.Config.ARGB_8888);
        root.draw(new android.graphics.Canvas(image));
        try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(System.getProperty("spool.ui.artifacts"), name + ".png"))) {
            assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
        } finally { image.recycle(); }
    }

    @Test public void missedTerminalSseIsRecoveredByTheImmediateGetAndUpdatesTheSidebarWithoutReplacingTheEditor() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.call("openSidebar");
            NativeUi.Composer editor = h.composer(); editor.input.setText("Next draft"); assertTrue(editor.input.requestFocus());
            Spannable document = editor.input.getText(); Object composition = new Object();
            document.setSpan(composition, 0, 4, Spanned.SPAN_COMPOSING | Spanned.SPAN_EXCLUSIVE_EXCLUSIVE); editor.input.setSelection(4);
            assertEquals("stop", kind(h));
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("completed", 30)); h.refreshIO.run();
            assertEquals("completed", h.<JSONObject>get("chat").getString("status")); assertEquals("send", kind(h));
            assertEquals("completed", h.<JSONArray>get("chats").getJSONObject(0).getString("status"));
            assertEquals("other-chat", h.<JSONArray>get("chats").getJSONObject(1).getString("id"));
            NativeSidebar sidebar = h.get("sidebar");
            for (NativeChatLink row : sidebar.chatRows) if (row.open.getTag().toString().endsWith(":chat-one")) assertNull(row.spinner);
            assertSame(editor, h.composer()); assertSame(document, editor.input.getText());
            assertEquals("Next draft", editor.input.getText().toString()); assertEquals(4, editor.input.getSelectionStart());
            assertEquals(0, document.getSpanStart(composition)); assertEquals(4, document.getSpanEnd(composition)); assertTrue(editor.input.hasFocus());
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals(List.of("GET", "/api/chats/chat-one", "", "test-csrf", "en"), call);
            tick(); tick(); assertTrue(h.refreshIO.jobs.isEmpty()); assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void unchangedAndOlderSnapshotsKeepTranscriptViewsScrollAndNativeSelection() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, false)) {
            start(h); android.widget.LinearLayout history = h.get("historyContent"); View row = history.getChildAt(0);
            JSONObject before = h.get("chat"); NativeUi.Composer editor = h.composer(); editor.input.setText("Next draft"); editor.input.setSelection(2);
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("running", 20)); h.refreshIO.run();
            assertSame(before, h.get("chat")); assertSame(row, history.getChildAt(0)); assertEquals(2, editor.input.getSelectionStart());
            tick(); assertEquals(1, h.refreshIO.jobs.size());
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("running", 10)); h.refreshIO.run();
            assertSame(before, h.get("chat")); assertSame(row, history.getChildAt(0)); assertEquals("stop", kind(h));
            assertEquals(20, h.<JSONObject>get("chat").getLong("updated_at")); assertEquals("Next draft", editor.input.getText().toString());
            h.call("renderChat"); h.call("startChat"); assertTrue(h.refreshIO.jobs.isEmpty());
        }
    }

    @Test public void aTerminalSseRevokesAnAlreadyReadPollResultBeforeItCanReviveTheTurn() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.call("startChat");
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("running", 20));
            MainActivityGoalTest.ShadowClient.chatListener.onEvent("snapshot", chat("stopped", 40).toString());
            h.refreshIO.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("stopped", h.<JSONObject>get("chat").getString("status"));
            assertEquals(40, h.<JSONObject>get("chat").getLong("updated_at")); assertEquals("send", kind(h));
            tick(); assertTrue(h.refreshIO.jobs.isEmpty());
        }
    }

    @Test public void navigationAndLogoutRevokeQueuedAndInFlightRefreshesBeforeTheyTouchAnotherChat() throws Exception {
        for (boolean alreadyRead : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); MainActivityGoalTest.ShadowClient.response = envelope(200, chat("completed", 30));
            Runnable request = h.refreshIO.jobs.remove(0); if (alreadyRead) request.run();
            ReflectionHelpers.callInstanceMethod(h.activity, "openChat", ReflectionHelpers.ClassParameter.from(String.class, "other-chat"));
            if (!alreadyRead) request.run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("other-chat", h.<String>get("chatID")); assertNull(h.get("chat")); assertFalse(h.composer().input.isEnabled());
            assertEquals(alreadyRead ? 1 : 0, MainActivityGoalTest.ShadowClient.calls.size());
            tick(); assertTrue(h.refreshIO.jobs.isEmpty());
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.call("clearWorkspace"); h.refreshIO.run(); tick();
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertNull(h.get("user")); assertNull(h.get("chat"));
        }
    }

    @Test public void backgroundStopsRefreshAndForegroundRestartsImmediatelyForTheSameRunningChat() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.controller.pause().stop(); h.refreshIO.run(); tick(); tick();
            assertTrue(h.refreshIO.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            h.controller.start().resume(); assertEquals(1, h.refreshIO.jobs.size());
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("completed", 30)); h.refreshIO.run();
            assertEquals("completed", h.<JSONObject>get("chat").getString("status")); assertEquals("send", kind(h));
        }
    }

    @Test public void offlineAndMalformedResponsesStaySilentButAuthExpiryRevokesTheSession() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.set("goalError", "Existing inline error"); int toasts = ShadowToast.shownToastCount();
            for (String response : new String[] {envelope(503, new JSONObject().put("error", "Offline")), "malformed",
                    envelope(200, chat("completed", 30).put("id", "wrong-chat"))}) {
                MainActivityGoalTest.ShadowClient.response = response; h.refreshIO.run();
                assertEquals("Existing inline error", h.<String>get("goalError")); assertEquals(toasts, ShadowToast.shownToastCount());
                assertEquals("running", h.<JSONObject>get("chat").getString("status")); tick();
            }
            MainActivityGoalTest.ShadowClient.response = envelope(401, new JSONObject().put("error", "Expired")); h.refreshIO.run();
            assertNull(h.get("user")); assertNull(h.get("chat")); assertNull(h.get("composerView"));
            tick(); assertTrue(h.refreshIO.jobs.isEmpty()); assertEquals(toasts, ShadowToast.shownToastCount());
        }
    }

    @Test public void sessionRebindingSkipsQueuedOldReadsAndAnyRunningTurnEnablesRefresh() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.set("authEpoch", h.<Integer>get("authEpoch") + 1); h.set("csrf", "new-csrf"); h.call("render");
            assertEquals(2, h.refreshIO.jobs.size()); h.refreshIO.jobs.remove(0).run();
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("completed", 30)); h.refreshIO.run();
            assertEquals("new-csrf", MainActivityGoalTest.ShadowClient.calls.get(0).get(3));
            JSONObject earlier = chat("completed", 40); earlier.getJSONArray("turns").put(0,
                    chat("running", 20).getJSONArray("turns").getJSONObject(0));
            earlier.getJSONArray("turns").put(new JSONObject().put("id", "last-completed").put("status", "completed").put("part_start", 2).put("input_end", 2).put("part_end", 2));
            h.set("chat", earlier); h.call("renderChat"); assertEquals(1, h.refreshIO.jobs.size());
            assertNull(NativeChatState.runningTurn(earlier)); assertTrue(NativeChatState.hasRunningTurn(earlier));
        }
    }

    @Test public void aStopAcknowledgedAfterNavigationUpdatesOnlyItsOwnSidebarConversation() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.call("sendOrStop");
            ReflectionHelpers.callInstanceMethod(h.activity, "openChat", ReflectionHelpers.ClassParameter.from(String.class, "other-chat"));
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("stopped", 30));
            h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("other-chat", h.<String>get("chatID")); assertNull(h.get("chat"));
            assertEquals("stopped", h.<JSONArray>get("chats").getJSONObject(0).getString("status"));
            assertEquals("other-chat", h.<JSONArray>get("chats").getJSONObject(1).getString("id"));
            assertTrue(h.<java.util.Set<?>>get("stoppingTurns").isEmpty());
        }
    }

    @Test public void productionBeforeAndAfterTerminalRefreshFramesRemainInspectableInBothThemes() throws Exception {
        for (boolean dark : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("theme", dark ? "dark" : "light"); h.call("render"); start(h);
            h.composer().input.setText("Next draft"); String prefix = "native-refresh-" + (dark ? "dark" : "light");
            if (android.os.Build.VERSION.SDK_INT == 35) capture(h, prefix + "-running");
            MainActivityGoalTest.ShadowClient.response = envelope(200, chat("completed", 30)); h.refreshIO.run();
            if (android.os.Build.VERSION.SDK_INT == 35) capture(h, prefix + "-completed");
            assertEquals("Next draft", h.composer().input.getText().toString()); assertEquals("send", kind(h));
        }
    }
}
