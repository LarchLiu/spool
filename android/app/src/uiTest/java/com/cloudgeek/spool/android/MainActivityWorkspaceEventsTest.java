package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.graphics.Rect;
import android.net.ConnectivityManager;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import java.time.Duration;
import java.util.ArrayList;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.annotation.RealObject;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.EventListener;
import spoolnet.EventStream;

/** Production lifecycle and people callbacks; only the generated Go boundary is replaced. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class, MainActivityWorkspaceEventsTest.ShadowStream.class, MainActivityWorkspaceEventsTest.ShadowSpoolnet.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityWorkspaceEventsTest {
    @Implements(value = spoolnet.Spoolnet.class, isInAndroidSdk = false)
    public static class ShadowSpoolnet {
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected static void updateInterfaces(String snapshot) {}
    }

    @Implements(value = EventStream.class, isInAndroidSdk = false)
    public static class ShadowStream {
        // gomobile value equality does not identify different connection handles.
        static final Map<EventStream, ShadowStream> streams = new IdentityHashMap<>();
        @RealObject EventStream stream;
        final List<JSONObject> sent = new ArrayList<>();
        boolean closed, fail;
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected void __constructor__() { streams.put(stream, this); }
        @Implementation protected void close() { closed = true; }
        @Implementation protected void send(String raw) throws Exception {
            if (fail) throw new IllegalStateException("Fixture send failed");
            sent.add(new JSONObject(raw));
        }
    }

    static void install(MainActivityGoalTest.Harness h) {
        ShadowStream.streams.clear();
        MainActivityGoalTest.ShadowClient.workspaceFactory = EventStream::new;
        Handler main = new Handler(Looper.getMainLooper());
        h.set("workspaceEvents", new NativeWorkspaceEvents<EventStream>(h.io, new NativeWorkspaceEvents.Scheduler() {
            public void post(Runnable task) { main.post(task); }
            public void postDelayed(Runnable task, long millis) { main.postDelayed(task, millis); }
            public void remove(Runnable task) { main.removeCallbacks(task); }
            public long now() { return SystemClock.elapsedRealtime(); }
            public double random() { return .5; }
        }));
    }
    static void event(String kind, String raw) {
        MainActivityGoalTest.ShadowClient.workspaceListener.onEvent(kind, raw);
        Shadows.shadowOf(Looper.getMainLooper()).idle();
    }
    static void run(MainActivityGoalTest.Harness h) { h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    static void until(long time) {
        assertTrue("Drawing must not pass the asserted absolute deadline", SystemClock.uptimeMillis() <= time);
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(time - SystemClock.uptimeMillis()));
    }
    private ShadowStream start(MainActivityGoalTest.Harness h) {
        install(h); h.call("startWorkspace"); h.io.run(); event("ready", "{}");
        return ShadowStream.streams.get(h.<EventStream>get("workspaceStream"));
    }
    private void presence() { event("presence", "[{id:'bob',nickname:'Bob',state:'online'}]"); }
    private void open(MainActivityGoalTest.Harness h) {
        ReflectionHelpers.callInstanceMethod(h.activity, "openPeople", ReflectionHelpers.ClassParameter.from(String.class, "bob"));
    }

    private NativePeopleMenu onlineMenu(MainActivityGoalTest.Harness h, boolean dark) throws Exception {
        JSONObject bob = new JSONObject().put("id", "bob").put("nickname", "Bob").put("state", "online");
        h.set("profiles", new JSONArray().put(new JSONObject().put("id", "alice").put("nickname", "Alice")).put(bob));
        h.set("presence", new JSONArray().put(bob));
        h.set("connected", true); h.set("workspaceReady", true); h.set("theme", dark ? "dark" : "light");
        h.call("render");
        View root = h.get("root");
        for (int i = 0; i < 2; i++) {
            root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
            root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
        }
        NativeUi.Header header = h.get("workspaceHeader");
        assertTrue(header.online.performClick());
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        NativePeopleMenu menu = h.get("peopleMenu");
        assertNotNull(menu); assertTrue(menu.isShowing());
        return menu;
    }

    @Test public void onlineMenuOpensPrivateChatOnTheFirstTouch() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                NativePeopleMenu menu = onlineMenu(h, dark);
                View root = h.get("root");
                View row = menu.options.stream().filter(option -> "bob".equals(option.getTag())).findFirst().orElseThrow();
                assertFalse("The first touch must activate an unfocused person", row.hasFocus());
                Rect visible = new Rect(); assertTrue(row.getGlobalVisibleRect(visible));
                assertEquals(row.getWidth(), visible.width()); assertEquals(row.getHeight(), visible.height());
                View decor = menu.popover.panel().getRootView();
                int[] origin = new int[2], position = new int[2];
                decor.getLocationOnScreen(origin); row.getLocationOnScreen(position);
                float x = position[0] - origin[0] + row.getWidth() / 2f;
                float y = position[1] - origin[1] + row.getHeight() / 2f;
                long now = SystemClock.uptimeMillis();
                for (int action : new int[] {MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP}) {
                    MotionEvent event = MotionEvent.obtain(now, now + action * 16, action, x, y, 0);
                    try { assertTrue(decor.dispatchTouchEvent(event)); } finally { event.recycle(); }
                }
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertFalse("A single tap dismisses the online menu", menu.isShowing());
                NativePeoplePanel panel = h.get("peoplePanel");
                assertNotNull("A single tap opens the actual private chat window", panel);
                assertSame(root, panel.getParent());
                assertEquals("bob", h.<NativePeopleModel>get("inbox").active);
                assertEquals("Bob", panel.getContentDescription());
                assertTrue(panel.editor.isFocused());
                assertTrue("Opening a private chat does not send a message", MainActivityGoalTest.ShadowClient.calls.isEmpty());
            }
        }
    }

    @Test public void outsideTouchesDismissOnlyTheOnlineMenuAndAllowReopening() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                NativePeopleMenu menu = onlineMenu(h, dark);
                for (int mode = 0; mode < 3; mode++) {
                    if (mode > 0) {
                        NativeUi.Header header = h.get("workspaceHeader");
                        assertTrue(header.online.performClick());
                        Shadows.shadowOf(Looper.getMainLooper()).idle();
                        menu = h.get("peopleMenu"); assertTrue(menu.isShowing());
                    }
                    NativePopupFrame frame = (NativePopupFrame) menu.popover.panel().getParent();
                    View decor = frame.getRootView();
                    int[] origin = new int[2], root = new int[2], content = new int[2];
                    decor.getLocationOnScreen(origin); h.<View>get("root").getLocationOnScreen(root); frame.getLocationOnScreen(content);
                    float x = mode == 1 ? content[0] - origin[0] + frame.panel.left : root[0] - origin[0] + 180;
                    float y = mode == 1 ? content[1] - origin[1] + frame.panel.top : root[1] - origin[1] + 760;
                    int action = mode == 2 ? MotionEvent.ACTION_OUTSIDE : MotionEvent.ACTION_DOWN;
                    MotionEvent event = MotionEvent.obtain(0, 0, action, x, y, 0);
                    try { assertTrue(decor.dispatchTouchEvent(event)); } finally { event.recycle(); }
                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                    assertFalse("Outside-window, rounded-corner and system outside touches dismiss the list", menu.isShowing());
                    assertNull(h.<NativeUi>get("ui").activePopover);
                    assertNull(ReflectionHelpers.getField(menu.popover, "observer"));
                    assertNull(h.get("peoplePanel"));
                    assertEquals("", h.<NativePeopleModel>get("inbox").active);
                    assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
                }
            }
        }
    }

    @Test public void disconnectClearsPresenceAndPendingOperationsWithoutLosingOrReplayingTheDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream old = start(h); presence(); open(h);
            NativeUi.Header header = h.get("workspaceHeader");
            assertTrue(header.online.getChildAt(0) instanceof NativeAvatarStack);
            NativePeoplePanel panel = h.get("peoplePanel"); panel.editor.setText("Keep this draft"); h.call("sendPeople");
            NativePeopleModel inbox = h.get("inbox"); assertEquals(1, inbox.sending.size());
            List<String> errors = new ArrayList<>();
            h.<Map<String, NativeGroupPicker.Completion>>get("pendingGroups").put("pending-group", (group, error) -> errors.add(error));
            event("transport_error", "{status:503}");
            assertTrue(old.closed); assertFalse(h.<Boolean>get("workspaceReady")); assertNull(h.get("workspaceStream"));
            assertEquals(0, h.<JSONArray>get("presence").length()); assertFalse(header.online.getChildAt(0) instanceof NativeAvatarStack);
            assertEquals("0 online", header.online.getContentDescription());
            assertEquals("Reconnecting…", panel.status.getText().toString()); assertFalse(panel.send.isEnabled());
            assertEquals("Keep this draft", panel.editor.getText().toString()); assertEquals("Keep this draft", inbox.draft("bob"));
            assertTrue(inbox.sending.isEmpty()); assertEquals("unknown", inbox.errors.get("bob")); assertEquals(List.of("unknown"), errors);
            assertTrue(h.<Map<?, ?>>get("pendingGroups").isEmpty());
            run(h); assertTrue("Queued old-connection send is revoked", old.sent.isEmpty());
            run(h); assertEquals("/api/me", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(550)); h.io.run(); event("ready", "{}"); presence();
            assertEquals("Keep this draft", inbox.draft("bob")); assertTrue(inbox.sending.isEmpty());
            assertTrue(ShadowStream.streams.get(h.<EventStream>get("workspaceStream")).sent.isEmpty());
            assertTrue("Reconnect does not replay people messages or group requests", h.io.jobs.isEmpty());
        }
    }

    @Test public void productionRetryUsesWebBackoffAndOnlyReadyResetsAttempts() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h);
            for (long delay : new long[] {550, 1100, 550}) {
                event("transport_error", "{}"); long lostAt = SystemClock.uptimeMillis();
                assertEquals(1, h.io.jobs.size()); h.io.run();
                until(lostAt + delay - 1); assertTrue(h.io.jobs.isEmpty());
                until(lostAt + delay); h.io.run();
                if (delay == 1100) event("ready", "{}");
            }
            assertEquals(4, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            assertEquals(3, MainActivityGoalTest.ShadowClient.calls.size());
            for (List<String> call : MainActivityGoalTest.ShadowClient.calls) assertEquals(List.of("GET", "/api/me", "", "test-csrf", "en"), call);
        }
    }

    @Test public void healthyFocusAndNetworkResumeSendRefreshWithoutWorkspaceGetRequests() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream stream = start(h);
            h.activity.onWindowFocusChanged(true); h.io.run();
            h.<ConnectivityManager.NetworkCallback>get("networkCallback").onAvailable(null); h.io.run();
            assertEquals(2, stream.sent.size());
            for (JSONObject value : stream.sent) { assertEquals("refresh", value.getString("type")); assertTrue(value.getBoolean("visible")); }
            assertEquals(1, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void disconnectedFocusBypassesRetryAndCoalescesForegroundDataReads() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream old = start(h); event("transport_error", "{}"); h.io.run();
            h.activity.onWindowFocusChanged(true);
            assertEquals("One connection and three batched REST reads", 4, h.io.jobs.size());
            h.<ConnectivityManager.NetworkCallback>get("networkCallback").onAvailable(null);
            assertEquals("The pending connection is replaced, REST reads stay coalesced", 5, h.io.jobs.size());
            run(h); assertEquals("Superseded socket work is fenced", 1, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            assertEquals(4, h.io.jobs.size());
            assertTrue(old.closed);
            // Leave the three data reads queued so a fixture response cannot alter the selected chat.
            h.io.jobs.remove(3).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); event("ready", "{}");
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1100));
            assertEquals("The bypassed backoff timer cannot reconnect again", 2, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            assertEquals(3, h.io.jobs.size());
        }
    }

    @Test public void pingFramesRearmTheFortyFiveSecondWatchdogAndNeverReachTheJsonUiParser() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream stream = start(h); presence();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(44000)); event("ping", "null");
            long pingAt = SystemClock.uptimeMillis(); until(pingAt + 44999);
            assertTrue(h.<Boolean>get("workspaceReady")); assertFalse(stream.closed); assertTrue(h.io.jobs.isEmpty());
            until(pingAt + 45000); assertFalse(h.<Boolean>get("workspaceReady")); assertTrue(stream.closed);
            assertEquals(0, h.<JSONArray>get("presence").length()); assertEquals(1, h.io.jobs.size());
        }
    }

    @Test public void policyClosureAndUnauthorizedAuthCheckExpireTheSessionButOldFramesCannot() throws Exception {
        for (boolean policy : new boolean[] {true, false}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream stream = start(h); presence();
            if (policy) event("auth_expired", "{}");
            else {
                MainActivityGoalTest.ShadowClient.response = "{status:401,body:'{\"error\":\"Expired\"}'}";
                event("transport_error", "{}"); h.io.run();
            }
            assertTrue(stream.closed); assertNull(h.get("user")); assertNull(h.get("composerView")); assertNotNull(h.get("loginError"));
            assertEquals(0, h.<JSONArray>get("presence").length());
            event("presence", "[{id:'revoked',state:'online'}]"); event("ready", "{}");
            assertFalse(h.<Boolean>get("workspaceReady")); assertEquals(0, h.<JSONArray>get("presence").length());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(5000)); assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void sessionAndForegroundRebindingRevokeOldCallbacksAndQueuedSends() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream first = start(h); presence(); EventListener english = MainActivityGoalTest.ShadowClient.workspaceListener;
            h.activity.onWindowFocusChanged(true); assertEquals(1, h.io.jobs.size());
            h.set("authEpoch", h.<Integer>get("authEpoch") + 1); h.set("csrf", "new-csrf"); h.call("render"); assertTrue(first.closed); run(h); assertTrue(first.sent.isEmpty()); h.io.run();
            assertEquals(List.of("new-csrf", "en"), MainActivityGoalTest.ShadowClient.workspaceSubscriptions.get(1));
            english.onEvent("auth_expired", "{}"); english.onEvent("presence", "[{id:'old',state:'online'}]"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNotNull(h.get("user")); assertEquals(0, h.<JSONArray>get("presence").length());
            event("ready", "{}"); presence(); EventListener japanese = MainActivityGoalTest.ShadowClient.workspaceListener;
            ShadowStream second = ShadowStream.streams.get(h.<EventStream>get("workspaceStream"));
            h.controller.pause().stop(); assertTrue(second.closed); assertFalse(h.<Boolean>get("workspaceReady"));
            japanese.onEvent("ready", "{}"); japanese.onEvent("presence", "[{id:'old',state:'online'}]"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(0, h.<JSONArray>get("presence").length()); assertTrue(h.io.jobs.isEmpty());
            MainActivityGoalTest.Queue reads = new MainActivityGoalTest.Queue(); h.set("workspaceWorker", reads);
            h.set("chatID", ""); h.set("chat", null);
            h.controller.start().resume();
            assertEquals("Resume batches three independent workspace reads", 3, reads.jobs.size());
            while (!h.io.jobs.isEmpty()) run(h);
            event("ready", "{}"); assertTrue(h.<Boolean>get("workspaceReady"));
            assertEquals("Start/focus revoke superseded work and open only one new stream", 3, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            assertEquals("The foreground reads stay coalesced through focus callbacks", 3, reads.jobs.size());
        }
    }

    @Test public void failedRefreshControlClosesPresenceAndStartsAuthAndRetry() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream stream = start(h); presence(); stream.fail = true;
            h.activity.onWindowFocusChanged(true); run(h);
            assertTrue(stream.closed); assertFalse(h.<Boolean>get("workspaceReady"));
            assertEquals(0, h.<JSONArray>get("presence").length()); h.io.run();
            assertEquals("/api/me", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(550)); h.io.run();
            assertEquals(2, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
        }
    }

    @Test public void aPostedOldSendFailureCannotCloseTheNewSessionConnectionOrAcknowledgeItsDraft() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream old = start(h); presence(); open(h);
            EventListener english = MainActivityGoalTest.ShadowClient.workspaceListener;
            NativePeoplePanel panel = h.get("peoplePanel"); panel.editor.setText("Do not lose this"); h.call("sendPeople");
            NativePeopleModel inbox = h.get("inbox"); String request = inbox.sending.get("bob").id;
            old.fail = true; h.io.jobs.remove(0).run(); // Failure is posted but not yet applied.
            h.set("authEpoch", h.<Integer>get("authEpoch") + 1); h.call("render"); h.io.run(); event("ready", "{}");
            ShadowStream current = ShadowStream.streams.get(h.<EventStream>get("workspaceStream"));
            english.onEvent("direct_message", new JSONObject().put("id", "late-ack").put("request_id", request)
                    .put("from", "alice").put("to", "bob").put("text", "Do not lose this").toString());
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNotSame(old, current); assertTrue(old.closed); assertFalse(current.closed); assertTrue(h.<Boolean>get("workspaceReady"));
            assertNotNull(h.get("user")); assertEquals("Do not lose this", inbox.draft("bob")); assertTrue(inbox.messages.isEmpty());
            assertTrue("A revoked send failure cannot trigger an auth request or retry", h.io.jobs.isEmpty());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void productionResumeAgeIncludesDeepSleepEvenWhenHandlerTimersHaveNotRun() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ShadowStream.streams.clear(); MainActivityGoalTest.ShadowClient.workspaceFactory = EventStream::new;
            h.call("startWorkspace"); h.io.run(); event("ready", "{}"); presence();
            ShadowStream old = ShadowStream.streams.get(h.<EventStream>get("workspaceStream"));
            MainActivityGoalTest.Queue reads = new MainActivityGoalTest.Queue(); h.set("workspaceWorker", reads);
            long awake = SystemClock.uptimeMillis(), elapsed = SystemClock.elapsedRealtime();
            org.robolectric.shadows.ShadowSystemClock.simulateDeepSleep(Duration.ofMillis(45000));
            assertEquals("Handler uptime must not advance in this sleep fixture", awake, SystemClock.uptimeMillis());
            assertEquals(elapsed + 45000, SystemClock.elapsedRealtime());
            assertTrue("No Handler watchdog has fired yet", h.<Boolean>get("workspaceReady"));
            h.activity.onWindowFocusChanged(true);
            assertTrue(old.closed); assertFalse(h.<Boolean>get("workspaceReady"));
            assertEquals(0, h.<JSONArray>get("presence").length()); assertEquals(3, reads.jobs.size());
            h.io.run(); assertTrue("Stale sockets reconnect, rather than sending refresh", old.sent.isEmpty());
            assertEquals(2, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
        }
    }
}
