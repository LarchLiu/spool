package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.view.inputmethod.EditorInfo;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Real Activity editing/optimistic/stop lifecycle with a deterministic gomobile boundary. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivitySubmissionTest {
    private String envelope(int status, JSONObject value) {
        return NativeChatState.put(NativeChatState.put(new JSONObject(), "status", status), "body", value.toString()).toString();
    }
    private JSONObject server(String id, String status) throws Exception {
        boolean idle = status.equals("idle");
        JSONArray parts = new JSONArray();
        if (!idle) {
            parts.put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Hello"));
            parts.put(new JSONObject().put("role", "assistant").put("kind", "text").put("text", "Partial reply"));
        }
        JSONArray turns = new JSONArray();
        if (!idle) turns.put(new JSONObject().put("id", "server-turn").put("user_id", "alice").put("status", status)
                .put("backend", "codex").put("started_at", 10).put("part_start", 0).put("input_end", 1).put("part_end", 2));
        return new JSONObject().put("id", id).put("project_id", "project-one").put("backend", "codex").put("can_control", true)
                .put("status", status).put("updated_at", idle ? 10 : status.equals("running") ? 20 : 30).put("turns", turns)
                .put("transcript", new JSONObject().put("parts", parts));
    }
    private void respond(String id) {
        MainActivityGoalTest.ShadowClient.responder = call -> {
            try {
                if (call.get(1).equals("/api/chats")) return envelope(200, server(id, "idle"));
                return envelope(200, server(id, call.get(1).endsWith("/stop") ? "stopped" : "running"));
            } catch (Exception error) { throw new AssertionError(error); }
        };
    }
    private String kind(NativeUi.Composer composer) { return ReflectionHelpers.getField(composer.send, "kind"); }
    private void attach(MainActivityGoalTest.Harness h) throws Exception {
        attach(h, "note.txt", "text/plain", new byte[] {65, 66});
    }
    private void attach(MainActivityGoalTest.Harness h, String name, String mediaType, byte[] bytes) throws Exception {
        Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
        Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
        Object file = ReflectionHelpers.callConstructor(type,
                ReflectionHelpers.ClassParameter.from(String.class, name), ReflectionHelpers.ClassParameter.from(String.class, mediaType),
                ReflectionHelpers.ClassParameter.from(byte[].class, bytes));
        List<Object> files = ReflectionHelpers.getField(draft, "files"); files.add(file); h.call("renderFiles");
    }
    private void event(String kind, JSONObject value) {
        assertNotNull(MainActivityGoalTest.ShadowClient.chatListener);
        MainActivityGoalTest.ShadowClient.chatListener.onEvent(kind, value.toString());
    }

    @Test public void sendImmediatelyRendersThePromptAndKeepsTheSameWritableEditorThroughAcknowledgement() throws Exception {
        for (boolean existing : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(existing)) {
            respond(existing ? "chat-one" : "created-one");
            NativeUi.Composer composer = h.composer();
            android.view.View root = h.get("root");
            root.measure(android.view.View.MeasureSpec.makeMeasureSpec(360, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(800, android.view.View.MeasureSpec.EXACTLY));
            root.layout(0, 0, 360, 800);
            assertTrue("Attached editor must take focus before sending", composer.input.requestFocus());
            assertTrue(composer.input.hasFocus());
            h.send("  Hello\n");
            assertTrue("Optimistic rendering must retain focus", composer.input.hasFocus());
            assertEquals("", composer.input.getText().toString()); assertTrue(composer.input.isEnabled());
            assertEquals("stop", kind(composer)); assertTrue(composer.send.isEnabled());
            assertFalse(composer.model.isEnabled()); assertFalse(composer.permission.isEnabled()); assertFalse(composer.add.isEnabled());
            JSONObject pending = h.get("chat");
            assertTrue(NativeChatState.runningTurn(pending).getString("id").startsWith("pending-"));
            assertEquals("Hello", pending.getJSONObject("transcript").getJSONArray("parts").getJSONObject(0).getString("text"));
            assertTrue(composer.input.onCreateInputConnection(new EditorInfo()).commitText("Next draft", 1));
            Object draftAfterSend = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            attach(h);
            assertTrue("IME document edits must retain focus", composer.input.hasFocus());
            h.io.run();
            assertSame(composer, h.composer());
            assertTrue("Acknowledgement moved focus to " + h.activity.getCurrentFocus() + "; attached=" + composer.input.isAttachedToWindow()
                    + "; enabled=" + composer.input.isEnabled() + "; visibility=" + composer.input.getVisibility(), composer.input.hasFocus());
            assertEquals("Next draft", composer.input.getText().toString());
            assertSame(draftAfterSend, ReflectionHelpers.callInstanceMethod(h.activity, "draft"));
            assertEquals(1, composer.attachments.getChildCount());
            JSONObject accepted = h.get("chat");
            assertEquals("server-turn", NativeChatState.runningTurn(accepted).getString("id")); assertEquals(1, accepted.getJSONArray("turns").length());
            assertFalse(h.<Boolean>get("busy"));
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(existing ? 0 : 1);
            assertEquals(existing ? "/api/chats/chat-one/messages" : "/api/chats/created-one/messages", call.get(1));
            assertEquals("  Hello\n", new JSONObject(call.get(2)).getString("message"));
        }
    }

    @Test public void immediateStopWaitsForTheAcceptedServerTurnAndDeduplicatesRepeatedClicks() throws Exception {
        for (boolean existing : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(existing)) {
            respond(existing ? "chat-one" : "created-one"); h.send("Hello");
            h.call("sendOrStop"); h.call("sendOrStop");
            assertEquals(1, h.io.jobs.size()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            assertEquals("spinner", kind(h.composer())); assertFalse(h.composer().send.isEnabled());
            assertEquals("Stopping…", h.composer().send.getContentDescription());
            assertEquals(h.composer().send.getPaddingLeft(), ((NativeUi) h.get("ui")).dp(7));
            h.io.run();
            assertEquals("spinner", kind(h.composer()));
            // One stop plus the Web-equivalent profiles/navigation/agents refresh.
            assertEquals(4, h.io.jobs.size());
            h.io.jobs.remove(0).run(); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            List<List<String>> stops = new java.util.ArrayList<>();
            for (List<String> call : MainActivityGoalTest.ShadowClient.calls) if (call.get(1).endsWith("/stop")) stops.add(call);
            assertEquals(1, stops.size());
            assertEquals("server-turn", new JSONObject(stops.get(0).get(2)).getString("turn_id"));
            assertEquals("send", kind(h.composer())); assertNull(NativeChatState.runningTurn(h.get("chat")));
            assertEquals("Partial reply", h.<JSONObject>get("chat").getJSONObject("transcript").getJSONArray("parts").getJSONObject(1).getString("text"));
        }
    }

    @Test public void aStopRequestedDuringAForkTargetsTheReturnedChatAndNeverThePendingId() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            respond("forked-chat"); h.send("Hello"); h.call("sendOrStop"); h.io.run();
            assertEquals("forked-chat", h.<String>get("chatID"));
            h.io.jobs.remove(0).run(); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(1);
            assertEquals("/api/chats/forked-chat/stop", call.get(1));
            assertEquals("server-turn", new JSONObject(call.get(2)).getString("turn_id"));
        }
    }

    @Test public void directStopPreservesEditingDoesNotReviveFinishedTurnsAndReportsErrorsInline() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            respond("chat-one"); h.set("chat", server("chat-one", "running")); h.call("renderChat");
            h.composer().input.setText("Next question");
            assertEquals("stop", kind(h.composer())); assertEquals(((NativeUi) h.get("ui")).dp(9), h.composer().send.getPaddingLeft());
            h.call("sendOrStop"); h.call("sendOrStop"); assertEquals(1, h.io.jobs.size());
            assertTrue(h.composer().input.isEnabled()); h.io.run(); assertEquals("Next question", h.composer().input.getText().toString());
            JSONObject finished = h.get("chat");
            h.set("chat", NativeChatState.reconcile(finished, server("chat-one", "running"))); h.call("renderChat");
            assertEquals("send", kind(h.composer()));
            h.set("chat", server("chat-one", "running")); h.call("renderChat");
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(409, NativeChatState.put(new JSONObject(), "error", "Stop failed"));
            h.call("sendOrStop"); h.io.run();
            assertEquals("Stop failed", h.<String>get("goalError")); assertEquals("stop", kind(h.composer())); assertTrue(h.composer().send.isEnabled());
        }
    }

    @Test public void attachmentUploadRestoresTheExactSubmittedDraftOnFailureLikeWeb() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            attach(h); assertEquals(1, h.composer().attachments.getChildCount());
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(409, NativeChatState.put(new JSONObject(), "error", "Upload failed"));
            h.send("  Hello\n");
            assertEquals(0, h.composer().attachments.getChildCount());
            assertEquals(1, NativeChatState.runningTurn(h.get("chat")).getJSONArray("attachments").length());
            assertEquals(2, h.<JSONObject>get("chat").getJSONObject("transcript").getJSONArray("parts").length());
            h.composer().input.setText("New text while uploading"); h.io.run();
            assertEquals("  Hello\n", h.composer().input.getText().toString());
            assertEquals(1, h.composer().attachments.getChildCount()); assertEquals("Upload failed", h.<String>get("goalError"));
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals("UPLOAD", call.get(0)); JSONObject payload = new JSONObject(call.get(2));
            assertEquals("  Hello\n", payload.getJSONObject("fields").getString("message"));
            assertEquals("QUI=", payload.getJSONArray("files").getJSONObject(0).getString("data"));
        }
    }

    @Test public void aFailedPendingStopDoesNotCallStopAndNewChatCreationFailureRestoresTheDraft() throws Exception {
        for (boolean creationFails : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(!creationFails)) {
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return call.get(0).equals("GET") ? envelope(200, server("chat-one", "idle"))
                        : envelope(503, NativeChatState.put(new JSONObject(), "error", "Unavailable")); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.send("Original draft"); h.call("sendOrStop"); h.io.run();
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
            assertFalse(MainActivityGoalTest.ShadowClient.calls.get(0).get(1).endsWith("/stop"));
            assertEquals("Original draft", h.composer().input.getText().toString());
            assertEquals(creationFails ? "send" : "stop", kind(h.composer())); assertFalse(h.<Boolean>get("busy"));
            assertEquals("Unavailable", h.<String>get("goalError"));
            if (creationFails) { assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat")); }
            else { h.io.run(); assertEquals("send", kind(h.composer())); }
        }
    }

    @Test public void failedPostKeepsNewerSseStateUntilTheAuthoritativeRecoveryRead() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.call("startChat"); h.send("Submitted text");
            JSONObject live = server("chat-one", "completed").put("updated_at", 40).put("title", "Live title");
            event("snapshot", live);
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return call.get(0).equals("GET") ? envelope(200, server("chat-one", "idle"))
                        : envelope(409, NativeChatState.put(new JSONObject(), "error", "Rejected")); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.composer().input.setText("A newer draft"); h.io.run();
            assertEquals("Submitted text", h.composer().input.getText().toString());
            assertEquals("Live title", h.<JSONObject>get("chat").getString("title"));
            assertEquals(40, h.<JSONObject>get("chat").getLong("updated_at"));
            assertEquals("Partial reply", h.<JSONObject>get("chat").getJSONObject("transcript").getJSONArray("parts").getJSONObject(1).getString("text"));
            assertNull(h.get("optimisticSend")); assertNull(h.get("pendingSend"));
            assertEquals(1, h.io.jobs.size()); h.io.run();
            assertEquals("GET", MainActivityGoalTest.ShadowClient.calls.get(1).get(0));
            assertEquals("/api/chats/chat-one", MainActivityGoalTest.ShadowClient.calls.get(1).get(1));
            assertEquals("idle", h.<JSONObject>get("chat").getString("status"));
            assertEquals("Rejected", h.<String>get("goalError"));
            assertEquals("Submitted text", h.composer().input.getText().toString());
        }
    }

    @Test public void failedRecoveryReadLeavesLiveStateAndTheOriginalPostErrorVisible() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.call("startChat"); h.send("Retry this");
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(503,
                    NativeChatState.put(new JSONObject(), "error", call.get(0).equals("GET") ? "Recovery unavailable" : "Send unavailable"));
            h.io.run();
            assertNotNull(h.get("optimisticSend")); assertNull(h.get("pendingSend"));
            JSONObject live = server("chat-one", "stopped").put("updated_at", 50);
            event("snapshot", live); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            assertNull(h.get("optimisticSend"));
            h.io.run();
            assertEquals("stopped", h.<JSONObject>get("chat").getString("status"));
            assertEquals(50, h.<JSONObject>get("chat").getLong("updated_at"));
            assertEquals("Send unavailable", h.<String>get("goalError"));
            assertEquals("Retry this", h.composer().input.getText().toString());
        }
    }

    @Test public void successfulCreationThenFailedUploadRestoresFilesUnderTheAdoptedChat() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            attach(h);
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return call.get(1).equals("/api/chats") || call.get(0).equals("GET")
                        ? envelope(200, server("created-one", "idle"))
                        : envelope(409, NativeChatState.put(new JSONObject(), "error", "Upload failed")); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.send("  Original upload\n"); h.composer().input.setText("Newer text");
            NativeSubmission<?> submitted = h.get("pendingSend"); h.io.run();
            assertEquals("created-one", h.<String>get("chatID"));
            assertEquals("created-one", h.<JSONObject>get("chat").getString("id"));
            assertEquals("  Original upload\n", h.composer().input.getText().toString());
            assertEquals(1, h.composer().attachments.getChildCount());
            assertSame(submitted, h.get("optimisticSend")); assertNull(h.get("pendingSend"));
            java.util.Map<?, ?> drafts = h.get("drafts");
            assertFalse(drafts.containsKey("new/project-one")); assertTrue(drafts.containsKey("created-one"));
            h.io.run();
            assertEquals("/api/chats/created-one", MainActivityGoalTest.ShadowClient.calls.get(2).get(1));
            assertNull(h.get("optimisticSend")); assertEquals("send", kind(h.composer()));
            assertEquals(1, h.composer().attachments.getChildCount());
        }
    }

    @Test public void failedOptimisticImageStaysLocalEvenAfterRemovingItsRestoredDraftAttachment() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("fileImages", new NativeImages(task -> {}, path -> { throw new AssertionError("A pending image cannot use an API URL"); }));
            byte[] image = java.util.Base64.getDecoder().decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=");
            attach(h, "pixel.png", "image/png", image);
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return call.get(0).equals("GET") ? envelope(200, server("chat-one", "idle"))
                        : envelope(409, NativeChatState.put(new JSONObject(), "error", "Upload failed")); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.send("Image"); h.io.run();
            NativeAttachmentCard draft = (NativeAttachmentCard) h.composer().attachments.getChildAt(0);
            String local = draft.image.getTag().toString(); assertTrue(local.startsWith("local:"));
            assertTrue(draft.remove.performClick()); assertEquals(0, h.composer().attachments.getChildCount());
            h.call("renderChat");
            android.widget.LinearLayout history = h.get("historyContent");
            NativeUserRow row = (NativeUserRow) history.getChildAt(0);
            NativeAttachmentCard optimistic = (NativeAttachmentCard) row.attachments.getChildAt(0);
            assertEquals(local, optimistic.image.getTag()); assertTrue(optimistic.open.performClick());
            NativeImagePreview preview = h.get("imagePreview");
            assertTrue(preview.content.image.getTag().toString().startsWith(local.substring(0, local.indexOf('\n')) + "\n"));
            h.call("closeImagePreview"); h.io.run();
            assertNull(h.get("optimisticSend")); assertEquals("idle", h.<JSONObject>get("chat").getString("status"));
        }
    }

    @Test public void failureAndRecoveryCallbacksCannotRestoreIntoAnotherChatOrRevokedSession() throws Exception {
        for (boolean recovery : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return call.get(0).equals("GET") ? envelope(200, server("chat-one", "idle"))
                        : envelope(409, NativeChatState.put(new JSONObject(), "error", "Rejected")); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.send("Original"); if (recovery) h.io.run();
            h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1);
            h.set("chatID", "other-chat"); h.set("chat", server("other-chat", "idle"));
            h.set("goalError", ""); h.composer().input.setText("Other draft"); h.io.run();
            assertEquals("other-chat", h.<JSONObject>get("chat").getString("id"));
            assertEquals("Other draft", h.composer().input.getText().toString()); assertEquals("", h.<String>get("goalError"));
            assertTrue(h.io.jobs.isEmpty());
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(401, NativeChatState.put(new JSONObject(), "error", "Expired"));
            h.send("Private draft"); h.io.run();
            assertNull(h.get("user")); assertNull(h.get("pendingSend")); assertNull(h.get("optimisticSend"));
            assertTrue(h.io.jobs.isEmpty()); assertNull(h.get("composerView"));
        }
    }

    @Test public void invalidAcceptedChatResponsesRestoreTheDraftAndRecoverWithoutSendingAStop() throws Exception {
        for (boolean existing : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(existing)) {
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(200, new JSONObject());
            h.send("Original draft"); h.call("sendOrStop"); h.io.run();
            assertEquals("Original draft", h.composer().input.getText().toString());
            assertEquals("The server returned an invalid response", h.<String>get("goalError"));
            assertNull(h.get("pendingSend")); assertTrue(h.<java.util.Set<?>>get("stoppingTurns").isEmpty());
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
            if (!existing) { assertNull(h.get("chat")); assertTrue(h.io.jobs.isEmpty()); }
            else assertEquals(1, h.io.jobs.size());
        }
    }

    @Test public void revokedQueuedMessagesAndLateAcknowledgementsCannotTouchAnotherSelection() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            respond("chat-one"); h.send("Hello"); h.call("clearWorkspace"); h.io.run();
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertNull(h.get("pendingSend"));
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            respond("chat-one"); h.send("Hello"); h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1);
            h.set("chatID", "other-chat"); h.set("chat", server("other-chat", "idle"));
            h.composer().input.setText("Other draft"); h.io.run();
            assertEquals("other-chat", h.<String>get("chatID")); assertEquals("Other draft", h.composer().input.getText().toString());
            assertNull(h.get("pendingSend")); assertFalse(h.<Boolean>get("busy"));
        }
    }
}
