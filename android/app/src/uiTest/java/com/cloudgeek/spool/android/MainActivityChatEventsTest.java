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
import org.robolectric.util.ReflectionHelpers;

/** Real Activity navigation/render/lifecycle, replacing only the gomobile transport. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityChatEventsTest {
    private JSONObject chat(String id, int version) throws Exception {
        return new JSONObject().put("id", id).put("title", "Chat " + version).put("project_id", "project-one").put("backend", "codex")
                .put("model", "model-one").put("reasoning_effort", "medium").put("permission_mode", "ask").put("can_control", true)
                .put("status", "completed").put("updated_at", version)
                .put("turns", new JSONArray("[{id:'turn-one',status:'completed',backend:'codex',part_start:0,input_end:1,part_end:1}]"))
                .put("transcript", new JSONObject("{parts:[{role:'user',kind:'text',text:'Hello'}]}"));
    }
    private String envelope(int status, JSONObject data) {
        return NativeChatState.put(NativeChatState.put(new JSONObject(), "status", status), "body", data.toString()).toString();
    }
    private void open(MainActivityGoalTest.Harness h, String id) {
        ReflectionHelpers.callInstanceMethod(h.activity, "openChat", ReflectionHelpers.ClassParameter.from(String.class, id));
    }
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private void event(String kind, JSONObject value) { MainActivityGoalTest.ShadowClient.chatListener.onEvent(kind, value.toString()); idle(); }
    private void error(int status) throws Exception { event("transport_error", new JSONObject().put("status", status)); }
    private void retry() { Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(3000)); }
    private void run(MainActivityGoalTest.Harness h, int index) { h.io.jobs.remove(index).run(); idle(); }
    private void permissions(MainActivityGoalTest.Harness h) throws Exception {
        MainActivityGoalTest.ShadowClient.response = envelope(200, new JSONObject("{current:'ask',modes:[{id:'ask',available:true}]}"));
        while (!h.io.jobs.isEmpty()) run(h, 0);
    }
    private long reads(String path) { return MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).equals(path)).count(); }

    @Test public void healthyNavigationOpensSseImmediatelyAndItsFirstSnapshotInitializesTheActualUi() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two");
            assertEquals(List.of(List.of("chat-two", "en")), MainActivityGoalTest.ShadowClient.chatSubscriptions);
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            assertNull(h.get("chat")); assertFalse(h.composer().input.isEnabled());
            event("snapshot", chat("chat-two", 20));
            assertEquals("chat-two", h.<JSONObject>get("chat").getString("id")); assertTrue(h.composer().input.isEnabled());
            assertEquals("model-one", h.<String>get("model")); assertEquals("medium", h.<String>get("effort")); assertEquals("ask", h.<String>get("permission"));
            assertEquals("Chat 20", h.<android.widget.TextView>get("heading").getText().toString());
            permissions(h); assertEquals(0, reads("/api/chats/chat-two"));
            h.call("startChat"); h.call("render"); assertEquals(1, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
        }
    }

    @Test public void failedInitializationMakesOneFallbackAndKeepsMissingOrForbiddenChatSelectedWithAnInlineError() throws Exception {
        for (int status : new int[] {403, 404, 503}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); error(status); error(status);
            assertEquals(1, h.io.jobs.size());
            MainActivityGoalTest.ShadowClient.response = envelope(status, new JSONObject().put("error", "Cannot load selected chat")); run(h, 0);
            assertEquals("chat-two", h.<String>get("chatID")); assertNull(h.get("chat")); assertNotNull(h.get("user"));
            assertEquals("Cannot load selected chat", h.<String>get("goalError")); assertNotNull(h.composer().commands.error);
            assertEquals(1, reads("/api/chats/chat-two")); assertFalse(h.composer().input.isEnabled());
            retry(); error(0); MainActivityGoalTest.ShadowClient.response = envelope(200, chat("chat-two", 20)); run(h, 0);
            assertEquals("chat-two", h.<JSONObject>get("chat").getString("id")); assertTrue(h.composer().input.isEnabled());
            assertEquals(2, reads("/api/chats/chat-two"));
        }
    }

    @Test public void aReconnectedSseSnapshotCancelsAQueuedFallbackBeforeItTouchesTransport() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); error(0); retry();
            assertEquals(2, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
            event("snapshot", chat("chat-two", 30)); run(h, 0);
            assertEquals(0, reads("/api/chats/chat-two")); assertEquals(30, h.<JSONObject>get("chat").getInt("updated_at"));
            permissions(h); assertEquals(0, reads("/api/chats/chat-two"));
        }
    }

    @Test public void aNewSseSnapshotBeatsInFlightFallbackDataAndEvenALateAuthenticationFailure() throws Exception {
        for (int status : new int[] {200, 401, 404}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); error(0); retry();
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    MainActivityGoalTest.ShadowClient.chatListener.onEvent("snapshot", chat("chat-two", 30).toString());
                    return envelope(status, status == 200 ? chat("chat-two", 10) : new JSONObject().put("error", "Stale error"));
                } catch (Exception failure) { throw new AssertionError(failure); }
            };
            run(h, 0);
            assertEquals("chat-two", h.<String>get("chatID")); assertNotNull(h.get("user"));
            assertEquals(30, h.<JSONObject>get("chat").getInt("updated_at")); assertEquals("", h.<String>get("goalError"));
            assertTrue(h.composer().input.isEnabled());
        }
    }

    @Test public void navigationRevokesOldCallbacksAndQueuedReadsEvenAfterReturningToTheSameChatId() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); spoolnet.EventListener old = MainActivityGoalTest.ShadowClient.chatListener; error(0);
            open(h, "chat-three"); open(h, "chat-two");
            old.onEvent("snapshot", chat("chat-two", 50).toString()); old.onEvent("transport_error", "{status:401}");
            run(h, 0); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertNull(h.get("chat"));
            event("snapshot", chat("chat-two", 20)); assertEquals(20, h.<JSONObject>get("chat").getInt("updated_at"));
            assertEquals(3, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
        }
    }

    @Test public void initializedStreamFailureOnlyChecksAuthenticationAndDoesNotFetchHistoryAgain() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); event("snapshot", chat("chat-two", 20)); permissions(h);
            for (int status : new int[] {0, 403, 404}) {
                error(status); assertEquals(1, h.io.jobs.size());
                MainActivityGoalTest.ShadowClient.response = envelope(200, new JSONObject().put("id", "alice")); run(h, 0);
                assertEquals("chat-two", h.<String>get("chatID")); assertEquals(20, h.<JSONObject>get("chat").getInt("updated_at"));
                retry(); event("snapshot", chat("chat-two", 20));
            }
            assertEquals(3, reads("/api/me")); assertEquals(0, reads("/api/chats/chat-two"));
            error(401); MainActivityGoalTest.ShadowClient.response = envelope(401, new JSONObject().put("error", "Expired")); run(h, 0);
            assertNull(h.get("user")); assertNull(h.get("chat")); assertNull(h.get("composerView"));
        }
    }

    @Test public void initializationAuthenticationFailureExpiresButARevokedQueuedFailureCannot() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); error(401);
            MainActivityGoalTest.ShadowClient.response = envelope(401, new JSONObject().put("error", "Expired")); run(h, 0);
            assertNull(h.get("user")); assertNull(h.get("chat"));
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); error(401); open(h, "chat-three");
            MainActivityGoalTest.ShadowClient.response = envelope(401, new JSONObject().put("error", "Expired")); run(h, 0);
            assertNotNull(h.get("user")); assertEquals("chat-three", h.<String>get("chatID")); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void unchangedSnapshotsRetainTranscriptViewsAndTheNativeComposingEditor() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.call("startChat"); event("snapshot", chat("chat-one", 20));
            NativeUi.Composer editor = h.composer(); assertTrue(editor.input.requestFocus()); editor.input.setText("Next draft"); editor.input.setSelection(4);
            Spannable text = editor.input.getText(); Object composition = new Object(); text.setSpan(composition, 0, 4, Spanned.SPAN_COMPOSING | Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            android.widget.LinearLayout history = h.get("historyContent"); View row = history.getChildAt(0);
            event("snapshot", chat("chat-one", 20)); event("snapshot", chat("chat-one", 10));
            assertSame(row, history.getChildAt(0)); assertSame(editor, h.composer()); assertSame(text, editor.input.getText());
            assertEquals(4, editor.input.getSelectionStart()); assertEquals(0, text.getSpanStart(composition)); assertEquals(4, text.getSpanEnd(composition));
            assertTrue(editor.input.hasFocus()); assertEquals("Next draft", editor.input.getText().toString()); assertEquals(20, h.<JSONObject>get("chat").getInt("updated_at"));
            assertEquals(0, reads("/api/chats/chat-one"));
        }
    }

    @Test public void localeAndForegroundRebindingRejectOldFramesAndCancelTheirQueuedFallback() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two"); spoolnet.EventListener old = MainActivityGoalTest.ShadowClient.chatListener; error(0);
            h.set("language", "ja"); h.call("render");
            assertEquals(List.of("chat-two", "ja"), MainActivityGoalTest.ShadowClient.chatSubscriptions.get(1));
            old.onEvent("snapshot", chat("chat-two", 40).toString()); run(h, 0);
            assertNull(h.get("chat")); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            error(0); spoolnet.EventListener japanese = MainActivityGoalTest.ShadowClient.chatListener;
            h.controller.pause().stop(); run(h, 0); japanese.onEvent("snapshot", chat("chat-two", 30).toString()); idle();
            assertNull(h.get("chat")); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            h.controller.start().resume(); assertEquals(3, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
            event("snapshot", chat("chat-two", 20)); assertEquals(20, h.<JSONObject>get("chat").getInt("updated_at"));
        }
    }

    @Test public void liveMetadataUpdatesTheSidebarAndRevokesControlsWithoutErasingTranscriptOrTheDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("chats", new JSONArray().put(chat("chat-one", 10)).put(chat("chat-two", 10)));
            h.call("startChat"); event("snapshot", chat("chat-one", 20)); h.call("openSidebar");
            h.composer().input.setText("Preserved draft"); Object document = h.composer().input.getText();
            event("metadata", new JSONObject().put("id", "chat-one").put("updated_at", 30).put("title", "Renamed live")
                    .put("can_control", false).put("participants", new JSONArray("[{id:'alice'}]")));
            JSONObject current = h.get("chat");
            assertEquals("Renamed live", h.<android.widget.TextView>get("heading").getText().toString());
            assertEquals("Hello", current.getJSONObject("transcript").getJSONArray("parts").getJSONObject(0).getString("text"));
            assertFalse(current.getBoolean("can_control")); assertFalse(h.composer().input.isEnabled()); assertFalse(h.composer().send.isEnabled());
            assertSame(document, h.composer().input.getText()); assertEquals("Preserved draft", h.composer().input.getText().toString());
            assertEquals("Renamed live", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            assertEquals("Chat 10", h.<JSONArray>get("chats").getJSONObject(1).getString("title"));
            NativeSidebar sidebar = h.get("sidebar");
            assertTrue(sidebar.chatRows.stream().anyMatch(row -> row.open.getContentDescription().toString().contains("Renamed live")));
            assertEquals(0, reads("/api/chats/chat-one"));
        }
    }

    @Test public void malformedAndForeignSnapshotsCannotInitializeOrUnlockTheSelectedChat() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            open(h, "chat-two");
            MainActivityGoalTest.ShadowClient.chatListener.onEvent("snapshot", "not-json");
            event("snapshot", chat("chat-three", 30));
            assertNull(h.get("chat")); assertFalse(h.composer().input.isEnabled()); assertTrue(h.io.jobs.isEmpty());
            error(0); MainActivityGoalTest.ShadowClient.response = envelope(200, chat("chat-three", 30)); run(h, 0);
            assertNull(h.get("chat")); assertEquals("chat-two", h.<String>get("chatID")); assertNotNull(h.get("user"));
            assertEquals("Unexpected chat in initialization response", h.<String>get("goalError"));
            retry(); event("snapshot", chat("chat-two", 20));
            assertEquals("chat-two", h.<JSONObject>get("chat").getString("id")); assertTrue(h.composer().input.isEnabled());
        }
    }
}
