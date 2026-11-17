package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.view.View;
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

/** Queued messages in the real Activity, with a deterministic gomobile boundary. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityQueueTest {
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

    @Test public void aQueueRequestLeftBehindByANetworkHandoffNoLongerBlocksTheQueue() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityConnectionTest.connectionQueue(h);
            show(h, running(0));
            h.composer().input.setText("Queued while running"); h.composer().queueSubmit.run();
            assertEquals(1, h.io.jobs.size()); assertEquals("", h.composer().input.getText().toString());
            h.composer().input.setText("Typed meanwhile"); h.composer().queueSubmit.run();
            assertEquals("One queue request is in flight at a time", 1, h.io.jobs.size());
            assertEquals(View.VISIBLE, h.composer().queue.getVisibility()); assertFalse(h.composer().queue.isEnabled());

            h.call("beginNetworkHandoff");
            assertTrue("The cleared draft's fate is reported: " + h.<String>get("goalError"), h.<String>get("goalError").startsWith("Send status unknown"));
            h.io.run();
            assertTrue("A request of the earlier connection must not reach the transport", MainActivityGoalTest.ShadowClient.calls.isEmpty());

            // The connection is back with the same chat.
            h.set("client", new Client("unused", "https://fixture.invalid")); h.set("connected", true); h.set("connecting", false); h.set("busy", false);
            show(h, running(1));
            assertEquals("Typed meanwhile", h.composer().input.getText().toString());
            assertTrue("The queue button must work again", h.composer().queue.isEnabled());
            MainActivityGoalTest.ShadowClient.responder = call -> { try { return envelope(running(2)); } catch (Exception error) { throw new AssertionError(error); } };
            h.composer().queueSubmit.run(); h.io.run();
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals("/api/chats/chat-one/messages", call.get(1));
            assertEquals("Typed meanwhile", new JSONObject(call.get(2)).getString("message")); assertEquals("queue", new JSONObject(call.get(2)).getString("delivery"));

            // A queued row's action left behind the same way does not disable the rows either.
            NativeQueuedMessages queue = h.get("queuedView");
            ((android.view.ViewGroup) ((android.view.ViewGroup) queue.getChildAt(0)).getChildAt(0)).getChildAt(1).performClick();
            assertEquals(1, h.io.jobs.size());
            h.call("beginNetworkHandoff"); h.io.run();
            h.set("client", new Client("unused", "https://fixture.invalid")); h.set("connected", true); h.set("connecting", false); h.set("busy", false);
            show(h, running(2));
            queue = h.get("queuedView");
            View steer = ((android.view.ViewGroup) ((android.view.ViewGroup) queue.getChildAt(0)).getChildAt(0)).getChildAt(1);
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
            assertEquals(2, queue.rows()); assertEquals(2 * ui.dp(NativeQueuedMessages.ROW_HEIGHT_DP), queue.getHeight());
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
            ((android.view.ViewGroup) last).getChildAt(2).performClick(); h.io.run();
            assertEquals(List.of("queued-19"), ids);
        }
    }
}
