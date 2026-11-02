package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import android.view.inputmethod.EditorInfo;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** Actual sidebar hit targets, failed-SSE hydration fallback races and ordinary-send callbacks. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityNavigationTest {
    private String envelope(int status, JSONObject value) {
        return NativeChatState.put(NativeChatState.put(new JSONObject(), "status", status), "body", value.toString()).toString();
    }
    private JSONObject chat(String id, String status, int time) throws Exception {
        JSONArray turns = new JSONArray(), parts = new JSONArray();
        if (!status.equals("idle")) {
            turns.put(new JSONObject().put("id", "turn-" + id).put("status", status).put("part_start", 0).put("input_end", 1).put("part_end", 1));
            parts.put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Hello"));
        }
        return new JSONObject().put("id", id).put("project_id", "project-one").put("title", id).put("backend", "codex")
                .put("permission_mode", "ask").put("can_control", true).put("updated_at", time).put("status", status)
                .put("turns", turns).put("transcript", new JSONObject().put("parts", parts));
    }
    private void initialize(MainActivityGoalTest.Harness h, boolean reject) throws Exception {
        h.set("chats", new JSONArray().put(chat("chat-one", "idle", 10)).put(chat("chat-two", "idle", 10)));
        MainActivityGoalTest.ShadowClient.responder = call -> {
            try {
                if (call.get(1).equals("/api/chats")) return envelope(200, chat("created-one", "idle", 10));
                if (call.get(1).endsWith("/messages")) return reject ? envelope(409, NativeChatState.put(new JSONObject(), "error", "Rejected"))
                        : envelope(200, chat(call.get(1).contains("created-one") ? "created-one" : "chat-one", "running", 20));
                if (call.get(1).startsWith("/api/permissions")) return envelope(200, new JSONObject().put("current", "ask")
                        .put("modes", new JSONArray().put(new JSONObject().put("id", "ask").put("available", true))));
                return envelope(200, chat(call.get(1).substring(call.get(1).lastIndexOf('/') + 1), "idle", 10));
            } catch (Exception error) { throw new AssertionError(error); }
        };
    }
    private void select(MainActivityGoalTest.Harness h, String id) {
        h.call("openSidebar"); NativeSidebar sidebar = h.get("sidebar");
        if (h.<Boolean>get("busy")) for (NativeProjectRow project : sidebar.projectRows) assertFalse(project.create.isEnabled());
        for (NativeChatLink link : sidebar.chatRows) if (link.open.getTag().toString().endsWith(":" + id)) {
            assertTrue(link.open.performClick());
            if (h.<JSONObject>get("chat") == null) {
                // These ordered REST races now exercise the real failure-only fallback.
                assertNotNull(MainActivityGoalTest.ShadowClient.chatListener);
                MainActivityGoalTest.ShadowClient.chatListener.onEvent("transport_error", "{}");
                Shadows.shadowOf(Looper.getMainLooper()).idle();
            }
            return;
        }
        fail("Missing production sidebar row " + id);
    }
    private void run(MainActivityGoalTest.Harness h, int index) {
        h.io.jobs.remove(index).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
    }
    private Object draft(MainActivityGoalTest.Harness h) {
        return org.robolectric.util.ReflectionHelpers.callInstanceMethod(h.activity, "draft");
    }
    private void attach(MainActivityGoalTest.Harness h) {
        try {
            Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
            Object file = org.robolectric.util.ReflectionHelpers.callConstructor(type,
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(String.class, "note.txt"),
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(String.class, "text/plain"),
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(byte[].class, new byte[] {65, 66}));
            this.<java.util.List<Object>>field(draft(h), "files").add(file); h.call("renderFiles");
        } catch (Exception error) { throw new AssertionError(error); }
    }
    private <T> T field(Object value, String name) { return org.robolectric.util.ReflectionHelpers.getField(value, name); }
    private void newChat(MainActivityGoalTest.Harness h) {
        org.robolectric.util.ReflectionHelpers.callInstanceMethod(h.activity, "newChat",
                org.robolectric.util.ReflectionHelpers.ClassParameter.from(String.class, "project-one"));
    }
    private void prepareAttachment(MainActivityGoalTest.Harness h) {
        android.net.Uri uri = android.net.Uri.parse("content://draft-fixture/note.txt");
        Shadows.shadowOf(h.activity.getContentResolver()).registerInputStream(uri, new java.io.ByteArrayInputStream(new byte[] {65, 66}));
        h.call("pickFiles"); assertNotNull(h.get("pickerDraft"));
        h.activity.onActivityResult(10, android.app.Activity.RESULT_OK, new android.content.Intent().setData(uri));
        assertNull(h.get("pickerDraft"));
    }

    @Test public void actualChatSelectionClearsTextAndAttachmentsInsteadOfRestoringPerChatDrafts() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.composer().input.setText("Old unsent draft"); attach(h);
            select(h, "chat-two"); run(h, 0);
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.composer().attachments.getChildCount());
            h.composer().input.setText("Another unsent draft"); attach(h);
            select(h, "chat-one"); run(h, 0);
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.composer().attachments.getChildCount());
            select(h, "chat-two"); run(h, 0);
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.composer().attachments.getChildCount());
        }
    }

    @Test public void explicitNewChatInTheSameProjectResetsTheNewDraftEveryTime() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            initialize(h, false); h.composer().input.setText("Unsent new conversation"); attach(h);
            newChat(h);
            assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat"));
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.composer().attachments.getChildCount());
            while (!h.io.jobs.isEmpty()) run(h, 0);
            h.composer().input.setText("Second new draft"); attach(h); newChat(h);
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.composer().attachments.getChildCount());
            while (!h.io.jobs.isEmpty()) run(h, 0);
        }
    }

    @Test public void sameSidebarRowAndSettingsRerenderDoNotDiscardTheCurrentDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.composer().input.setText("  Keep this draft\n"); attach(h);
            NativeUi.Composer composer = h.composer(); Object document = composer.input.getText(), selectedDraft = draft(h);
            select(h, "chat-one");
            assertSame(composer, h.composer()); assertSame(document, h.composer().input.getText());
            assertSame(selectedDraft, draft(h)); assertEquals(1, h.composer().attachments.getChildCount());
            h.call("openSettings"); h.call("closeSettings");
            assertSame(selectedDraft, draft(h)); assertEquals("  Keep this draft\n", h.composer().input.getText().toString());
            assertEquals(1, h.composer().attachments.getChildCount());
        }
    }

    @Test public void queuedAttachmentPreparationCannotPopulateAnotherChatOrAFreshNewDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); prepareAttachment(h); select(h, "chat-two"); run(h, 1); run(h, 0);
            assertEquals("chat-two", h.<String>get("chatID")); assertEquals(0, h.composer().attachments.getChildCount());
            assertEquals(0, this.<java.util.List<?>>field(draft(h), "files").size());
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); prepareAttachment(h);
            h.set("projectID", "project-two");
            run(h, 0);
            assertEquals(0, h.composer().attachments.getChildCount());
            assertEquals(0, this.<java.util.List<?>>field(draft(h), "files").size());
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            initialize(h, false); prepareAttachment(h); newChat(h); run(h, 1); run(h, 0);
            assertEquals("", h.<String>get("chatID")); assertEquals(0, h.composer().attachments.getChildCount());
            assertEquals(0, this.<java.util.List<?>>field(draft(h), "files").size());
        }
    }

    @Test public void returningToTheSameSelectionAcceptsPreparedFilesIntoItsCurrentDraftLikeWeb() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); Object original = draft(h); prepareAttachment(h);
            select(h, "chat-two"); select(h, "chat-one"); run(h, 2); run(h, 1);
            assertNotSame(original, draft(h)); h.composer().input.setText("Current draft"); run(h, 0);
            assertEquals("Current draft", h.composer().input.getText().toString());
            assertEquals(1, h.composer().attachments.getChildCount());
            assertEquals(1, this.<java.util.List<?>>field(draft(h), "files").size());
            assertEquals(0, this.<java.util.List<?>>field(original, "files").size());
        }
    }

    @Test public void canceledOrRevokedDocumentPickerCannotLeakAnOldDraftIntoTheWorkspace() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.call("pickFiles"); assertNotNull(h.get("pickerDraft"));
            h.activity.onActivityResult(10, android.app.Activity.RESULT_CANCELED, null);
            assertNull(h.get("pickerDraft")); assertTrue(h.io.jobs.isEmpty());
            h.call("pickFiles"); h.call("clearWorkspace"); assertNull(h.get("pickerDraft"));
            h.activity.onActivityResult(10, android.app.Activity.RESULT_OK,
                    new android.content.Intent().setData(android.net.Uri.parse("content://draft-fixture/note.txt")));
            assertTrue(h.io.jobs.isEmpty()); assertNull(h.get("user"));
        }
    }

    @Test public void aFileResultChosenAfterNavigationIsIgnoredInsteadOfWritingAnAbandonedDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.call("pickFiles"); select(h, "chat-two"); assertNull(h.get("pickerDraft"));
            h.activity.onActivityResult(10, android.app.Activity.RESULT_OK,
                    new android.content.Intent().setData(android.net.Uri.parse("content://draft-fixture/note.txt")));
            assertEquals(1, h.io.jobs.size()); run(h, 0); assertEquals(0, h.composer().attachments.getChildCount());
        }
    }

    @Test public void removingTheSelectedProjectDiscardsItsComposerAndDocumentPickerOwnership() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.composer().input.setText("Abandoned project draft"); attach(h); h.call("pickFiles");
            Object old = draft(h);
            JSONObject navigation = new JSONObject().put("projects", new JSONArray()
                    .put(new JSONObject().put("id", "project-two").put("name", "Other project").put("can_create", true)))
                    .put("chats", new JSONArray());
            org.robolectric.util.ReflectionHelpers.callInstanceMethod(h.activity, "acceptNavigation",
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(JSONObject.class, navigation));
            assertEquals("project-two", h.<String>get("projectID")); assertEquals("", h.<String>get("chatID"));
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.composer().attachments.getChildCount());
            assertNull(h.get("pickerDraft")); assertNotSame(old, draft(h));
            assertEquals("", this.<String>field(old, "text")); assertEquals(0, this.<java.util.List<?>>field(old, "files").size());
        }
    }

    @Test public void sidebarCanOpenAnotherConversationWhilePostingWithoutExposingTheOldEditor() throws Exception {
        for (boolean reject : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, reject); h.send("Original"); NativeUi.Composer original = h.composer();
            select(h, "chat-two");
            assertEquals("chat-two", h.<String>get("chatID")); assertNull(h.get("chat"));
            assertNotSame(original, h.composer()); assertFalse(h.composer().input.isEnabled());
            assertFalse(h.composer().send.isEnabled()); assertEquals("Read-only", h.composer().input.getHint().toString());
            assertEquals("", h.<String>get("permission")); assertTrue(h.<Boolean>get("busy"));
            assertEquals(2, h.io.jobs.size()); run(h, 1);
            assertEquals("chat-two", h.<JSONObject>get("chat").getString("id")); assertTrue(h.composer().input.isEnabled());
            assertTrue(h.composer().input.onCreateInputConnection(new EditorInfo()).commitText("Other draft", 1));
            run(h, 0);
            assertEquals("chat-two", h.<JSONObject>get("chat").getString("id"));
            assertEquals("Other draft", h.composer().input.getText().toString()); assertEquals("", h.<String>get("goalError"));
            assertFalse(h.<Boolean>get("busy")); assertNull(h.get("pendingSend")); assertNull(h.get("optimisticSend"));
            assertEquals("/api/chats/chat-two", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            assertEquals("/api/chats/chat-one/messages", MainActivityGoalTest.ShadowClient.calls.get(1).get(1));
        }
    }

    @Test public void returningToTheSameChatBeforeAcknowledgementUsesItsCurrentDraftAndWebIdentityRule() throws Exception {
        for (boolean reject : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, reject); attach(h); h.send("Original"); select(h, "chat-two"); select(h, "chat-one");
            run(h, 2); run(h, 1);
            assertEquals("chat-one", h.<JSONObject>get("chat").getString("id"));
            h.composer().input.setText("Returned draft"); run(h, 0);
            assertEquals("chat-one", h.<JSONObject>get("chat").getString("id"));
            assertEquals(reject ? "Original" : "Returned draft", h.composer().input.getText().toString());
            assertEquals(reject ? 1 : 0, h.composer().attachments.getChildCount());
            assertEquals(reject ? "Rejected" : "", h.<String>get("goalError"));
            if (!reject) assertEquals("turn-chat-one", NativeChatState.runningTurn(h.get("chat")).getString("id"));
            Object current = org.robolectric.util.ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            assertEquals(h.composer().input.getText().toString(), org.robolectric.util.ReflectionHelpers.getField(current, "text"));
        }
    }

    @Test public void backgroundFirstChatAdoptionNeverPullsTheUserBackFromAnotherConversation() throws Exception {
        for (boolean reject : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            initialize(h, reject); h.send("First message"); select(h, "chat-two");
            run(h, 1); h.composer().input.setText("Other draft"); run(h, 0);
            assertEquals("chat-two", h.<String>get("chatID")); assertEquals("chat-two", h.<JSONObject>get("chat").getString("id"));
            assertEquals("Other draft", h.composer().input.getText().toString()); assertEquals("", h.<String>get("goalError"));
            assertFalse(h.<Boolean>get("busy")); assertNull(h.get("pendingSend"));
            assertEquals("/api/chats", MainActivityGoalTest.ShadowClient.calls.get(1).get(1));
            assertEquals("/api/chats/created-one/messages", MainActivityGoalTest.ShadowClient.calls.get(2).get(1));
        }
    }

    @Test public void aLateHydrationCannotOverwriteTheNewerAcknowledgedTurn() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.send("Original"); select(h, "chat-two"); select(h, "chat-one");
            run(h, 0);
            assertEquals("running", h.<JSONObject>get("chat").getString("status"));
            assertEquals(20, h.<JSONObject>get("chat").getLong("updated_at"));
            NativeUi.Composer editor = h.composer();
            editor.input.setText("Next draft"); assertTrue(editor.input.requestFocus());
            android.text.Spannable text = editor.input.getText(); Object composing = new Object();
            text.setSpan(composing, 0, 4, android.text.Spanned.SPAN_COMPOSING | android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            editor.input.setSelection(4);
            run(h, 1);
            assertEquals("running", h.<JSONObject>get("chat").getString("status"));
            assertEquals(20, h.<JSONObject>get("chat").getLong("updated_at"));
            assertSame(editor, h.composer()); assertSame(text, editor.input.getText());
            assertEquals(0, text.getSpanStart(composing)); assertEquals(4, text.getSpanEnd(composing));
            assertEquals(4, editor.input.getSelectionStart()); assertTrue(editor.input.hasFocus());
            assertTrue(editor.input.isEnabled());
        }
    }

    @Test public void clickingTheAlreadySelectedSidebarChatPreservesTheLiveComposerAndPendingTurn() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); h.send("Original"); NativeUi.Composer editor = h.composer();
            JSONObject pending = h.get("chat"); editor.input.setText("Next draft");
            select(h, "chat-one");
            assertSame(editor, h.composer()); assertSame(pending, h.get("chat"));
            assertEquals("Next draft", editor.input.getText().toString()); assertEquals(1, h.io.jobs.size());
            assertEquals("", h.<String>get("panel"));
        }
    }

    @Test public void failedOrStaleHydrationKeepsTheTargetReadOnlyAndReportsOnlyItsOwnError() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); select(h, "chat-two");
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(404, NativeChatState.put(new JSONObject(), "error", "Missing chat"));
            h.io.run();
            assertFalse(h.composer().input.isEnabled()); assertFalse(h.composer().send.isEnabled());
            assertEquals("Missing chat", h.<String>get("goalError")); assertNotNull(h.composer().commands.error);
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            initialize(h, false); select(h, "chat-two"); select(h, "chat-one");
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(404, NativeChatState.put(new JSONObject(), "error", "Stale failure"));
            run(h, 0); assertEquals("", h.<String>get("goalError"));
            assertEquals("chat-one", h.<String>get("chatID"));
        }
    }

    @Test public void readyEditorRequiresAnAuthorizedProjectMatchingChatAndAuthenticatedUser() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.composer().input.setText("Draft"); assertTrue(h.composer().send.isEnabled());
            h.set("chatID", "not-the-loaded-chat"); h.call("updateComposer");
            assertFalse(h.composer().input.isEnabled()); h.call("sendOrStop"); assertTrue(h.io.jobs.isEmpty());
            h.set("chatID", "chat-one"); h.set("projects", new JSONArray()); h.call("updateComposer");
            assertFalse(h.composer().input.isEnabled()); assertFalse(h.composer().send.isEnabled());
            h.set("projects", new JSONArray().put(new JSONObject().put("id", "project-one"))); h.set("user", null); h.call("updateComposer");
            assertFalse(h.composer().input.isEnabled()); assertFalse(h.composer().send.isEnabled());
        }
    }
}
