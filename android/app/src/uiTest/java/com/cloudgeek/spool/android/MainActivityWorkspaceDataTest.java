package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.os.SystemClock;
import java.time.Duration;
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

/** Production Activity callbacks with only the gomobile transport replaced. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class, MainActivityWorkspaceEventsTest.ShadowStream.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityWorkspaceDataTest {
    private String envelope(Object value) {
        try { return new JSONObject().put("status", 200).put("body", value.toString()).toString(); }
        catch (Exception error) { throw new IllegalArgumentException(error); }
    }
    private JSONObject nav(String title) throws Exception {
        JSONObject result = new JSONObject("{projects:[{id:'project-one',name:'Project',can_create:true}],chats:[{id:'chat-one',project_id:'project-one',updated_at:10,can_control:true,participants:[]}]}");
        result.getJSONArray("chats").getJSONObject(0).put("title", title); return result;
    }
    private void source(JSONObject navigation, JSONObject profiles) {
        MainActivityGoalTest.ShadowClient.responder = call -> envelope(call.get(1).equals("/api/navigation") ? navigation
                : call.get(1).equals("/api/profiles") ? profiles : new JSONArray());
    }
    private void event(MainActivityGoalTest.Harness h, String kind, Object value) {
        ReflectionHelpers.callInstanceMethod(h.activity, "workspaceEvent", ReflectionHelpers.ClassParameter.from(String.class, kind),
                ReflectionHelpers.ClassParameter.from(String.class, value.toString()), ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("workspaceEpoch")));
    }
    private void run(MainActivityGoalTest.Harness h, int index) { h.io.jobs.remove(index).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    private void decode(MainActivityGoalTest.Harness h, Object value) {
        ReflectionHelpers.callInstanceMethod(h.activity, "decodeWorkspaceEvent", ReflectionHelpers.ClassParameter.from(String.class, "navigation"),
                ReflectionHelpers.ClassParameter.from(String.class, value.toString()), ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("workspaceEpoch")));
    }

    @Test public void workspacePayloadsDecodeOffTheUiQueueInOrderAndOldConnectionsCannotPublish() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.Queue decoder = new MainActivityGoalTest.Queue(); h.set("workspaceDecode", decoder);
            JSONArray original = h.get("chats"); decode(h, nav("First")); decode(h, nav("Second"));
            assertSame(original, h.get("chats")); assertEquals(2, decoder.jobs.size());
            decoder.jobs.remove(0).run(); assertSame("Decoded data still waits for the UI commit", original, h.get("chats"));
            Shadows.shadowOf(Looper.getMainLooper()).idle(); assertEquals("First", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            decoder.run(); assertEquals("Second", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            decode(h, nav("Stale")); decoder.jobs.remove(0).run(); h.set("workspaceEpoch", h.<Integer>get("workspaceEpoch") + 1);
            Shadows.shadowOf(Looper.getMainLooper()).idle(); assertEquals("Second", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            decode(h, nav("Revoked")); h.set("authEpoch", h.<Integer>get("authEpoch") + 1); decoder.run();
            assertEquals("Second", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void productionBatchesInitialReadsAndPushesFenceBothNavigationAndAgents() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            source(nav("REST"), new JSONObject("{revision:1,people:[{id:'alice',nickname:'Alice'}]}"));
            JSONArray original = h.get("chats"), originalAgents = h.get("agents");
            h.call("refreshWorkspace"); h.call("refreshWorkspace"); assertEquals(3, h.io.jobs.size());
            run(h, 1); assertSame(original, h.get("chats")); assertSame(originalAgents, h.get("agents"));
            event(h, "navigation", nav("Push"));
            JSONArray pushedAgents = new JSONArray("[{backend:'claude',enabled:true,installed:true}]"); event(h, "agents", pushedAgents);
            run(h, 1); run(h, 0);
            assertEquals("Push", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            assertEquals("claude", h.<JSONArray>get("agents").getJSONObject(0).getString("backend"));
            assertEquals(3, MainActivityGoalTest.ShadowClient.calls.size());
            assertEquals(List.of("/api/navigation", "/api/agents", "/api/profiles"), MainActivityGoalTest.ShadowClient.calls.stream().map(call -> call.get(1)).toList());
            for (List<String> call : MainActivityGoalTest.ShadowClient.calls) { assertEquals("GET", call.get(0)); assertEquals("en", call.get(4)); }
        }
    }

    @Test public void sameRevisionProfilePushCannotBeUndoneByAnAlreadyReadRestReply() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            source(nav("REST"), new JSONObject("{revision:4,people:[{id:'alice',nickname:'Stale'},{id:'revoked',nickname:'Revoked'}]}"));
            h.call("refreshWorkspace"); h.io.jobs.remove(0).run();
            event(h, "profiles", new JSONObject("{revision:4,people:[{id:'alice',nickname:'Current'},{id:'new',nickname:'New'}]}"));
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            JSONArray profiles = h.get("profiles"); assertEquals(2, profiles.length());
            assertEquals("Current", profiles.getJSONObject(0).getString("nickname")); assertEquals("new", profiles.getJSONObject(1).getString("id"));
            assertEquals("Current", h.<JSONObject>get("user").getString("nickname"));
        }
    }

    @Test public void missingIdentitiesTriggerOneDebouncedDirectoryReadNotOneRequestPerAvatar() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            source(nav("REST"), new JSONObject("{revision:1,people:[{id:'alice',nickname:'Alice'}]}"));
            h.call("refreshWorkspace"); run(h, 0); run(h, 0); run(h, 0);
            event(h, "presence", new JSONArray("[{id:'bob'},{id:'carol'}]"));
            event(h, "presence", new JSONArray("[{id:'bob'},{id:'carol'}]"));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(49)); assertTrue(h.io.jobs.isEmpty());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1)); assertEquals(1, h.io.jobs.size());
            run(h, 0); assertEquals("/api/profiles", MainActivityGoalTest.ShadowClient.calls.get(3).get(1));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(100)); assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void clearingASessionRevokesQueuedAndPostedWorkspaceReads() throws Exception {
        for (boolean alreadyRead : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            source(nav("REST"), new JSONObject("{revision:1,people:[{id:'alice',nickname:'Stale'}]}")); h.call("refreshWorkspace");
            List<Runnable> requests = new ArrayList<>(h.io.jobs); h.io.jobs.clear();
            if (alreadyRead) for (Runnable task : requests) task.run();
            h.call("clearWorkspace"); if (!alreadyRead) for (Runnable task : requests) task.run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNull(h.get("user")); assertEquals(0, h.<JSONArray>get("profiles").length()); assertEquals(0, h.<JSONArray>get("chats").length());
            assertEquals(alreadyRead ? 3 : 0, MainActivityGoalTest.ShadowClient.calls.size());
        }
    }

    @Test public void initialNavigationSelectsTheWebDefaultAndLaterBlankNewChatStaysBlank() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            ReflectionHelpers.callInstanceMethod(h.activity, "acceptNavigation", ReflectionHelpers.ClassParameter.from(JSONObject.class, nav("Default")));
            assertEquals("chat-one", h.<String>get("chatID")); assertNull(h.get("chat")); assertFalse(h.composer().input.isEnabled());
            h.io.jobs.clear();
            ReflectionHelpers.callInstanceMethod(h.activity, "newChat", ReflectionHelpers.ClassParameter.from(String.class, "project-one"));
            h.io.jobs.clear();
            ReflectionHelpers.callInstanceMethod(h.activity, "acceptNavigation", ReflectionHelpers.ClassParameter.from(JSONObject.class, nav("Still listed")));
            assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat"));
        }
    }

    @Test public void authoritativeMembershipRevokesAccessWithoutRegressingNewerChatMetadata() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject live = h.get("chat"); live.put("updated_at", 20).put("title", "Live title").put("participants", new JSONArray("[{id:'bob'}]"));
            h.set("chats", new JSONArray().put(live));
            JSONObject incoming = nav("Older title"); incoming.getJSONArray("chats").getJSONObject(0).put("can_control", false);
            ReflectionHelpers.callInstanceMethod(h.activity, "acceptNavigation", ReflectionHelpers.ClassParameter.from(JSONObject.class, incoming));
            assertEquals("Live title", h.<JSONObject>get("chat").getString("title"));
            assertEquals("bob", h.<JSONObject>get("chat").getJSONArray("participants").getJSONObject(0).getString("id"));
            assertFalse(h.<JSONObject>get("chat").getBoolean("can_control")); assertFalse(h.composer().input.isEnabled());
            assertEquals("Live title", h.<android.widget.TextView>get("heading").getText().toString());
            incoming.put("chats", new JSONArray());
            ReflectionHelpers.callInstanceMethod(h.activity, "acceptNavigation", ReflectionHelpers.ClassParameter.from(JSONObject.class, incoming));
            assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat")); assertEquals(0, h.<JSONArray>get("chats").length());
        }
    }

    @Test public void languageRebindingRevokesOldQueuedReadsWithoutReloadingForUnchangedRenders() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            source(nav("REST"), new JSONObject("{revision:1,people:[{id:'alice',nickname:'Alice'}]}"));
            h.call("refreshWorkspace"); h.call("render"); h.call("render"); assertEquals(3, h.io.jobs.size());
            h.set("language", "ja"); h.call("render"); assertEquals(6, h.io.jobs.size());
            run(h, 0); run(h, 0); run(h, 0); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            run(h, 0); run(h, 0); run(h, 0); assertEquals(3, MainActivityGoalTest.ShadowClient.calls.size());
            for (List<String> call : MainActivityGoalTest.ShadowClient.calls) assertEquals("ja", call.get(4));
        }
    }

    @Test public void socketReconnectDoesNotTurnTheFifteenSecondDataFallbackIntoRapidPolling() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            source(nav("REST"), new JSONObject("{revision:1,people:[{id:'alice',nickname:'Alice'}]}"));
            long boundAt = SystemClock.uptimeMillis(); h.call("refreshWorkspace");
            assertEquals("The fallback is scheduled at binding, before response rendering", boundAt, SystemClock.uptimeMillis());
            run(h, 0); run(h, 0); run(h, 0);
            MainActivityWorkspaceEventsTest.install(h); h.call("startWorkspace"); h.io.run();
            MainActivityWorkspaceEventsTest.event("ready", "{}");
            MainActivityWorkspaceEventsTest.event("transport_error", "{}");
            long lostAt = SystemClock.uptimeMillis();
            assertEquals("Authentication is checked immediately, independently of the retry", 1, h.io.jobs.size());
            run(h, 0); assertEquals("/api/me", MainActivityGoalTest.ShadowClient.calls.get(3).get(1));
            MainActivityWorkspaceEventsTest.until(lostAt + 549); assertTrue(h.io.jobs.isEmpty());
            MainActivityWorkspaceEventsTest.until(lostAt + 550);
            assertEquals("Only the socket retry; not three more REST data reads", 1, h.io.jobs.size());
            h.io.run();
            // Native drawing advances Robolectric's clock too; assert the absolute bind deadline.
            long beforeFallback = boundAt + 14999;
            assertTrue("Response rendering must finish before the fallback deadline", SystemClock.uptimeMillis() <= beforeFallback);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(beforeFallback - SystemClock.uptimeMillis()));
            assertEquals(beforeFallback, SystemClock.uptimeMillis()); assertTrue(h.io.jobs.isEmpty());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1)); assertEquals(3, h.io.jobs.size());
            run(h, 0); run(h, 0); run(h, 0);
            for (String path : List.of("/api/navigation", "/api/agents", "/api/profiles"))
                assertEquals(path, 2, MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).equals(path)).count());
        }
    }

    @Test public void stalledWorkspaceReadsCannotOccupyTheSendAndStopExecutor() throws Exception {
        MainActivityGoalTest.Queue workspace = new MainActivityGoalTest.Queue();
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("workspaceWorker", workspace);
            h.call("refreshWorkspace"); assertEquals(3, workspace.jobs.size()); assertTrue(h.io.jobs.isEmpty());
            JSONObject chat = h.get("chat");
            chat.put("status", "running").put("updated_at", 10).put("transcript", new JSONObject().put("parts", new JSONArray()));
            chat.put("turns", new JSONArray("[{id:'server-turn',status:'running',backend:'codex',part_start:0,input_end:0,part_end:0}]"));
            h.call("renderChat"); h.call("sendOrStop"); assertEquals(1, h.io.jobs.size());
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try { return envelope(new JSONObject(chat.toString()).put("status", "stopped").put("updated_at", 20)
                        .put("turns", new JSONArray("[{id:'server-turn',status:'stopped',backend:'codex',part_start:0,input_end:0,part_end:0}]"))); }
                catch (Exception error) { throw new AssertionError(error); }
            };
            run(h, 0);
            assertEquals("/api/chats/chat-one/stop", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            assertEquals("POST", MainActivityGoalTest.ShadowClient.calls.get(0).get(0));
            assertEquals("stopped", h.<JSONObject>get("chat").getString("status"));
            assertEquals("All background reads remain stalled while stop completes", 3, workspace.jobs.size());
        }
        for (Runnable task : workspace.jobs) task.run();
        assertEquals("Revoked background reads never reach the transport", 1, MainActivityGoalTest.ShadowClient.calls.size());
    }
}
