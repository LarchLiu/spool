package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Executor;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeWorkspaceDataTest {
    private JSONObject reference() throws Exception {
        try (InputStream input = getClass().getResourceAsStream("/web-workspace-data.json")) {
            assertNotNull(input); return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }
    private static final class Queue implements Executor, NativeWorkspaceData.Dispatcher {
        final List<Runnable> jobs = new ArrayList<>();
        final Map<Runnable, Long> timers = new LinkedHashMap<>();
        long clock;
        public void execute(Runnable task) { jobs.add(task); }
        public void post(Runnable task) { jobs.add(task); }
        public void postDelayed(Runnable task, long delay) { timers.put(task, clock + delay); }
        public void remove(Runnable task) { timers.remove(task); }
        void run(int index) { jobs.remove(index).run(); }
        void flush() { while (!jobs.isEmpty()) run(0); }
        void advance(long millis) {
            clock += millis;
            for (Map.Entry<Runnable, Long> entry : new ArrayList<>(timers.entrySet())) if (entry.getValue() <= clock) {
                timers.remove(entry.getKey()); entry.getKey().run();
            }
            flush();
        }
    }
    private static final class Sink implements NativeWorkspaceData.Listener {
        final NativeProfiles profiles;
        final List<JSONObject> navigation = new ArrayList<>();
        final List<Boolean> errors = new ArrayList<>();
        int agents;
        Sink(NativeProfiles profiles) { this.profiles = profiles; }
        public void navigation(JSONObject value) { navigation.add(value); }
        public void agents(JSONArray value) { agents++; }
        public void profiles(JSONObject value) { profiles.accept(value); }
        public void failed(Exception error, boolean initial) { errors.add(initial); }
    }

    @Test public void navigationSelectionAndAuthoritativeMembershipMatchActualWebSequences() throws Exception {
        JSONArray cases = reference().getJSONArray("navigationCases");
        for (int c = 0; c < cases.length(); c++) {
            JSONObject item = cases.getJSONObject(c), initial = item.getJSONObject("initial");
            String project = initial.getString("project"), chat = initial.getString("chatID");
            JSONArray chats = initial.getJSONArray("chats"); NativeNavigation state = new NativeNavigation();
            JSONArray steps = item.getJSONArray("steps");
            for (int i = 0; i < steps.length(); i++) {
                JSONObject step = steps.getJSONObject(i), value = step.getJSONObject("navigation"), expected = step.getJSONObject("expected");
                NativeNavigation.Selection selected = state.select(project, chat, value.getJSONArray("projects"), value.getJSONArray("chats"));
                project = selected.project; chat = selected.chat;
                chats = NativeNavigation.merge(chats, value.getJSONArray("chats"));
                assertEquals(item.getString("id"), expected.getString("project"), project);
                assertEquals(item.getString("id"), expected.getString("chatID"), chat);
                assertTrue(item.getString("id"), NativeChatState.equivalent(expected.getJSONArray("chats"), chats));
            }
        }
    }

    @Test public void batchedRestAndSameRevisionProfileRacesMatchActualWebEffect() throws Exception {
        JSONArray cases = reference().getJSONArray("loadingCases");
        for (int c = 0; c < cases.length(); c++) {
            JSONObject item = cases.getJSONObject(c); String mode = item.getString("mode");
            NativeProfiles profiles = new NativeProfiles(); Sink sink = new Sink(profiles); Queue worker = new Queue(), ui = new Queue();
            NativeWorkspaceData data = new NativeWorkspaceData(worker, ui, profiles);
            data.bind("session", path -> {
                if (mode.equals("navigation-failure") && path.equals("/navigation") || mode.equals("agents-failure") && path.equals("/agents")) throw new Exception("Offline");
                return path.equals("/navigation") ? item.getJSONObject("restNavigation") : path.equals("/profiles") ? item.getJSONObject("restProfiles") : new JSONArray();
            }, sink);
            data.reload(); assertEquals(3, worker.jobs.size());
            worker.run(1); ui.flush();
            assertEquals(item.getJSONObject("beforeAgents").getInt("navigationCount"), sink.navigation.size());
            assertEquals(item.getJSONObject("beforeAgents").getInt("agentCount"), sink.agents);
            if (mode.equals("navigation-push")) data.navigation(item.getJSONObject("pushNavigation"));
            if (mode.equals("profile-push") || mode.equals("profile-save")) {
                profiles.accept(item.getJSONObject("profilePush"));
                if (mode.equals("profile-save")) profiles.acceptSaved(item.getJSONObject("profileSave"), "alice");
            }
            worker.run(1); ui.flush(); worker.run(0); ui.flush();
            JSONObject expected = item.getJSONObject("expected");
            assertEquals(mode, expected.getInt("navigationCount"), sink.navigation.size());
            assertEquals(mode, expected.getInt("agentCount"), sink.agents);
            assertEquals(mode, expected.getJSONArray("failures").length(), sink.errors.stream().filter(Boolean::booleanValue).count());
            assertTrue(mode, NativeChatState.equivalent(expected.getJSONObject("profiles").getJSONArray("people"), profiles.people));
            data.stop(); assertTrue(ui.timers.isEmpty());
        }
    }

    @Test public void missingIdentitiesUseTheSameWebBatchKeyAndFiftyMillisecondDebounce() throws Exception {
        JSONArray cases = reference().getJSONArray("missingCases");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i); NativeProfiles profiles = new NativeProfiles(); profiles.accept(item.getJSONObject("profiles"));
            assertEquals(item.getString("expected"), profiles.missing(item.getJSONArray("chats"), item.optJSONObject("chat"), item.getJSONArray("online")));
        }
        NativeProfiles profiles = new NativeProfiles(); Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink(profiles);
        NativeWorkspaceData data = new NativeWorkspaceData(worker, ui, profiles);
        List<String> reads = new ArrayList<>();
        data.bind("session", path -> { reads.add(path); return path.equals("/agents") ? new JSONArray() : new JSONObject(); }, sink);
        data.missing("alice,bob"); data.missing("alice,bob"); ui.advance(50);
        assertEquals("Missing lookup coalesces with the initial batch", 3, worker.jobs.size());
        worker.flush(); ui.flush(); assertEquals(1, reads.stream().filter("/profiles"::equals).count());
        data.missing(""); data.missing("alice,bob"); ui.advance(49); assertTrue(worker.jobs.isEmpty());
        ui.advance(1); assertEquals(1, worker.jobs.size());
        data.missing(""); worker.flush(); ui.flush(); assertEquals(2, reads.stream().filter("/profiles"::equals).count());
        data.missing("changed"); data.missing(""); ui.advance(50); assertTrue(worker.jobs.isEmpty()); data.stop();
    }

    @Test public void fifteenSecondFallbackStopsWhenReadyAndRestartsOnlyWhenDisconnected() {
        NativeProfiles profiles = new NativeProfiles(); Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink(profiles);
        NativeWorkspaceData data = new NativeWorkspaceData(worker, ui, profiles);
        data.bind("session", path -> path.equals("/agents") ? new JSONArray() : new JSONObject(), sink);
        worker.flush(); ui.flush(); data.ready(true); ui.advance(15000); assertTrue(worker.jobs.isEmpty());
        data.ready(false); ui.advance(14999); assertTrue(worker.jobs.isEmpty()); ui.advance(1); assertEquals(3, worker.jobs.size());
        ui.advance(15000); assertEquals("Do not accumulate batches behind a slow response", 3, worker.jobs.size());
        data.stop(); worker.flush(); ui.flush(); assertTrue(ui.timers.isEmpty()); assertEquals(1, sink.navigation.size());
    }

    @Test public void stopAndRebindRevokeQueuedReadsPostedRepliesAndOldSameKeyScopes() {
        for (boolean alreadyRead : new boolean[] {false, true}) {
            NativeProfiles profiles = new NativeProfiles(); Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink(profiles);
            NativeWorkspaceData data = new NativeWorkspaceData(worker, ui, profiles); int[] reads = {0};
            NativeWorkspaceData.Source source = path -> { reads[0]++; return path.equals("/agents") ? new JSONArray() : new JSONObject(); };
            data.bind("same", source, sink); if (alreadyRead) worker.flush(); data.stop();
            worker.flush(); ui.flush(); assertTrue(sink.navigation.isEmpty()); assertEquals(0, sink.agents);
            assertEquals(alreadyRead ? 3 : 0, reads[0]);
            data.bind("same", source, sink); worker.flush(); ui.flush(); assertEquals(1, sink.navigation.size()); data.stop();
        }
    }

    @Test public void aFailedBatchCannotSuppressAnOutstandingAuthenticationFailure() {
        NativeProfiles profiles = new NativeProfiles(); Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink(profiles);
        NativeWorkspaceData data = new NativeWorkspaceData(worker, ui, profiles);
        data.bind("session", path -> { if (path.equals("/profiles")) return new JSONObject(); throw new Exception(path); }, sink);
        worker.run(1); ui.flush(); worker.run(1); ui.flush();
        assertEquals(List.of(true, false), sink.errors); assertTrue(sink.navigation.isEmpty()); data.stop();
    }
}
