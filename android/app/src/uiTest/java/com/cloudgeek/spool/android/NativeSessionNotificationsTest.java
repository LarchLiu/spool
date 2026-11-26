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
    private JSONObject chat(String id, String project, String status) throws Exception {
        return new JSONObject().put("id", id).put("project_id", project).put("title", id).put("backend", "codex").put("status", status);
    }
    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, 800);
    }

    @Test public void collapsedProjectsShowUniqueStatusesFromAllTheirChatsAndExpandedProjectsHideThem() throws Exception {
        for (int width : new int[] {320, 412}) for (boolean dark : new boolean[] {false, true}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, dark ? "zh-CN" : "en");
                NativeSidebarState choices = new NativeSidebarState();
                java.util.List<String> actions = new java.util.ArrayList<>();
                NativeSidebar sidebar = new NativeSidebar(ui, choices, id -> new JSONObject(), () -> {}, actions::add, id -> actions.add("new:" + id), () -> {}, () -> {}, () -> {});
                controller.get().setContentView(sidebar);
                NativeSessionNotifications states = new NativeSessionNotifications(); sidebar.notifications(states);
                String name = "A long project name that must leave room for every conversation status";
                JSONArray projects = new JSONArray().put(new JSONObject().put("id", "project-one").put("name", name))
                        .put(new JSONObject().put("id", "quiet").put("name", "Quiet project"));
                JSONArray chats = new JSONArray();
                for (int i = 0; i < 12; i++) chats.put(chat("chat-" + (100 + i), "project-one", i == 5 || i == 6 ? "running" : "idle"));
                chats.put(chat("chat-quiet", "quiet", "completed")).put(chat("chat-other", "elsewhere", "running"));
                states.receive(event("chat-other", "failed"), "alice", "");
                sidebar.bind(new JSONObject().put("id", "alice"), projects, chats, "project-one", "chat-100", "codex", false);
                assertEquals(View.GONE, sidebar.projectRows.get(0).status.getVisibility());
                sidebar.projectRows.get(0).toggle.performClick();
                NativeProjectRow row = sidebar.projectRows.get(0);
                assertNotNull("Loading includes conversations beyond the first five", row.status.findViewWithTag("project-status:running"));
                for (int i = 7; i < 12; i++) {
                    states.receive(event("chat-" + (100 + i), i < 9 ? "completed" : i == 9 ? "failed" : "approval"), "alice", "");
                    sidebar.notifications(states);
                    assertSame("Unread updates keep project controls and their focus anchors", row, sidebar.projectRows.get(0));
                }
                assertEquals(4, row.status.getChildCount());
                assertEquals("project-status:running", row.status.getChildAt(0).getTag());
                assertEquals("session-notification:approval", row.status.getChildAt(1).getTag());
                assertEquals("session-notification:failed", row.status.getChildAt(2).getTag());
                assertEquals("session-notification:completed", row.status.getChildAt(3).getTag());
                for (String label : new String[] {"Working", "Approval required", "Failed", "Completed"})
                    assertTrue(row.toggle.getContentDescription().toString().contains(ui.t(label)));
                assertEquals("A quiet project excludes other projects' running and unread chats", View.GONE, sidebar.projectRows.get(1).status.getVisibility());
                View spinner = row.status.getChildAt(0); sidebar.notifications(states);
                assertSame("Unchanged summaries retain the running animation", spinner, row.status.getChildAt(0));
                layout(sidebar, width);
                assertEquals(ui.dp(31), row.getHeight()); assertTrue(row.title.getWidth() > 0);
                assertTrue(row.toggle.getRight() <= row.status.getLeft()); assertTrue(row.status.getRight() <= row.create.getLeft());
                for (int i = 0; i < row.status.getChildCount(); i++) {
                    View icon = row.status.getChildAt(i);
                    assertTrue(icon.getWidth() > 0); assertTrue(icon.getLeft() >= 0); assertTrue(icon.getRight() <= row.status.getWidth());
                }
                Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888); sidebar.draw(new Canvas(image));
                File artifact = new File(System.getProperty("spool.ui.artifacts"), "project-statuses-" + width + (dark ? "-dark" : "-light") + ".png"); artifact.getParentFile().mkdirs();
                try (java.io.FileOutputStream output = new java.io.FileOutputStream(artifact)) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); } finally { image.recycle(); }
                row.create.performClick(); assertEquals(java.util.List.of("new:project-one"), actions);
                row.toggle.performClick();
                assertEquals(View.GONE, sidebar.projectRows.get(0).status.getVisibility());
                assertEquals(0, sidebar.projectRows.get(0).status.getChildCount());
                assertEquals(name, sidebar.projectRows.get(0).toggle.getContentDescription());
                sidebar.findViewWithTag("more:project-one").performClick();
                assertTrue(sidebar.chatRows.stream().anyMatch(link -> link.chatID.equals("chat-110") && link.notification != null));
                sidebar.projectRows.get(0).toggle.performClick();
                assertEquals(4, sidebar.projectRows.get(0).status.getChildCount());
                sidebar.deactivate();
            }
        }
    }

    @Test public void collapsedProjectStatusesFollowReadStateNavigationAndAccountChanges() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en"); NativeSidebarState choices = new NativeSidebarState();
            NativeSidebar sidebar = new NativeSidebar(ui, choices, id -> new JSONObject(), () -> {}, id -> {}, id -> {}, () -> {}, () -> {}, () -> {});
            controller.get().setContentView(sidebar);
            NativeSessionNotifications states = new NativeSessionNotifications(); sidebar.notifications(states);
            JSONObject user = new JSONObject().put("id", "alice");
            JSONArray projects = new JSONArray().put(new JSONObject().put("id", "project-one").put("name", "Project"));
            JSONArray chats = new JSONArray().put(chat("chat-one", "project-one", "running")).put(chat("chat-two", "project-one", "idle"));
            sidebar.bind(user, projects, chats, "project-one", "chat-one", "codex", false);
            sidebar.projectRows.get(0).toggle.performClick();
            for (String id : new String[] {"chat-one", "chat-two"}) states.receive(event(id, "completed"), "alice", "");
            sidebar.notifications(states);
            states.dismiss("chat-one"); sidebar.notifications(states);
            assertNotNull(sidebar.projectRows.get(0).status.findViewWithTag("session-notification:completed"));
            states.dismiss("chat-two"); sidebar.notifications(states);
            assertNull(sidebar.projectRows.get(0).status.findViewWithTag("session-notification:completed"));
            assertNotNull(sidebar.projectRows.get(0).status.findViewWithTag("project-status:running"));
            chats.getJSONObject(0).put("status", "completed");
            sidebar.bind(user, projects, chats, "project-one", "chat-one", "codex", false);
            assertFalse(choices.expanded.contains("project-one")); assertEquals(View.GONE, sidebar.projectRows.get(0).status.getVisibility());
            states.receive(event("chat-two", "failed"), "alice", ""); sidebar.notifications(states);
            assertNotNull(sidebar.projectRows.get(0).status.findViewWithTag("session-notification:failed"));
            sidebar.bind(user, projects, new JSONArray().put(chats.getJSONObject(0)), "project-one", "chat-one", "codex", false);
            assertEquals("Removed conversations no longer contribute unread icons", View.GONE, sidebar.projectRows.get(0).status.getVisibility());
            states.receive(event("chat-one", "approval"), "alice", ""); sidebar.notifications(states);
            assertEquals(View.VISIBLE, sidebar.projectRows.get(0).status.getVisibility());
            sidebar.notifications(new NativeSessionNotifications());
            assertEquals(View.GONE, sidebar.projectRows.get(0).status.getVisibility());
            assertEquals("Project", sidebar.projectRows.get(0).toggle.getContentDescription());
            sidebar.deactivate();
        }
    }

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
