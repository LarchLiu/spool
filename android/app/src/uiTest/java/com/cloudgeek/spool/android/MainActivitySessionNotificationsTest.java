package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class,
        MainActivityWorkspaceEventsTest.ShadowStream.class, MainActivityWorkspaceEventsTest.ShadowSpoolnet.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivitySessionNotificationsTest {
    private void event(String chat, String kind, String user) throws Exception {
        MainActivityWorkspaceEventsTest.event("session_notification", new JSONObject().put("chat", chat).put("kind", kind).put("user", user).toString());
    }
    private void drain(MainActivityGoalTest.Harness h) {
        while (!h.io.jobs.isEmpty()) { h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    }
    private void start(MainActivityGoalTest.Harness h) throws Exception {
        h.set("chats", new JSONArray().put(new JSONObject().put("id", "chat-one").put("title", "Current").put("project_id", "project-one").put("backend", "codex"))
                .put(new JSONObject().put("id", "chat-two").put("title", "Other").put("project_id", "project-one").put("backend", "codex")));
        MainActivityWorkspaceEventsTest.install(h); h.call("startWorkspace"); h.io.run(); MainActivityWorkspaceEventsTest.event("ready", "true");
        h.controller.windowFocusChanged(true); drain(h);
        assertTrue(h.activity.hasWindowFocus()); h.call("openSidebar");
    }

    @Test public void realWorkspaceEventsUpdateOtherSessionsAndOpeningTheChatClearsEveryCopy() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h);
            NativeSessionNotifications states = h.get("sessionNotifications");
            for (String kind : new String[] {"completed", "failed", "approval"}) {
                event("chat-one", kind, "alice"); event("chat-two", kind, "alice");
                assertEquals("", states.kind("chat-one")); assertEquals(kind, states.kind("chat-two"));
                NativeSidebar sidebar = h.get("sidebar");
                assertTrue(sidebar.chatRows.stream().filter(row -> row.chatID.equals("chat-two")).allMatch(row -> row.notification != null));
            }
            event("chat-two", "failed", "bob"); assertEquals("approval", states.kind("chat-two"));
            NativeSidebar sidebar = h.get("sidebar");
            sidebar.chatRows.stream().filter(row -> row.chatID.equals("chat-two")).findFirst().get().open.performClick();
            assertEquals("chat-two", h.<String>get("chatID")); assertEquals("", states.kind("chat-two"));
            h.call("openSidebar"); for (NativeChatLink row : h.<NativeSidebar>get("sidebar").chatRows) assertNull(row.notification);
            h.call("clearWorkspace"); assertEquals("", states.kind("chat-one")); assertEquals("", states.kind("chat-two"));
        }
    }

    @Test public void backgroundCurrentSessionRetainsItsStatusUntilReturningToViewIt() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            start(h); h.call("closeDrawer");
            h.controller.pause().stop(); drain(h);
            event("chat-one", "completed", "alice"); event("chat-two", "failed", "alice");
            NativeSessionNotifications states = h.get("sessionNotifications");
            assertEquals("completed", states.kind("chat-one")); assertEquals("failed", states.kind("chat-two"));
            h.controller.start().resume().windowFocusChanged(true);
            assertEquals("", states.kind("chat-one")); assertEquals("failed", states.kind("chat-two"));
            h.call("clearWorkspace"); assertEquals("", states.kind("chat-two"));
            event("chat-two", "approval", "alice"); assertEquals("", states.kind("chat-two"));
        }
    }
}
