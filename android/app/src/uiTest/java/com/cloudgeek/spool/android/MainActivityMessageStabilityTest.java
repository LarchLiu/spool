package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Actual Activity views must survive optimistic/server/native turn identity changes. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityMessageStabilityTest {
    private static final String PROMPT = "Prompt line\n".repeat(12).trim();

    private JSONObject chat(String id, String backend, boolean accepted) throws Exception {
        JSONArray parts = new JSONArray(), turns = new JSONArray();
        if (accepted) {
            parts.put(new JSONObject().put("role", "user").put("kind", "text").put("text", PROMPT));
            turns.put(new JSONObject().put("id", "server-turn").put("native_turn_id", "native-turn")
                    .put("backend", backend).put("user_id", "alice").put("status", "running").put("started_at", 10)
                    .put("part_start", 0).put("input_end", 1).put("part_end", 1));
        }
        return new JSONObject().put("id", id).put("project_id", "project-one").put("backend", backend)
                .put("can_control", true).put("status", accepted ? "running" : "idle").put("updated_at", accepted ? 20 : 10)
                .put("turns", turns).put("transcript", new JSONObject().put("parts", parts));
    }

    private String envelope(JSONObject value) throws Exception {
        return new JSONObject().put("status", 200).put("body", value.toString()).toString();
    }

    private void layout(MainActivityGoalTest.Harness h) {
        for (int i = 0; i < 3; i++) {
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            View root = h.get("root");
            root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY),
                    View.MeasureSpec.makeMeasureSpec(1000, View.MeasureSpec.EXACTLY));
            root.layout(0, 0, 360, 1000); root.getViewTreeObserver().dispatchOnPreDraw();
        }
    }

    private NativeUserRow user(View root) {
        if (root instanceof NativeUserRow) return (NativeUserRow) root;
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) {
            NativeUserRow row = user(((ViewGroup) root).getChildAt(i));
            if (row != null) return row;
        }
        return null;
    }

    private NativeActivityList.State activityState(MainActivityGoalTest.Harness h) throws Exception {
        JSONObject chat = h.get("chat"), turn = chat.getJSONArray("turns").getJSONObject(0);
        return ReflectionHelpers.callInstanceMethod(h.activity, "activityState",
                ReflectionHelpers.ClassParameter.from(JSONObject.class, chat),
                ReflectionHelpers.ClassParameter.from(JSONObject.class, turn));
    }

    private void event(String kind, JSONObject value) {
        assertNotNull(MainActivityGoalTest.ShadowClient.chatListener);
        MainActivityGoalTest.ShadowClient.chatListener.onEvent(kind, value.toString());
        Shadows.shadowOf(Looper.getMainLooper()).idle();
    }

    private void stable(MainActivityGoalTest.Harness h, NativeUserRow original, NativeActivityList.State state, String stage) throws Exception {
        layout(h);
        assertSame(stage + ": the submitted user row stays mounted", original, user(h.get("root")));
        assertTrue(stage + ": the prompt stays expanded", original.message.content.expanded);
        assertEquals("Show less", original.message.toggle.getText().toString());
        assertSame(stage + ": workflow state keeps its identity", state, activityState(h));
    }

    @Test public void acceptanceStreamAndNativeHistoryPreserveTheSubmittedRowAndItsExpandedState() throws Exception {
        for (String backend : new String[] {"codex", "claude"})
            for (boolean recycler : new boolean[] {false, true})
                for (boolean existing : new boolean[] {false, true})
                    for (boolean streamFirst : new boolean[] {false, true})
                        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(existing, recycler)) {
                            String id = existing ? "chat-one" : "created-one";
                            String label = backend + "/" + recycler + "/" + existing + "/" + streamFirst;
                            h.set("backend", backend);
                            h.set("agents", new JSONArray().put(new JSONObject().put("backend", backend).put("enabled", true).put("installed", true)));
                            if (existing) { h.set("chat", chat(id, backend, false)); h.call("renderChat"); h.call("startChat"); }
                            JSONObject accepted = chat(id, backend, true);
                            NativeUserRow[] original = new NativeUserRow[1];
                            NativeActivityList.State[] state = new NativeActivityList.State[1];
                            MainActivityGoalTest.ShadowClient.responder = call -> {
                                try {
                                    if (call.get(1).equals("/api/chats")) return envelope(chat(id, backend, false));
                                    // Apply first-chat adoption before delivering a frame on its new subscription.
                                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                                    if (streamFirst) {
                                        event("snapshot", accepted);
                                        stable(h, original[0], state[0], label + " SSE acceptance");
                                    }
                                    return envelope(accepted);
                                } catch (Exception error) { throw new AssertionError(error); }
                            };
                            h.send(PROMPT); layout(h);
                            original[0] = user(h.get("root")); assertNotNull(original[0]);
                            assertEquals(View.VISIBLE, original[0].message.toggle.getVisibility());
                            assertTrue(original[0].message.toggle.performClick()); layout(h);
                            state[0] = activityState(h);
                            int[] detaches = {0};
                            original[0].addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
                                public void onViewAttachedToWindow(View view) {}
                                public void onViewDetachedFromWindow(View view) { detaches[0]++; }
                            });
                            h.io.run();
                            stable(h, original[0], state[0], label + " REST acceptance");
                            assertEquals("server-turn", NativeChatState.runningTurn(h.get("chat")).getString("id"));
                            if (!streamFirst) { event("snapshot", accepted); stable(h, original[0], state[0], label + " SSE replay"); }
                            JSONObject streamed = NativeChatState.copy(accepted).put("updated_at", 21);
                            streamed.getJSONObject("transcript").getJSONArray("parts").put(new JSONObject()
                                    .put("role", "assistant").put("kind", "text").put("phase", "final_answer").put("text", "First streamed reply"));
                            JSONObject turn = streamed.getJSONArray("turns").getJSONObject(0).put("part_end", 2);
                            streamed.put("turn", turn); event("turn", streamed);
                            stable(h, original[0], state[0], label + " first reply");
                            JSONObject nativeChat = NativeChatState.copy(streamed).put("updated_at", 22).put("status", "completed");
                            nativeChat.remove("turn"); nativeChat.getJSONObject("transcript").put("source", backend + "-native");
                            nativeChat.getJSONArray("turns").getJSONObject(0).put("id", "history-turn").put("status", "completed").put("finished_at", 30);
                            event("snapshot", nativeChat);
                            stable(h, original[0], state[0], label + " native history");
                            assertEquals(label + ": updates never detach the user row", 0, detaches[0]);
                        }
    }
}
