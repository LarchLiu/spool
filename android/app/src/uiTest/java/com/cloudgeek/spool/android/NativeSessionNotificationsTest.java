package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import java.io.File;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeSessionNotificationsTest {
    private JSONObject event(String chat, String kind) throws Exception { return new JSONObject().put("chat", chat).put("kind", kind).put("user", "alice"); }

    @Test public void latestEventsAreUnreadPerChatAndRejectAnotherAccountOrMalformedData() throws Exception {
        NativeSessionNotifications state = new NativeSessionNotifications();
        assertTrue(state.receive(event("chat-one", "completed"), "alice", ""));
        assertTrue(state.receive(event("chat-one", "failed"), "alice", ""));
        assertFalse(state.receive(event("chat-one", "failed"), "alice", ""));
        for (JSONObject invalid : new JSONObject[] {null, new JSONObject(), event("short", "completed"), event("../api/me", "failed"),
                event("chat-one", "test"), event("chat-one", "approval").put("user", "bob")}) assertFalse(state.receive(invalid, "alice", ""));
        assertEquals("failed", state.kind("chat-one"));
        assertTrue(state.receive(event("chat-two", "approval"), "alice", "chat-one"));
        assertTrue(state.receive(event("chat-one", "completed"), "alice", "chat-one"));
        assertEquals("", state.kind("chat-one")); assertEquals("approval", state.kind("chat-two"));
        NativeSessionNotifications cached = new NativeSessionNotifications(); cached.copyFrom(state); state.clear();
        assertEquals("approval", cached.kind("chat-two")); assertEquals("", state.kind("chat-two"));
    }

    @Test public void allSidebarCopiesUpdateInPlaceAtTheRightEdgeWithoutLosingLongPressOrSelection() throws Exception {
        for (int width : new int[] {320, 412}) for (boolean dark : new boolean[] {false, true}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, "en");
                NativeSidebarState choices = new NativeSidebarState(); choices.togglePin("chat-one"); choices.expanded.add("project-one");
                java.util.List<String> actions = new java.util.ArrayList<>();
                NativeSidebar sidebar = new NativeSidebar(ui, choices, id -> new JSONObject(), () -> {}, actions::add, id -> {}, () -> {}, () -> {}, () -> {});
                sidebar.chatOptions((value, anchor) -> actions.add("menu:" + value.optString("id")));
                controller.get().setContentView(sidebar);
                NativeSessionNotifications states = new NativeSessionNotifications(); sidebar.notifications(states);
                JSONArray projects = new JSONArray().put(new JSONObject().put("id", "project-one").put("name", "Example"));
                JSONArray chats = new JSONArray();
                for (String id : new String[] {"chat-one", "chat-two", "chat-three"}) chats.put(new JSONObject().put("id", id).put("project_id", "project-one")
                        .put("title", "A long conversation title that leaves room for its status — " + id).put("backend", "codex").put("status", "idle"));
                sidebar.bind(new JSONObject().put("id", "alice"), projects, chats, "project-one", "chat-two", "codex", false);
                NativeChatLink original = sidebar.chatRows.get(0);
                for (String kind : new String[] {"completed", "failed", "approval"}) {
                    states.receive(event("chat-one", kind), "alice", ""); sidebar.notifications(states);
                    assertSame("Status updates retain the existing long-press anchor", original, sidebar.chatRows.get(0));
                    sidebar.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
                    sidebar.layout(0, 0, width, 800);
                    int copies = 0;
                    for (NativeChatLink row : sidebar.chatRows) if (row.chatID.equals("chat-one")) {
                        copies++; assertNotNull(row.notification); assertEquals("session-notification:" + kind, row.notification.getTag());
                        assertSame(row.notification, row.open.getChildAt(row.open.getChildCount() - 1));
                        assertTrue(row.title.getRight() <= row.notification.getLeft()); assertTrue(row.notification.getRight() <= row.open.getWidth());
                        assertTrue(row.notification.getWidth() > 0); assertTrue(row.open.getContentDescription().toString().contains(ui.t(kind.equals("completed") ? "Completed" : kind.equals("failed") ? "Failed" : "Approval required")));
                    }
                    assertEquals("Pinned, project and recent rows share one event state", 3, copies);
                }
                original.open.performLongClick(); original.open.performClick();
                assertEquals(java.util.List.of("menu:chat-one", "chat-one"), actions);
                states.dismiss("chat-one"); sidebar.notifications(states);
                for (NativeChatLink row : sidebar.chatRows) assertNull(row.notification);
                states.receive(event("chat-one", "completed"), "alice", ""); states.receive(event("chat-two", "failed"), "alice", ""); states.receive(event("chat-three", "approval"), "alice", ""); sidebar.notifications(states);
                sidebar.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY)); sidebar.layout(0, 0, width, 800);
                Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888); sidebar.draw(new Canvas(image));
                File artifact = new File(System.getProperty("spool.ui.artifacts"), "session-notifications-" + width + (dark ? "-dark" : "-light") + ".png"); artifact.getParentFile().mkdirs();
                try (java.io.FileOutputStream output = new java.io.FileOutputStream(artifact)) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); } finally { image.recycle(); }
            }
        }
    }
}
