package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.MotionEvent;
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
    private void hover(View view, int action) {
        MotionEvent event = MotionEvent.obtain(0, 0, action, 12, 12, 0);
        event.setSource(android.view.InputDevice.SOURCE_MOUSE);
        try { view.dispatchGenericMotionEvent(event); } finally { event.recycle(); }
    }

    @Test public void idleChatAgesRefreshInEverySidebarCopyAndYieldToNotificationsAndHover() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            NativeSidebarState choices = new NativeSidebarState(); choices.togglePin("chat-one");
            NativeSidebar sidebar = new NativeSidebar(ui, choices, id -> new JSONObject(), () -> {}, id -> {}, id -> {}, () -> {}, () -> {}, () -> {});
            controller.get().setContentView(sidebar);
            NativeSessionNotifications states = new NativeSessionNotifications(); sidebar.notifications(states);
            long now = System.currentTimeMillis();
            JSONArray projects = new JSONArray().put(new JSONObject().put("id", "project-one").put("name", "Project"));
            JSONObject first = chat("chat-one", "project-one", "idle").put("updated_at", now);
            JSONArray chats = new JSONArray().put(first).put(chat("chat-two", "project-one", "idle").put("updated_at", now - 5_184_000_000L));
            sidebar.bind(new JSONObject().put("id", "alice"), projects, chats, "project-one", "chat-one", "codex", false);
            layout(sidebar, 360);
            NativeChatLink original = sidebar.chatRows.get(0);
            int copies = 0;
            for (NativeChatLink row : sidebar.chatRows) {
                assertEquals(row.chatID.equals("chat-one") ? "now" : "2mo", row.age.getText().toString());
                assertEquals(View.VISIBLE, row.age.getVisibility());
                assertTrue(row.title.getRight() <= row.age.getLeft());
                assertEquals(row.open.getWidth(), row.age.getRight());
                assertTrue(row.open.getContentDescription().toString().contains(row.age.getTooltipText()));
                if (row.chatID.equals("chat-one")) copies++;
            }
            assertEquals(3, copies);
            NativeChatLink running = new NativeChatLink(ui, new JSONObject(first.toString()).put("status", "running"), false, false, "", id -> new JSONObject(), () -> {});
            assertEquals("Running status takes priority over age", View.GONE, running.age.getVisibility());
            hover(original, MotionEvent.ACTION_HOVER_ENTER); assertEquals(View.GONE, original.age.getVisibility());
            hover(original, MotionEvent.ACTION_HOVER_EXIT); assertEquals(View.VISIBLE, original.age.getVisibility());
            states.receive(event("chat-one", "completed"), "alice", ""); sidebar.notifications(states);
            for (NativeChatLink row : sidebar.chatRows) if (row.chatID.equals("chat-one")) {
                assertEquals(View.GONE, row.age.getVisibility()); assertNotNull(row.notification);
            }
            states.dismiss("chat-one"); sidebar.notifications(states);
            for (NativeChatLink row : sidebar.chatRows) if (row.chatID.equals("chat-one")) assertEquals(View.VISIBLE, row.age.getVisibility());
            for (NativeChatLink row : sidebar.chatRows) row.refreshTime(now + 60_000);
            for (NativeChatLink row : sidebar.chatRows) if (row.chatID.equals("chat-one")) assertEquals("1m", row.age.getText().toString());
            // Robolectric advances Handler time separately from the JVM wall clock.
            // The scheduled refresh must replace the supplied time with the current wall clock.
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMinutes(1));
            assertSame("Minute updates retain row and long-press anchors", original, sidebar.chatRows.get(0));
            for (NativeChatLink row : sidebar.chatRows) if (row.chatID.equals("chat-one")) assertEquals("now", row.age.getText().toString());
            sidebar.setVisibility(View.GONE);
            original.refreshTime(now + 120_000);
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMinutes(1));
            assertEquals("Hidden sidebars pause their timer", "2m", original.age.getText().toString());
            sidebar.setVisibility(View.VISIBLE); assertEquals("now", original.age.getText().toString());
            sidebar.deactivate();
            original.refreshTime(now + 180_000);
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMinutes(1));
            assertEquals("Disposed sidebars cancel their timer", "3m", original.age.getText().toString());
        }
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
                assertEquals("The new-chat action always keeps the right edge", row.getWidth() - ui.dp(4), row.create.getRight());
                for (int i = 0; i < row.status.getChildCount(); i++) {
                    View icon = row.status.getChildAt(i);
                    assertTrue(icon.getWidth() > 0); assertTrue(icon.getLeft() >= 0); assertTrue(icon.getRight() <= row.status.getWidth());
                }
                Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888); sidebar.draw(new Canvas(image));
                File artifact = new File(System.getProperty("spool.ui.artifacts"), "project-statuses-" + width + (dark ? "-dark" : "-light") + ".png"); artifact.getParentFile().mkdirs();
                try (java.io.FileOutputStream output = new java.io.FileOutputStream(artifact)) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); } finally { image.recycle(); }
                int titleWidth = row.title.getWidth(), actionLeft = row.create.getLeft();
                android.animation.ValueAnimator spin = org.robolectric.util.ReflectionHelpers.getField(spinner, "spin");
                assertNotNull(spin); spin.setCurrentPlayTime(375);
                hover(row, MotionEvent.ACTION_HOVER_ENTER);
                assertEquals("Pointer hover hides status without stopping animation", 0f, row.status.getAlpha(), 0f);
                assertEquals(View.VISIBLE, row.status.getVisibility());
                assertEquals(View.VISIBLE, row.create.getVisibility());
                layout(sidebar, width);
                assertEquals(titleWidth, row.title.getWidth()); assertEquals(actionLeft, row.create.getLeft());
                hover(row, MotionEvent.ACTION_HOVER_EXIT);
                assertEquals("Touch users and rows without hover show status", 1f, row.status.getAlpha(), 0f);
                assertSame("Hover retains the running animator", spin, org.robolectric.util.ReflectionHelpers.getField(spinner, "spin"));
                assertTrue("The animation continues from its previous progress", spin.isRunning() && spin.getCurrentPlayTime() >= 375);
                float x = (row.create.getLeft() + row.create.getRight()) / 2f, y = row.getHeight() / 2f;
                for (int action : new int[] {MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP}) {
                    MotionEvent tap = MotionEvent.obtain(0, 0, action, x, y, 0);
                    try { assertTrue("Status must not intercept the new-chat touch target", row.dispatchTouchEvent(tap)); } finally { tap.recycle(); }
                }
                org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
                assertEquals(java.util.List.of("new:project-one"), actions);
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
