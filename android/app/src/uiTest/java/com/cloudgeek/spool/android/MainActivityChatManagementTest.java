package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.view.View;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowToast;

/** Actual title/menu/dialog/API flows; only the gomobile network boundary is replaced. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityChatManagementTest {
    private String envelope(int status, JSONObject value) throws Exception {
        return new JSONObject().put("status", status).put("body", value.toString()).toString();
    }

    private void seed(MainActivityGoalTest.Harness h, String backend) throws Exception {
        JSONObject chat = h.get("chat");
        chat.put("title", "Original title").put("project_id", "project-one").put("backend", backend).put("updated_at", 10);
        h.set("chats", new JSONArray().put(NativeChatState.copy(chat)));
        ((NativeNavigation) h.get("navigation")).select("project-one", "chat-one", h.get("projects"), h.get("chats"));
        h.call("render");
    }

    private NativeChatMenu open(MainActivityGoalTest.Harness h, int option) {
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        NativeUi.Header header = h.get("workspaceHeader");
        assertEquals(View.VISIBLE, header.titleChevron.getVisibility());
        assertTrue(header.titleAction.isEnabled());
        assertTrue(header.titleAction.performClick());
        NativeChatMenu menu = h.get("chatMenu");
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertTrue(menu.popup.isShowing());
        assertEquals(5, menu.popup.content.getChildCount());
        NativeUi ui = h.get("ui");
        assertEquals(ui.t("Rename"), menu.popup.content.getChildAt(0).getContentDescription());
        assertEquals(ui.t(h.<NativeSidebarState>get("sidebarState").pinned.contains(h.<String>get("chatID")) ? "Unpin chat" : "Pin chat"), menu.popup.content.getChildAt(1).getContentDescription());
        assertFalse(menu.popup.content.getChildAt(2).isFocusable());
        assertFalse(menu.popup.content.getChildAt(2).isClickable());
        assertEquals(ui.dp(1), menu.popup.content.getChildAt(2).getHeight());
        assertEquals(ui.t("Archive"), menu.popup.content.getChildAt(3).getContentDescription());
        assertEquals(ui.t("Delete"), menu.popup.content.getChildAt(4).getContentDescription());
        if (option >= 0) assertTrue(menu.popup.content.getChildAt(option < 2 ? option : option + 1).performClick());
        return menu;
    }

    private void snapshot(View view, String name) throws Exception {
        java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"), "chat-management");
        assertTrue(directory.isDirectory() || directory.mkdirs());
        android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(view.getWidth(), view.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
        view.draw(new android.graphics.Canvas(image));
        try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, name + ".png"))) {
            assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
        } finally { image.recycle(); }
    }

    private void snapshotOpenMenu(MainActivityGoalTest.Harness h, NativeChatMenu menu, String name) throws Exception {
        View page = h.activity.getWindow().getDecorView();
        NativePopupFrame popup = org.robolectric.util.ReflectionHelpers.getField(menu.popup, "frame");
        int[] origin = new int[2]; page.getLocationOnScreen(origin);
        android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(page.getWidth(), page.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
        android.graphics.Canvas canvas = new android.graphics.Canvas(image);
        page.draw(canvas);
        canvas.save();
        canvas.translate(popup.window.left - origin[0], popup.window.top - origin[1]);
        popup.draw(canvas);
        canvas.restore();
        java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"), "chat-management");
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, name + ".png"))) {
            assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
        } finally { image.recycle(); }
    }

    private JSONObject navigation(MainActivityGoalTest.Harness h, JSONArray chats) throws Exception {
        return new JSONObject().put("projects", h.<JSONArray>get("projects")).put("chats", chats);
    }

    @Test public void renameHasAnEditableTitleValidatesAndUpdatesHeaderAndNavigationForBothAgents() throws Exception {
        for (String backend : new String[] {"codex", "claude"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            seed(h, backend);
            NativeChatMenu menu = open(h, 0);
            assertTrue(menu.dialog.isShowing());
            assertTrue(menu.dialog.name.hasFocus());
            assertEquals("Original title", menu.dialog.name.getText().toString());
            menu.dialog.name.setText("  "); menu.dialog.submit.performClick();
            assertNotNull(menu.dialog.name.getError()); assertTrue(h.io.jobs.isEmpty());
            menu.dialog.name.setText("x".repeat(201)); menu.dialog.submit.performClick();
            assertTrue(h.io.jobs.isEmpty());
            menu.dialog.name.setText("  New   title 😀  "); menu.dialog.submit.performClick(); menu.dialog.submit.performClick();
            assertEquals(1, h.io.jobs.size());
            assertFalse(menu.dialog.name.isEnabled()); assertFalse(menu.dialog.cancel.isEnabled());
            MainActivityGoalTest.ShadowClient.response = envelope(200, NativeChatState.copy(h.get("chat")).put("title", "New title 😀"));
            h.io.run();
            List<String> request = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals(List.of("POST", "/api/chats/chat-one/rename"), request.subList(0, 2));
            assertEquals("New title 😀", new JSONObject(request.get(2)).getString("title"));
            assertEquals("test-csrf", request.get(3)); assertEquals("en", request.get(4));
            assertEquals("New title 😀", h.<JSONObject>get("chat").getString("title"));
            assertEquals("New title 😀", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
            assertEquals("New title 😀", h.<NativeUi.Header>get("workspaceHeader").title.getText().toString());
            assertFalse(menu.dialog.isShowing());
            h.call("renderChat");
            assertEquals("Empty history must retain the renamed title", "New title 😀", h.<NativeUi.Header>get("workspaceHeader").title.getText().toString());
        }
    }

    @Test public void renameErrorsKeepInputForRetryAndCancelNeverSends() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            seed(h, "claude");
            NativeChatMenu menu = open(h, 0);
            menu.dialog.cancel.performClick(); assertTrue(h.io.jobs.isEmpty());
            menu = open(h, 0); menu.dialog.name.setText("😀".repeat(200)); menu.dialog.submit.performClick();
            MainActivityGoalTest.ShadowClient.response = envelope(409, new JSONObject().put("error", "Send a message before renaming this chat"));
            h.io.run();
            assertEquals(200, new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2)).getString("title").codePointCount(0, 400));
            assertTrue(menu.dialog.isShowing()); assertTrue(menu.dialog.submit.isEnabled());
            assertEquals(View.VISIBLE, menu.dialog.error.getVisibility());
            assertEquals("Send a message before renaming this chat", menu.dialog.error.getText().toString());
            assertEquals("Original title", h.<JSONObject>get("chat").getString("title"));
        }
    }

    @Test public void archiveFailureKeepsChatAndSuccessUsesReturnedMembershipForBothAgents() throws Exception {
        for (String backend : new String[] {"codex", "claude"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            seed(h, backend);
            MainActivityGoalTest.ShadowClient.response = envelope(409, new JSONObject().put("error", "Wait for the running response before archiving this chat"));
            open(h, 2); assertEquals("chat-one", h.<String>get("chatID"));
            assertFalse(h.<NativeUi.Header>get("workspaceHeader").titleAction.isEnabled());
            h.io.run();
            assertEquals("chat-one", h.<String>get("chatID")); assertEquals(1, h.<JSONArray>get("chats").length());
            assertEquals("Wait for the running response before archiving this chat", ShadowToast.getTextOfLatestToast());
            // A returned item must remain visible; the client never filters it optimistically.
            MainActivityGoalTest.ShadowClient.response = envelope(200, navigation(h, h.get("chats")));
            open(h, 2); h.io.run();
            assertEquals("chat-one", h.<String>get("chatID"));
            MainActivityGoalTest.ShadowClient.response = envelope(200, navigation(h, new JSONArray()));
            open(h, 2); h.io.run();
            assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat"));
            assertEquals(0, h.<JSONArray>get("chats").length());
            assertEquals("/api/chats/chat-one/archive", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
        }
    }

    @Test public void deletionRequiresConfirmationAndOnlySuccessfulRefreshClearsSelection() throws Exception {
        for (String backend : new String[] {"codex", "claude"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            seed(h, backend);
            NativeChatMenu menu = open(h, 3);
            assertTrue(h.io.jobs.isEmpty()); assertTrue(menu.dialog.cancel.hasFocus());
            menu.dialog.cancel.performClick(); assertTrue(h.io.jobs.isEmpty());
            menu = open(h, 3); menu.dialog.submit.performClick(); menu.dialog.submit.performClick();
            assertFalse(menu.dialog.cancel.isEnabled()); assertEquals(1, h.io.jobs.size());
            MainActivityGoalTest.ShadowClient.response = envelope(502, new JSONObject().put("error", "Could not delete chat"));
            h.io.run();
            assertTrue(menu.dialog.isShowing()); assertEquals("Could not delete chat", menu.dialog.error.getText().toString());
            assertEquals("chat-one", h.<String>get("chatID")); assertEquals(1, h.<JSONArray>get("chats").length());
            MainActivityGoalTest.ShadowClient.response = envelope(200, navigation(h, new JSONArray()));
            menu.dialog.submit.performClick(); h.io.run();
            assertFalse(menu.dialog.isShowing()); assertEquals("", h.<String>get("chatID"));
            assertEquals(0, h.<JSONArray>get("chats").length());
            assertEquals("/api/chats/chat-one/delete", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
        }
    }

    @Test public void titleMenuPinsReadOnlyChatsAndStaysUnavailableForUnsavedOrLoadingChats() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            assertEquals(View.GONE, h.<NativeUi.Header>get("workspaceHeader").titleChevron.getVisibility());
            assertFalse(h.<NativeUi.Header>get("workspaceHeader").titleAction.isClickable());
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            seed(h, "codex"); h.<JSONObject>get("chat").put("can_control", false); h.call("updateComposer");
            assertEquals(View.VISIBLE, h.<NativeUi.Header>get("workspaceHeader").titleChevron.getVisibility());
            h.<NativeUi.Header>get("workspaceHeader").titleAction.performClick();
            NativeChatMenu menu = h.get("chatMenu");
            assertEquals(1, menu.popup.content.getChildCount());
            assertTrue(menu.popup.content.getChildAt(0).performClick());
            assertTrue(h.<NativeSidebarState>get("sidebarState").pinned.contains("chat-one"));
            h.set("chat", null); h.call("updateComposer");
            assertEquals(View.GONE, h.<NativeUi.Header>get("workspaceHeader").titleChevron.getVisibility());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void titleMenuPinAndUnpinUpdateTheSidebarAndRestoreAfterRecreation() throws Exception {
        for (String backend : new String[] {"codex", "claude"}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                seed(h, backend);
                NativeSidebarState state = h.get("sidebarState");
                state.toggleSection("pinned");
                NativeChatMenu menu = open(h, -1);
                View pin = menu.popup.content.getChildAt(1), archive = menu.popup.content.getChildAt(3);
                pin.requestFocus();
                pin.dispatchKeyEvent(new android.view.KeyEvent(android.view.KeyEvent.ACTION_DOWN, android.view.KeyEvent.KEYCODE_DPAD_DOWN));
                assertTrue("Keyboard navigation skips the separator", archive.hasFocus());
                archive.dispatchKeyEvent(new android.view.KeyEvent(android.view.KeyEvent.ACTION_DOWN, android.view.KeyEvent.KEYCODE_DPAD_UP));
                assertTrue(pin.hasFocus());
                pin.performClick();
                assertFalse(menu.popup.isShowing()); assertTrue(state.pinned.contains("chat-one"));
                assertFalse(state.collapsed.contains("pinned")); assertTrue(h.io.jobs.isEmpty());
                h.call("openSidebar");
                NativeSidebar sidebar = h.get("sidebar");
                assertNotNull(sidebar.findViewWithTag("pinned:chat-one")); assertNotNull(sidebar.findViewWithTag("recent:chat-one"));
                h.call("closeDrawer");
            }
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                seed(h, backend);
                assertTrue(h.<NativeSidebarState>get("sidebarState").pinned.contains("chat-one"));
                NativeChatMenu menu = open(h, 1);
                assertFalse(menu.popup.isShowing()); assertFalse(h.<NativeSidebarState>get("sidebarState").pinned.contains("chat-one"));
                h.call("openSidebar");
                assertNull(h.<NativeSidebar>get("sidebar").findViewWithTag("pinned:chat-one"));
                assertNotNull(h.<NativeSidebar>get("sidebar").findViewWithTag("recent:chat-one"));
                assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            }
        }
    }

    @Test public void signingOutOrSwitchingServersRevokesQueuedRequestsAndPostedReplies() throws Exception {
        for (boolean alreadySent : new boolean[] {false, true}) for (boolean server : new boolean[] {false, true})
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                seed(h, "claude");
                NativeChatMenu menu = open(h, 3); menu.dialog.submit.performClick();
                MainActivityGoalTest.ShadowClient.response = envelope(200, navigation(h, new JSONArray()));
                Runnable request = h.io.jobs.remove(0);
                if (alreadySent) request.run();
                if (server) { h.set("generation", h.<Integer>get("generation") + 1); h.call("clearWorkspace"); }
                else h.call("clearWorkspace");
                h.set("chatID", "new-account-chat"); h.set("chats", new JSONArray().put(h.chat("new-account-chat")));
                if (!alreadySent) request.run();
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertEquals(alreadySent ? 1 : 0, MainActivityGoalTest.ShadowClient.calls.size());
                assertEquals("new-account-chat", h.<String>get("chatID"));
                assertEquals(1, h.<JSONArray>get("chats").length());
                assertFalse(menu.dialog.isShowing());
            }
    }

    @Test @Config(sdk = 35) public void narrowConfirmationButtonsRemainVisibleInEveryLanguageAndTheme() throws Exception {
        for (String language : new String[] {"zh-CN", "en", "ja"}) for (boolean dark : new boolean[] {false, true})
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
                org.robolectric.RuntimeEnvironment.setQualifiers("w320dp-h640dp-mdpi");
                h.set("language", language); h.set("theme", dark ? "dark" : "light");
                seed(h, "claude");
                h.<JSONObject>get("chat").put("title", "这是一段很长的会话标题，用来验证删除确认弹窗在窄屏下的换行和按钮布局 😀");
                h.call("renderChat");
                NativeChatMenu menu = open(h, -1);
                NativeUi.Header header = h.get("workspaceHeader");
                assertTrue("Title remains visible beside its arrow", header.title.getWidth() > 40);
                assertEquals(13, header.titleChevron.getWidth());
                assertTrue(header.titleChevron.getLeft() >= header.title.getRight());
                assertTrue(header.titleChevron.getRight() <= header.titleAction.getWidth());
                View last = menu.popup.content.getChildAt(4);
                assertTrue(last.getHeight() >= 48);
                assertTrue("All four menu actions fit without clipping", last.getBottom() <= ((View) menu.popup.content.getParent()).getHeight());
                if (language.equals("zh-CN")) {
                    snapshot(header, "header-320" + (dark ? "-dark" : "-light"));
                    snapshot(menu.popup.content, "menu-320" + (dark ? "-dark" : "-light"));
                    snapshotOpenMenu(h, menu, "dropdown-320" + (dark ? "-dark" : "-light"));
                }
                menu.popup.content.getChildAt(4).performClick();
                View modal = menu.dialog.modal;
                modal.measure(View.MeasureSpec.makeMeasureSpec(320, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(600, View.MeasureSpec.AT_MOST));
                modal.layout(0, 0, modal.getMeasuredWidth(), modal.getMeasuredHeight());
                for (android.widget.Button button : new android.widget.Button[] {menu.dialog.cancel, menu.dialog.submit}) {
                    assertTrue(button.isShown()); assertTrue(button.getTextSize() >= 12);
                    assertTrue(button.getWidth() >= 80); assertTrue(button.getHeight() >= 48);
                    assertNotNull(button.getLayout());
                    for (int line = 0; line < button.getLayout().getLineCount(); line++) assertEquals(0, button.getLayout().getEllipsisCount(line));
                    android.graphics.Rect bounds = new android.graphics.Rect(); button.getDrawingRect(bounds);
                    menu.dialog.modal.offsetDescendantRectToMyCoords(button, bounds);
                    assertTrue(bounds.left >= 0 && bounds.right <= modal.getWidth());
                    assertTrue(bounds.top >= 0 && bounds.bottom <= modal.getHeight());
                }
                snapshot(menu.dialog.modal.surface, "delete-320-" + language + (dark ? "-dark" : "-light"));
            }
    }

    @Test public void lateResponsesUpdateOnlyTheTargetAndPreserveAnotherChatsDraft() throws Exception {
        for (int option : new int[] {0, 2, 3}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            seed(h, "codex");
            JSONObject other = h.chat("chat-two").put("title", "Other title").put("project_id", "project-one");
            h.<JSONArray>get("chats").put(other);
            NativeChatMenu menu = open(h, option);
            if (option != 2) {
                if (option == 0) menu.dialog.name.setText("Renamed target");
                menu.dialog.submit.performClick();
            }
            JSONObject response = option == 0 ? NativeChatState.copy(h.get("chat")).put("title", "Renamed target")
                    : navigation(h, new JSONArray().put(other));
            h.set("chatID", "chat-two"); h.set("chat", other); h.call("render");
            h.composer().input.setText("Keep this draft");
            MainActivityGoalTest.ShadowClient.response = envelope(200, response); h.io.run();
            assertEquals("chat-two", h.<String>get("chatID"));
            assertEquals("Other title", h.<NativeUi.Header>get("workspaceHeader").title.getText().toString());
            assertEquals("Keep this draft", h.composer().input.getText().toString());
            if (option == 0) assertEquals("Renamed target", h.<JSONArray>get("chats").getJSONObject(0).getString("title"));
        }
    }

    @Test public void deletionOfANewChatAbsentFromPreviousNavigationStillClearsItsSelection() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.ShadowClient.response = envelope(200, navigation(h, new JSONArray()));
            NativeChatMenu menu = open(h, 3); menu.dialog.submit.performClick(); h.io.run();
            assertEquals("", h.<String>get("chatID")); assertNull(h.get("chat"));
        }
    }
}
