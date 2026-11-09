package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.AbstractExecutorService;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.TimeUnit;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.Client;

/** Exercises the actual Activity/API callbacks, replacing only the gomobile transport boundary. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityGoalTest {
    private static String response(int status, String key, String value) throws Exception {
        return new JSONObject().put("status", status).put("body", new JSONObject().put(key, value).toString()).toString();
    }
    @Implements(value = go.Seq.class, isInAndroidSdk = false)
    public static class ShadowSeq {
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected static void setContext(android.content.Context context) {}
    }

    @Implements(value = Client.class, isInAndroidSdk = false)
    public static class ShadowClient {
        @org.robolectric.annotation.RealObject Client real;
        static final List<List<String>> calls = new ArrayList<>();
        static String response;
        static java.util.function.Function<List<String>, String> responder;
        static spoolnet.EventListener chatListener;
        static final List<List<String>> chatSubscriptions = new ArrayList<>();
        static spoolnet.EventListener workspaceListener;
        static final java.util.Map<Client, spoolnet.EventListener> workspaceListeners = new java.util.IdentityHashMap<>();
        static final java.util.Map<Client, spoolnet.EventStream> workspaceStreams = new java.util.IdentityHashMap<>();
        static final List<List<String>> workspaceSubscriptions = new ArrayList<>();
        static java.util.function.Supplier<spoolnet.EventStream> workspaceFactory = () -> null;
        String healthResponse = "{state:'Running',https:'Verified'}";
        long responseAge = -1;
        String sessionDirectory, directOrigin, tailscaleOrigin, relayCode;
        int closes;
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected void __constructor__(String data, String origin) {}
        @Implementation protected String request(String method, String path, String body, String auth, String locale) {
            List<String> call = java.util.Arrays.asList(method, path, body, auth, locale); calls.add(call);
            return responder == null ? response : responder.apply(call);
        }
        @Implementation protected void close() { closes++; }
        @Implementation protected void useServerSession(String directory, String direct, String tailscale) {
            sessionDirectory = directory; directOrigin = direct; tailscaleOrigin = tailscale;
        }
        @Implementation protected void usePairedServerSession(String directory, String direct, String tailscale, String code) {
            useServerSession(directory, direct, tailscale); relayCode = code;
        }
        @Implementation protected String health() { return healthResponse; }
        @Implementation protected String status() { return "{state:'Running',authURL:''}"; }
        @Implementation protected long responseAgeMillis() { return responseAge; }
        @Implementation protected String loginURL() { return "https://login.tailscale.com/a/test"; }
        @Implementation protected spoolnet.EventStream subscribeStatus(spoolnet.EventListener listener) { return null; }
        @Implementation protected void login() {}
        @Implementation protected void networkChanged() {}
        @Implementation protected void reconnect() {}
        @Implementation protected spoolnet.EventStream subscribeChat(String id, String locale, spoolnet.EventListener listener) {
            chatSubscriptions.add(List.of(id, locale)); chatListener = listener; return null;
        }
        @Implementation protected spoolnet.EventStream subscribeWorkspace(String auth, String locale, spoolnet.EventListener listener) {
            workspaceSubscriptions.add(List.of(auth, locale)); workspaceListener = listener; workspaceListeners.put(real, listener);
            spoolnet.EventStream stream = workspaceFactory.get(); workspaceStreams.put(real, stream); return stream;
        }
        @Implementation protected String upload(String id, String body, String auth, String locale) {
            return request("UPLOAD", "/api/chats/" + id + "/messages", body, auth, locale);
        }
    }

    static final class Queue extends AbstractExecutorService {
        final List<Runnable> jobs = new ArrayList<>();
        final boolean immediate;
        Queue() { this(false); }
        Queue(boolean immediate) { this.immediate = immediate; }
        boolean stopped;
        @Override public void execute(Runnable task) { if (immediate) task.run(); else jobs.add(task); }
        void run() { assertEquals(1, jobs.size()); jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }
        @Override public void shutdown() { stopped = true; }
        @Override public List<Runnable> shutdownNow() { stopped = true; List<Runnable> rest = new ArrayList<>(jobs); jobs.clear(); return rest; }
        @Override public boolean isShutdown() { return stopped; }
        @Override public boolean isTerminated() { return stopped && jobs.isEmpty(); }
        @Override public boolean awaitTermination(long timeout, TimeUnit unit) { return isTerminated(); }
    }

    static final class Harness implements AutoCloseable {
        final ActivityController<MainActivity> controller;
        final MainActivity activity;
        final Queue io = new Queue();
        final Queue refreshIO = new Queue();
        Harness(boolean existing) throws Exception { this(existing, null); }
        Harness(boolean existing, Boolean useMarkwonHistory) throws Exception {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
            ShadowClient.calls.clear(); ShadowClient.chatSubscriptions.clear(); ShadowClient.responder = null; ShadowClient.chatListener = null; ShadowClient.response = response(200, "message", "Goal details");
            ShadowClient.workspaceSubscriptions.clear(); ShadowClient.workspaceListeners.clear(); ShadowClient.workspaceStreams.clear(); ShadowClient.workspaceListener = null; ShadowClient.workspaceFactory = () -> null;
            controller = Robolectric.buildActivity(MainActivity.class).create().start().resume().visible();
            activity = controller.get();
            if (useMarkwonHistory != null) set("useMarkwonHistory", useMarkwonHistory);
            // Workspace tests explicitly enter the workspace after the startup sidebar.
            call("closeDrawer");
            set("workspaceDecode", (java.util.concurrent.Executor) Runnable::run);
            ((ExecutorService) ReflectionHelpers.getField(activity, "io")).shutdown();
            ((ExecutorService) ReflectionHelpers.getField(activity, "workspaceWorker")).shutdown();
            ((ExecutorService) ReflectionHelpers.getField(activity, "workspaceSocketWorker")).shutdown();
            ((ExecutorService) ReflectionHelpers.getField(activity, "workspaceProbeWorker")).shutdown();
            ((ExecutorService) ReflectionHelpers.getField(activity, "streamCloser")).shutdown();
            set("streamCloser", new Queue(true));
            // Existing ordered boundary tests share a queue; isolation tests inject separate queues.
            set("workspaceWorker", io);
            set("workspaceSocketWorker", io); set("workspaceProbeWorker", io);
            set("io", io); set("language", "en"); set("projectID", "project-one");
            android.os.Handler main = new android.os.Handler(Looper.getMainLooper());
            set("chatRefresh", new NativeChatRefresh<JSONObject>(refreshIO, new NativeChatRefresh.Dispatcher() {
                public void post(Runnable task) { main.post(task); }
                public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
                public void remove(Runnable task) { main.removeCallbacks(task); }
            }));
            set("user", new JSONObject().put("id", "alice"));
            set("projects", new JSONArray().put(new JSONObject().put("id", "project-one").put("name", "Project").put("can_create", true)));
            set("agents", new JSONArray().put(new JSONObject().put("backend", "codex").put("enabled", true).put("installed", true)));
            set("permission", "ask"); set("permissionCatalog", new JSONObject().put("modes", new JSONArray().put(new JSONObject().put("id", "ask").put("available", true))));
            set("csrf", "test-csrf"); set("client", new Client("unused", "https://fixture.invalid")); set("connected", true);
            set("avatarImages", new NativeAvatars(task -> {}, (style, seed) -> { throw new AssertionError("No avatar generation in Activity request tests"); }));
            if (existing) {
                set("chatID", "chat-one"); set("chat", chat("chat-one"));
            }
            call("render"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertTrue(io.jobs.isEmpty());
        }
        JSONObject chat(String id) throws Exception { return new JSONObject().put("id", id).put("backend", "codex").put("can_control", true).put("status", "completed").put("turns", new JSONArray()); }
        void set(String name, Object value) { ReflectionHelpers.setField(activity, name, value); }
        <T> T get(String name) { return ReflectionHelpers.getField(activity, name); }
        void call(String name) { ReflectionHelpers.callInstanceMethod(activity, name); }
        NativeUi.Composer composer() { return get("composerView"); }
        void send(String text) { composer().input.setText(text); call("sendOrStop"); }
        @Override public void close() { set("client", null); io.jobs.clear(); controller.pause().stop().destroy(); refreshIO.jobs.clear(); }
    }

    @Test public void getAndClearUseTheGoalApiWithoutCreatingChatsOrTranscriptTurns() throws Exception {
        for (String command : new String[] {"/goal", "/goal clear"}) try (Harness h = new Harness(true)) {
            JSONObject original = h.get("chat");
            h.send(command); assertTrue(h.<Boolean>get("busy")); assertEquals(1, h.io.jobs.size());
            h.io.run(); assertFalse(h.<Boolean>get("busy"));
            assertEquals(1, ShadowClient.calls.size()); List<String> request = ShadowClient.calls.get(0);
            assertEquals("POST", request.get(0)); assertEquals("/api/chats/chat-one/goal", request.get(1));
            assertEquals(command.equals("/goal") ? "get" : "clear", new JSONObject(request.get(2)).getString("action"));
            assertEquals("test-csrf", request.get(3)); assertEquals("en", request.get(4));
            assertSame(original, h.get("chat")); assertEquals(0, original.getJSONArray("turns").length());
            assertEquals("Goal details", h.<String>get("goalNotice")); assertEquals("", h.composer().input.getText().toString());
            assertNotNull(h.composer().commands.notice);
        }
    }

    @Test public void newChatGoalControlsAreLocalAndInvalidCommandsNeverReachTransport() throws Exception {
        try (Harness h = new Harness(false)) {
            h.send("/goal clear"); assertTrue(h.io.jobs.isEmpty()); assertTrue(ShadowClient.calls.isEmpty());
            assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat"));
            assertEquals("No goal set.", h.<String>get("goalNotice")); assertEquals("", h.composer().input.getText().toString());
            h.send("/goal " + "x".repeat(4001));
            assertEquals("Goal conditions must be at most 4000 characters", h.<String>get("goalError"));
            assertTrue(h.io.jobs.isEmpty()); assertTrue(ShadowClient.calls.isEmpty());
        }
        try (Harness h = new Harness(true)) {
            h.set("backend", "claude");
            h.set("agents", new JSONArray().put(new JSONObject().put("backend", "claude").put("enabled", true).put("installed", true)));
            h.send("/goal");
            assertEquals("Select this chat's current agent to view or clear its goal", h.<String>get("goalError"));
            assertTrue(h.io.jobs.isEmpty()); assertTrue(ShadowClient.calls.isEmpty());
        }
    }

    @Test public void lateResponsesCannotReplaceANewerDraftOrAnotherSelectionAndErrorsAreInline() throws Exception {
        try (Harness h = new Harness(true)) {
            h.send("/goal"); h.composer().input.setText("A newer draft"); h.io.run();
            assertEquals("A newer draft", h.composer().input.getText().toString());
            assertEquals("Goal details", h.<String>get("goalNotice"));
            h.send("/goal clear"); h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1);
            h.set("chatID", "chat-two"); h.set("chat", h.chat("chat-two"));
            h.set("goalNotice", ""); h.composer().input.setText("Other chat draft"); h.io.run();
            assertEquals("", h.<String>get("goalNotice")); assertEquals("Other chat draft", h.composer().input.getText().toString());
            ShadowClient.response = response(409, "error", "Goal cannot be cleared");
            h.send("/goal clear"); h.io.run();
            assertEquals("Goal cannot be cleared", h.<String>get("goalError"));
            assertNotNull(h.composer().commands.error); assertFalse(h.<Boolean>get("busy"));
            assertEquals("/goal clear", h.composer().input.getText().toString());
        }
    }

    @Test public void revokedSessionAndAuthenticationFailureCannotApplyAStaleGoalNotice() throws Exception {
        try (Harness h = new Harness(true)) {
            h.send("/goal"); h.call("clearWorkspace"); h.io.run();
            assertTrue("Queued commands must not run with a revoked session", ShadowClient.calls.isEmpty());
            assertNull(h.get("user")); assertEquals("", h.<String>get("goalNotice"));
            assertEquals("", h.<String>get("goalError")); assertFalse(h.<Boolean>get("busy"));
        }
        try (Harness h = new Harness(true)) {
            h.send("/goal"); assertEquals(1, h.io.jobs.size()); h.io.jobs.remove(0).run();
            assertEquals(1, ShadowClient.calls.size()); assertEquals("", h.<String>get("goalNotice"));
            h.call("clearWorkspace"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNull(h.get("user")); assertEquals("", h.<String>get("goalNotice"));
            assertEquals("", h.<String>get("goalError")); assertFalse(h.<Boolean>get("busy"));
        }
        try (Harness h = new Harness(true)) {
            ShadowClient.response = response(401, "error", "Expired");
            h.send("/goal"); h.io.run();
            assertNull(h.get("user")); assertEquals("", h.<String>get("goalNotice"));
            assertEquals("", h.<String>get("goalError")); assertFalse(h.<Boolean>get("busy"));
            assertNull(h.get("composerView")); assertNotNull(h.get("loginError"));
        }
    }

    @Test public void attachmentsAndReadOnlyOrBusyStateCannotSendGoalCommands() throws Exception {
        try (Harness h = new Harness(true)) {
            Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
            Object file = ReflectionHelpers.callConstructor(type,
                    ReflectionHelpers.ClassParameter.from(String.class, "note.txt"),
                    ReflectionHelpers.ClassParameter.from(String.class, "text/plain"),
                    ReflectionHelpers.ClassParameter.from(byte[].class, new byte[] {65}));
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            List<Object> files = ReflectionHelpers.getField(draft, "files"); files.add(file);
            h.send("/goal clear");
            assertEquals("Goal commands do not accept attachments", h.<String>get("goalError"));
            assertTrue(h.io.jobs.isEmpty()); assertTrue(ShadowClient.calls.isEmpty()); files.clear();
            h.set("goalError", ""); h.set("busy", true); h.send("/goal");
            assertTrue(h.io.jobs.isEmpty()); h.set("busy", false);
            h.<JSONObject>get("chat").put("can_control", false); h.send("/goal");
            assertTrue(h.io.jobs.isEmpty()); assertTrue(ShadowClient.calls.isEmpty());
        }
    }

    @Test public void settingAGoalUsesTheExistingMessageEndpointAndPreservesTheOriginalDraftOnFailure() throws Exception {
        try (Harness h = new Harness(true)) {
            String message = "/goal " + "\ud83d\ude00".repeat(4000);
            ShadowClient.response = response(409, "error", "Fixture message rejection");
            h.send(message); h.io.run();
            assertEquals(1, ShadowClient.calls.size()); List<String> request = ShadowClient.calls.get(0);
            assertEquals("POST", request.get(0)); assertEquals("/api/chats/chat-one/messages", request.get(1));
            JSONObject payload = new JSONObject(request.get(2));
            assertEquals(message, payload.getString("message")); assertEquals("codex", payload.getString("backend"));
            assertEquals("ask", payload.getString("permission_mode"));
            assertEquals(message, h.composer().input.getText().toString()); assertFalse(h.<Boolean>get("busy"));
        }
    }
}
