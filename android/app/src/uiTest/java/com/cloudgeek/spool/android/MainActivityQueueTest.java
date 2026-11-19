package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.view.View;
import android.view.ViewGroup;
import android.view.KeyEvent;
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
import spoolnet.Client;
import org.robolectric.util.ReflectionHelpers;

/** Queued messages in the real Activity, with a deterministic gomobile boundary. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class, MainActivityQueueTest.ShadowQueueDrag.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityQueueTest {
    // Robolectric has no system drag service. Capture the real handle's OS call,
    // then deliver the framework events back to the production queue View.
    @org.robolectric.annotation.Implements(View.class)
    public static class ShadowQueueDrag extends org.robolectric.shadows.ShadowView {
        static Object local;
        // ImageView subclasses can retain a plain ShadowView; this OS stub needs no instance.
        @org.robolectric.annotation.Implementation
        protected static boolean startDragAndDrop(android.content.ClipData data, View.DragShadowBuilder shadow, Object state, int flags) {
            local = state; return true;
        }
        @org.robolectric.annotation.Resetter public static void resetDrag() { local = null; }
    }
    private JSONObject running(int queued) throws Exception {
        JSONArray parts = new JSONArray().put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Hello"))
                .put(new JSONObject().put("role", "assistant").put("kind", "text").put("text", "Partial reply"));
        JSONArray turns = new JSONArray().put(new JSONObject().put("id", "server-turn").put("user_id", "alice").put("status", "running")
                .put("backend", "codex").put("started_at", 10).put("part_start", 0).put("input_end", 1).put("part_end", 2));
        JSONArray messages = new JSONArray();
        for (int i = 0; i < queued; i++) messages.put(new JSONObject().put("id", "queued-" + i).put("message", "Queued message " + i).put("created_at", 20 + i));
        return new JSONObject().put("id", "chat-one").put("project_id", "project-one").put("backend", "codex").put("can_control", true)
                .put("status", "running").put("updated_at", 20).put("turns", turns).put("transcript", new JSONObject().put("parts", parts))
                .put("queued_messages", messages);
    }

    private String envelope(JSONObject value) {
        return NativeChatState.put(NativeChatState.put(new JSONObject(), "status", 200), "body", value.toString()).toString();
    }

    private void show(MainActivityGoalTest.Harness h, JSONObject chat) {
        h.set("chat", chat); h.call("renderChat");
        Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); NativeMarkwonHistoryTest.layout(h.get("root"));
    }

    private View action(MainActivityGoalTest.Harness h, int index, String name) {
        NativeQueuedMessages queue = h.get("queuedView");
        ViewGroup row = (ViewGroup) ((ViewGroup) queue.getChildAt(0)).getChildAt(index);
        for (int i = 0; i < row.getChildCount(); i++) {
            View child = row.getChildAt(i);
            if (name.contentEquals(child.getContentDescription() == null ? "" : child.getContentDescription())) return child;
        }
        throw new AssertionError("Missing queue action " + name);
    }

    private android.widget.TextView text(View view, String value) {
        if (view.getVisibility() == View.GONE) return null;
        if (view instanceof android.widget.TextView && value.contentEquals(((android.widget.TextView) view).getText())) return (android.widget.TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            android.widget.TextView found = text(((ViewGroup) view).getChildAt(i), value); if (found != null) return found;
        }
        return null;
    }

    private void screenshot(MainActivityGoalTest.Harness h, String name) throws Exception {
        if (android.os.Build.VERSION.SDK_INT != 35) return;
        java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"), "../message-delivery");
        assertTrue(directory.isDirectory() || directory.mkdirs());
        View root = h.get("root"); NativeMarkwonHistoryTest.layout(root);
        android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(root.getWidth(), root.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
        root.draw(new android.graphics.Canvas(image));
        try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, "android-" + name + ".png"))) {
            assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
        } finally { image.recycle(); }
    }

    @Test public void queueAppearsBeforeTheRequestRunsAndAStreamReceiptReplacesIt() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("theme", "dark"); h.call("render");
            show(h, running(0));
            h.composer().input.setText("Immediate queue"); h.composer().send.performClick();
            NativeQueuedMessages queue = h.get("queuedView");
            assertEquals(1, queue.rows()); assertNotNull(text(queue, "Immediate queue")); assertNotNull(text(queue, "Sending…"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            screenshot(h, "queue-sending");
            Object local = h.<List<?>>get("localDeliveries").get(0);
            String id = ReflectionHelpers.getField(local, "id");
            JSONObject accepted = running(0).put("updated_at", 21);
            accepted.put("queued_messages", new JSONArray().put(new JSONObject().put("id", "server-queue")
                    .put("client_message_id", id).put("message", "Immediate queue").put("created_at", 21)));
            show(h, accepted);
            assertEquals(1, queue.rows()); assertNull(text(queue, "Sending…"));
            assertNotNull(action(h, 0, "Delete queued message"));
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { assertEquals(id, new JSONObject(call.get(2)).getString("client_message_id")); return envelope(accepted); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.io.run(); assertEquals(1, queue.rows()); assertTrue(h.<List<?>>get("localDeliveries").isEmpty());
        }
    }

    @Test public void steeringIsImmediateAndAProvisionalServerInputStillShowsSending() throws Exception {
        for (boolean recycler : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, recycler)) {
            h.set("theme", "dark"); h.call("render");
            show(h, running(0));
            h.composer().input.setText("Immediate steer");
            ReflectionHelpers.callInstanceMethod(h.activity, "deliver", ReflectionHelpers.ClassParameter.from(boolean.class, true));
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); NativeMarkwonHistoryTest.layout(h.get("root"));
            assertNotNull(text(h.get("root"), "Immediate steer")); assertNotNull(text(h.get("root"), "Sending…"));
            NativeMessageFooter footer = (NativeMessageFooter) text(h.get("root"), "Sending…").getParent().getParent();
            assertTrue(footer.getChildAt(0) instanceof NativeDeliveryStatus);
            assertTrue(footer.indexOfChild(footer.author) > 0);
            assertTrue(footer.indexOfChild(footer.time) > footer.indexOfChild(footer.author));
            assertEquals(0, h.<NativeQueuedMessages>get("queuedView").rows()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            if (recycler) screenshot(h, "steer-sending");
            String id = ReflectionHelpers.getField(h.<List<?>>get("localDeliveries").get(0), "id");
            JSONObject received = running(0).put("updated_at", 21);
            received.getJSONObject("transcript").getJSONArray("parts").put(new JSONObject().put("role", "user").put("kind", "text")
                    .put("text", "Immediate steer").put("timestamp", 21).put("client_message_id", id).put("pending", true));
            received.getJSONArray("turns").getJSONObject(0).put("part_end", 3);
            show(h, received);
            assertNotNull(text(h.get("root"), "Sending…"));
            assertEquals(1, h.<List<?>>get("localDeliveries").size());
            MainActivityGoalTest.ShadowClient.responder = call -> envelope(received);
            h.io.run(); NativeMarkwonHistoryTest.layout(h.get("root"));
            assertNull(text(h.get("root"), "Sending…")); assertTrue(h.<List<?>>get("localDeliveries").isEmpty());
            assertNotNull(text(h.get("root"), "Immediate steer"));
        }
    }

    @Test public void failedDeliveriesRetainTheirTextAndAttachmentForEditing() throws Exception {
        for (boolean steer : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(0));
            Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
            Object file = ReflectionHelpers.callConstructor(type, ReflectionHelpers.ClassParameter.from(String.class, "note.txt"),
                    ReflectionHelpers.ClassParameter.from(String.class, "text/plain"), ReflectionHelpers.ClassParameter.from(byte[].class, new byte[] {65}));
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            ReflectionHelpers.<List<Object>>getField(draft, "files").add(file);
            h.composer().input.setText("Retained delivery");
            ReflectionHelpers.callInstanceMethod(h.activity, "deliver", ReflectionHelpers.ClassParameter.from(boolean.class, steer));
            MainActivityGoalTest.ShadowClient.responder = call -> NativeChatState.put(NativeChatState.put(new JSONObject(), "status", 500), "body", "{\"error\":\"Delivery failed\"}").toString();
            h.io.run(); NativeMarkwonHistoryTest.layout(h.get("root"));
            assertNotNull(text(h.get("root"), "Send failed"));
            assertNotNull(text(h.get("root"), "Retained delivery")); assertEquals("", h.composer().input.getText().toString());
            screenshot(h, (steer ? "steer" : "queue") + "-failed");
            if (steer) text(h.get("root"), "Edit message").performClick();
            else {
                assertNotNull(action(h, 0, "Delete queued message"));
                action(h, 0, "Queued message actions").performClick();
                h.<NativeUi>get("ui").activePopover.content.getChildAt(0).performClick();
            }
            assertEquals("Retained delivery", h.composer().input.getText().toString());
            assertSame(file, ReflectionHelpers.<List<Object>>getField(draft, "files").get(0));
            assertTrue(h.<List<?>>get("localDeliveries").isEmpty());
        }
    }

    @Test public void delayedDeliveryFailureDoesNotAlterAnotherChatsDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(0)); h.composer().input.setText("Chat one delivery"); h.composer().send.performClick();
            ReflectionHelpers.callInstanceMethod(h.activity, "selectChat", ReflectionHelpers.ClassParameter.from(String.class, "chat-two"));
            show(h, running(0).put("id", "chat-two")); h.composer().input.setText("Other draft");
            MainActivityGoalTest.ShadowClient.responder = call -> NativeChatState.put(NativeChatState.put(new JSONObject(), "status", 500), "body", "{\"error\":\"Delivery failed\"}").toString();
            h.io.run();
            assertEquals("Other draft", h.composer().input.getText().toString()); assertEquals(0, h.<NativeQueuedMessages>get("queuedView").rows());
            ReflectionHelpers.callInstanceMethod(h.activity, "selectChat", ReflectionHelpers.ClassParameter.from(String.class, "chat-one"));
            show(h, running(0)); assertNotNull(text(h.get("queuedView"), "Send failed"));
        }
    }

    @Test public void theSameComposerButtonSendsDraftsAndStopsOnlyWhenTheDraftIsEmpty() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(1));
            NativeUi.IconButton button = h.composer().send;
            assertEquals(4, h.composer().bottom.getChildCount());
            assertEquals("Stop response", button.getContentDescription());
            h.composer().input.setText("Follow-up");
            assertSame(button, h.composer().send);
            assertEquals("Send message", button.getContentDescription());
            assertTrue(button.isEnabled());
            h.composer().input.setText(" \n\t ");
            assertEquals("Stop response", button.getContentDescription());

            Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
            Object file = ReflectionHelpers.callConstructor(type,
                    ReflectionHelpers.ClassParameter.from(String.class, "note.txt"),
                    ReflectionHelpers.ClassParameter.from(String.class, "text/plain"),
                    ReflectionHelpers.ClassParameter.from(byte[].class, new byte[] {65}));
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            List<Object> files = ReflectionHelpers.getField(draft, "files");
            files.add(file); h.call("updateComposer");
            assertEquals("Send message", button.getContentDescription());
            files.clear(); h.call("updateComposer");
            assertEquals("Stop response", button.getContentDescription());

            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return envelope(running(2).put("updated_at", 21)); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            h.composer().input.setText("Click to queue"); button.performClick();
            assertEquals("", h.composer().input.getText().toString());
            assertEquals("Stop response", button.getContentDescription());
            h.io.run();
            List<String> request = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals("/api/chats/chat-one/messages", request.get(1));
            assertEquals("queue", new JSONObject(request.get(2)).getString("delivery"));
            assertEquals("Click to queue", new JSONObject(request.get(2)).getString("message"));
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void queueBordersMatchTheComposerAndPressedGripsStayCircularInBothThemes() throws Exception {
        for (String theme : new String[] {"light", "dark"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("theme", theme); h.call("render"); show(h, running(3));
            NativeUi ui = h.get("ui"); NativeQueuedMessages queue = h.get("queuedView");
            View grip = action(h, 1, "Drag to reorder");
            assertEquals(ui.dp(26), grip.getWidth()); assertEquals(grip.getWidth(), grip.getHeight());
            grip.setPressed(true);
            android.graphics.Bitmap icon = android.graphics.Bitmap.createBitmap(grip.getWidth(), grip.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
            grip.draw(new android.graphics.Canvas(icon));
            assertEquals(0, android.graphics.Color.alpha(icon.getPixel(0, 0)));
            assertEquals(ui.controlActive, icon.getPixel(grip.getWidth() / 2, ui.dp(2)));
            icon.recycle();
            android.graphics.Bitmap frame = android.graphics.Bitmap.createBitmap(queue.getWidth(), queue.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
            queue.draw(new android.graphics.Canvas(frame));
            assertEquals(ui.borderStrong, frame.getPixel(0, queue.getHeight() / 2));
            int separator = ui.dark ? androidx.core.graphics.ColorUtils.blendARGB(ui.line, ui.borderStrong, .65f) : ui.line;
            assertEquals(separator, frame.getPixel(queue.getWidth() / 2, ui.dp(1) + ui.dp(NativeQueuedMessages.ROW_HEIGHT_DP)));
            assertEquals(ui.elevated, frame.getPixel(queue.getWidth() / 2, queue.getHeight() - 1));
            frame.recycle();
            java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"), "../queued-messages");
            assertTrue(directory.isDirectory() || directory.mkdirs());
            View root = h.get("root");
            android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(root.getWidth(), root.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
            root.draw(new android.graphics.Canvas(image));
            try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory,
                    "android-api" + android.os.Build.VERSION.SDK_INT + "-" + theme + ".png"))) {
                assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
            }
            image.recycle(); grip.setPressed(false);
        }
    }

    @Test public void editingRestoresTextAndAttachmentWithoutReplacingTheExistingDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(1));
            h.composer().input.setText("Existing draft");
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    JSONObject payload = new JSONObject(call.get(2));
                    assertEquals("queued-0", payload.getString("id"));
                    if (payload.getString("action").equals("draft")) return envelope(new JSONObject().put("message", "  Original\nmessage  ")
                            .put("files", new JSONArray().put(new JSONObject().put("name", "notes.txt").put("media_type", "text/plain").put("data", "AP8qDQo="))));
                    assertEquals("edit", payload.getString("action"));
                    return envelope(running(0).put("updated_at", 21));
                } catch (Exception error) { throw new AssertionError(error); }
            };
            action(h, 0, "Queued message actions").performClick();
            NativeUi ui = h.get("ui");
            assertNotNull(ui.activePopover);
            assertEquals("Edit message", ui.activePopover.content.getChildAt(0).getContentDescription());
            ui.activePopover.content.getChildAt(0).performClick();
            assertFalse(h.composer().input.isEnabled());
            h.io.run();
            assertEquals("Existing draft", h.composer().input.getText().toString());
            h.io.run();
            assertEquals("  Original\nmessage  \n\nExisting draft", h.composer().input.getText().toString());
            assertTrue(h.composer().input.isEnabled());
            assertEquals(0, h.<NativeQueuedMessages>get("queuedView").rows());
            assertEquals(1, h.composer().attachments.getChildCount());
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            List<?> files = ReflectionHelpers.getField(draft, "files");
            assertArrayEquals(new byte[] {0, (byte) 255, 42, 13, 10}, ReflectionHelpers.getField(files.get(0), "data"));
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void anEditConflictKeepsTheCurrentDraftAndDoesNotRestoreADuplicate() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(1)); h.composer().input.setText("Keep this");
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    if (new JSONObject(call.get(2)).getString("action").equals("draft"))
                        return envelope(new JSONObject().put("message", "Already sent").put("files", new JSONArray()));
                    return new JSONObject().put("status", 409).put("body", new JSONObject().put("error", "This message is no longer queued").toString()).toString();
                } catch (Exception error) { throw new AssertionError(error); }
            };
            action(h, 0, "Queued message actions").performClick();
            h.<NativeUi>get("ui").activePopover.content.getChildAt(0).performClick();
            h.io.run(); h.io.run();
            assertEquals("Keep this", h.composer().input.getText().toString());
            assertEquals("This message is no longer queued", h.<String>get("goalError"));
            assertTrue(h.composer().input.isEnabled());
            assertEquals(0, h.composer().attachments.getChildCount());
        }
    }

    @Test public void anEditCompletedAfterNavigationReturnsToItsOwnChat() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject original = running(1), other = h.chat("chat-two").put("project_id", "project-one");
            h.set("chats", new JSONArray().put(original).put(other));
            show(h, original);
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    if (new JSONObject(call.get(2)).getString("action").equals("draft"))
                        return envelope(new JSONObject().put("message", "Recovered for chat one").put("files", new JSONArray()));
                    return envelope(running(0).put("updated_at", 21));
                } catch (Exception error) { throw new AssertionError(error); }
            };
            action(h, 0, "Queued message actions").performClick();
            h.<NativeUi>get("ui").activePopover.content.getChildAt(0).performClick();
            h.io.run(); // The cancellation has been sent when the user navigates.
            ReflectionHelpers.callInstanceMethod(h.activity, "selectChat", ReflectionHelpers.ClassParameter.from(String.class, "chat-two"));
            show(h, other);
            h.composer().input.setText("Chat two draft");
            h.io.run();
            assertEquals("Chat two draft", h.composer().input.getText().toString());
            ReflectionHelpers.callInstanceMethod(h.activity, "selectChat", ReflectionHelpers.ClassParameter.from(String.class, "chat-one"));
            show(h, running(0).put("updated_at", 21));
            assertEquals("Recovered for chat one", h.composer().input.getText().toString());
            assertTrue(h.<java.util.Map<?, ?>>get("editedQueueDrafts").isEmpty());
        }
    }

    @Test public void queueHandlesMoveTheSelectedMessageRelativeToItsNeighbor() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(3));
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    JSONObject payload = new JSONObject(call.get(2));
                    assertEquals("move", payload.getString("action"));
                    assertEquals("queued-1", payload.getString("id"));
                    assertEquals("queued-0", payload.getString("before_id"));
                    JSONObject result = running(3).put("updated_at", 21);
                    JSONArray messages = result.getJSONArray("queued_messages");
                    result.put("queued_messages", new JSONArray().put(messages.get(1)).put(messages.get(0)).put(messages.get(2)));
                    return envelope(result);
                } catch (Exception error) { throw new AssertionError(error); }
            };
            View grip = action(h, 1, "Drag to reorder");
            assertTrue(grip.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_UP)));
            h.io.run();
            assertEquals("queued-1", h.<JSONObject>get("chat").getJSONArray("queued_messages").getJSONObject(0).getString("id"));
        }
    }

    @Test public void aRejectedReorderReturnsToTheServerOrder() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(3));
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return new JSONObject().put("status", 500).put("body", new JSONObject().put("error", "Move failed").toString()).toString(); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            View grip = action(h, 1, "Drag to reorder");
            assertTrue(grip.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_UP)));
            ViewGroup rows = (ViewGroup) h.<NativeQueuedMessages>get("queuedView").getChildAt(0);
            assertEquals("queued-1", rows.getChildAt(0).getTag());
            h.io.run(); NativeMarkwonHistoryTest.layout(h.get("root"));
            rows = (ViewGroup) h.<NativeQueuedMessages>get("queuedView").getChildAt(0);
            assertEquals("queued-0", rows.getChildAt(0).getTag());
            assertEquals("Move failed", h.<String>get("goalError"));
            assertTrue(action(h, 1, "Drag to reorder").isEnabled());
        }
    }

    @Test public void aLongPressDragDropsTheMessageAtTheSelectedPosition() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, running(3));
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    JSONObject payload = new JSONObject(call.get(2));
                    assertEquals("move", payload.getString("action"));
                    assertEquals("queued-2", payload.getString("id"));
                    assertEquals("queued-0", payload.getString("before_id"));
                    return envelope(running(3).put("updated_at", 21));
                } catch (Exception error) { throw new AssertionError(error); }
            };
            View grip = action(h, 2, "Drag to reorder");
            grip.performLongClick();
            Object local = ShadowQueueDrag.local;
            assertNotNull("The native drag retains its queue message", local);
            NativeQueuedMessages queue = h.get("queuedView");
            for (int type : new int[] {android.view.DragEvent.ACTION_DRAG_STARTED, android.view.DragEvent.ACTION_DRAG_LOCATION,
                    android.view.DragEvent.ACTION_DROP, android.view.DragEvent.ACTION_DRAG_ENDED}) {
                android.view.DragEvent event = ReflectionHelpers.callStaticMethod(android.view.DragEvent.class, "obtain");
                ReflectionHelpers.setField(event, "mAction", type);
                ReflectionHelpers.setField(event, "mY", 2f);
                ReflectionHelpers.setField(event, "mLocalState", local);
                assertTrue(queue.dispatchDragEvent(event)); ReflectionHelpers.callInstanceMethod(event, "recycle");
                if (type == android.view.DragEvent.ACTION_DRAG_LOCATION) {
                    Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(80));
                    assertTrue("Neighbors make room while the native drag shadow follows the touch", queue.getChildAt(0) instanceof ViewGroup
                            && ((ViewGroup) queue.getChildAt(0)).getChildAt(0).getTranslationY() > 0);
                } else if (type == android.view.DragEvent.ACTION_DROP) {
                    ViewGroup rows = (ViewGroup) queue.getChildAt(0);
                    assertEquals("Drop shows the new order before the network response", "queued-2", rows.getChildAt(0).getTag());
                }
            }
            h.io.run();
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void aQueueRequestLeftBehindByANetworkHandoffNoLongerBlocksTheQueue() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityConnectionTest.connectionQueue(h);
            show(h, running(0));
            h.composer().input.setText("Queued while running"); h.composer().send.performClick();
            assertEquals(1, h.io.jobs.size()); assertEquals("", h.composer().input.getText().toString());
            h.composer().input.setText("Typed meanwhile"); h.composer().send.performClick();
            assertEquals("One queue request is in flight at a time", 1, h.io.jobs.size());
            assertEquals("Send message", h.composer().send.getContentDescription()); assertFalse(h.composer().send.isEnabled());

            h.call("beginNetworkHandoff");
            assertTrue("The cleared draft's fate is reported: " + h.<String>get("goalError"), h.<String>get("goalError").startsWith("Send status unknown"));
            h.io.run();
            assertTrue("A request of the earlier connection must not reach the transport", MainActivityGoalTest.ShadowClient.calls.isEmpty());

            // The connection is back with the same chat.
            h.set("client", new Client("unused", "https://fixture.invalid")); h.set("connected", true); h.set("connecting", false); h.set("busy", false);
            show(h, running(1));
            assertEquals("Typed meanwhile", h.composer().input.getText().toString());
            assertTrue("The send button must work again", h.composer().send.isEnabled());
            MainActivityGoalTest.ShadowClient.responder = call -> { try { return envelope(running(2)); } catch (Exception error) { throw new AssertionError(error); } };
            h.composer().send.performClick(); h.io.run();
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals("/api/chats/chat-one/messages", call.get(1));
            assertEquals("Typed meanwhile", new JSONObject(call.get(2)).getString("message")); assertEquals("queue", new JSONObject(call.get(2)).getString("delivery"));

            // A queued row's action left behind the same way does not disable the rows either.
            NativeQueuedMessages queue = h.get("queuedView");
            ((android.view.ViewGroup) ((android.view.ViewGroup) queue.getChildAt(0)).getChildAt(0)).getChildAt(2).performClick();
            assertEquals(1, h.io.jobs.size());
            h.call("beginNetworkHandoff"); h.io.run();
            h.set("client", new Client("unused", "https://fixture.invalid")); h.set("connected", true); h.set("connecting", false); h.set("busy", false);
            show(h, running(2));
            queue = h.get("queuedView");
            View steer = ((android.view.ViewGroup) ((android.view.ViewGroup) queue.getChildAt(0)).getChildAt(0)).getChildAt(2);
            assertTrue(steer.isEnabled()); steer.performClick(); h.io.run();
            call = MainActivityGoalTest.ShadowClient.calls.get(MainActivityGoalTest.ShadowClient.calls.size() - 1);
            assertEquals("/api/chats/chat-one/queue", call.get(1)); assertEquals("steer", new JSONObject(call.get(2)).getString("action"));
        }
    }

    @Test public void aLongQueueScrollsWithinItsOwnHeightAndLeavesTheComposerOnScreen() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            NativeUi ui = h.get("ui");
            show(h, running(2));
            NativeQueuedMessages queue = h.get("queuedView");
            assertEquals(2, queue.rows()); assertEquals(ui.dp(1) + 2 * ui.dp(NativeQueuedMessages.ROW_HEIGHT_DP), queue.getHeight());
            assertEquals(ui.dp(16), queue.getLeft() - h.composer().box.getLeft());
            assertEquals(ui.dp(16), h.composer().box.getRight() - queue.getRight());
            assertFalse("A short queue has nothing to scroll", queue.canScrollVertically(1));
            int[] at = new int[2]; h.composer().box.getLocationInWindow(at); int composerTop = at[1], composerHeight = h.composer().box.getHeight();

            show(h, running(20));
            queue = h.get("queuedView");
            assertEquals(20, queue.rows()); assertEquals(ui.dp(NativeQueuedMessages.MAX_HEIGHT_DP), queue.getHeight());
            h.composer().box.getLocationInWindow(at);
            assertEquals("The composer keeps its size", composerHeight, h.composer().box.getHeight());
            assertEquals("Only the capped queue moves the composer's top", composerTop, at[1]);
            assertTrue("The composer stays on screen", at[1] + composerHeight <= 800);
            NativeMarkwonHistory history = h.get("markwonHistory");
            assertTrue("The conversation keeps room above the queue", history.getHeight() > ui.dp(200));

            // Every row is reachable, and still acts on its own message.
            assertTrue(queue.canScrollVertically(1));
            View list = queue.getChildAt(0);
            queue.scrollTo(0, list.getHeight() - queue.getHeight());
            assertFalse(queue.canScrollVertically(1));
            List<String> ids = new ArrayList<>();
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { ids.add(new JSONObject(call.get(2)).getString("id")); return envelope(running(19)); } catch (Exception error) { throw new AssertionError(error); }
            };
            View last = ((android.view.ViewGroup) list).getChildAt(19);
            assertTrue("The last row is in view", last.getBottom() - queue.getScrollY() <= queue.getHeight());
            action(h, 19, "Delete queued message").performClick(); h.io.run();
            assertEquals(List.of("queued-19"), ids);
        }
    }
}
