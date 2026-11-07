package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.os.SystemClock;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.time.Duration;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.Client;
import spoolnet.EventListener;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class,
        MainActivityWorkspaceEventsTest.ShadowStream.class, MainActivityWorkspaceEventsTest.ShadowSpoolnet.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityAppearanceTest {
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private void change(MainActivityGoalTest.Harness h, String theme, String language) {
        ReflectionHelpers.callInstanceMethod(h.activity, "updatePreferences",
                ReflectionHelpers.ClassParameter.from(String.class, theme), ReflectionHelpers.ClassParameter.from(String.class, language));
    }
    private String text(View view) {
        String value = view instanceof TextView ? ((TextView) view).getText().toString() : "";
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) value += "\n" + text(((ViewGroup) view).getChildAt(i));
        return value;
    }

    @Test public void rapidChoicesUpdateTheDialogAndWorkspaceImmediatelyWithoutReconnecting() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityWorkspaceEventsTest.install(h); h.call("startWorkspace"); h.io.run();
            MainActivityWorkspaceEventsTest.event("ready", "{}"); h.call("startChat");
            Client client = h.get("client"); Object stream = h.get("workspaceStream");
            EventListener workspaceListener = MainActivityGoalTest.ShadowClient.workspaceListener, chatListener = MainActivityGoalTest.ShadowClient.chatListener;
            View root = h.get("root"), history = h.get("history"), editor = h.composer();
            h.composer().input.setText("Keep this draft while changing appearance");
            MainActivityGoalTest.ShadowClient.responder = call -> {
                assertEquals("Appearance must not request or save server state", "/api/desktop", call.get(1));
                return "{status:200,body:'{}'}";
            };
            h.call("openAppearance"); idle();
            MainActivityGoalTest.ShadowClient.calls.clear();
            NativeSettings dialog = h.get("appearanceDialog");
            assertFalse(text(dialog.content).contains("Choose your avatar"));
            dialog.content.appearance.themes.getChildAt(1).performClick();
            assertEquals("dark", h.<String>get("theme")); assertTrue(dialog.content.appearance.themes.getChildAt(1).isSelected());
            assertNotSame(root, h.get("root")); assertNotSame(history, h.get("history")); assertNotSame(editor, h.composer());
            assertTrue(h.<NativeUi>get("ui").dark);
            assertEquals("Keep this draft while changing appearance", h.composer().input.getText().toString());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
            change(h, "dark", "ja");
            assertSame(dialog, h.get("appearanceDialog")); assertTrue(dialog.isShowing());
            assertTrue(text(dialog.content.heading).contains("外観と言語"));
            assertTrue(dialog.content.appearance.language.isEnabled()); assertTrue(h.io.jobs.isEmpty());
            assertEquals("dark", h.activity.getPreferences(android.app.Activity.MODE_PRIVATE).getString("theme", ""));
            assertEquals("ja", h.activity.getPreferences(android.app.Activity.MODE_PRIVATE).getString("language", ""));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            assertEquals("ja", h.<NativeUi>get("ui").locale);
            View applied = h.get("root");
            dialog.content.close.performClick();
            assertSame("Closing the dialog must not defer another appearance update", applied, h.get("root"));
            assertEquals("ja", h.<NativeUi>get("ui").locale); assertTrue(h.<NativeUi>get("ui").dark);
            assertSame(client, h.get("client")); assertSame(stream, h.get("workspaceStream"));
            assertSame(workspaceListener, MainActivityGoalTest.ShadowClient.workspaceListener);
            assertSame(chatListener, MainActivityGoalTest.ShadowClient.chatListener);
            assertEquals(1, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            assertEquals(1, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
            assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void localChoicesStayAvailableWithoutAnAuthenticatedConnection() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            h.set("user", null); h.set("client", null); h.call("openAppearance"); idle();
            NativeSettings dialog = h.get("appearanceDialog"); View root = h.get("root");
            change(h, "dark", "zh-CN");
            assertEquals("dark", h.<String>get("theme")); assertEquals("zh-CN", h.<String>get("language"));
            assertTrue(dialog.content.appearance.language.isEnabled()); assertTrue(dialog.content.appearance.themes.getChildAt(1).isEnabled());
            assertNotSame(root, h.get("root")); assertNull(h.get("client"));
            assertTrue(h.<NativeUi>get("ui").dark); assertEquals("zh-CN", h.<NativeUi>get("ui").locale);
            assertEquals("zh-CN", h.activity.getPreferences(android.app.Activity.MODE_PRIVATE).getString("language", ""));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
        }
    }

    private void layout(View view) {
        view.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, 360, 800);
    }

    private void snapshot(View view, String name) throws Exception {
        layout(view);
        android.graphics.Bitmap bitmap = android.graphics.Bitmap.createBitmap(360, 800, android.graphics.Bitmap.Config.ARGB_8888);
        view.draw(new android.graphics.Canvas(bitmap));
        java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, name))) {
            bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output);
        } finally { bitmap.recycle(); }
    }

    @Test public void openingAndClosingSettingsKeepsTheSidebarAndBackDismissesOnlyTheDialog() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.call("openSidebar"); idle(); layout(h.get("root"));
            NativeSidebar sidebar = h.get("sidebar"); Object drawer = h.get("drawer");
            h.call("openSettings"); idle();
            assertSame(sidebar, h.get("sidebar")); assertSame(drawer, h.get("drawer"));
            assertEquals("sidebar", h.<String>get("panel"));
            assertTrue(h.<NativeSettings>get("settingsDialog").isShowing());
            h.call("back"); idle();
            assertNull(h.get("settingsDialog")); assertSame(sidebar, h.get("sidebar")); assertSame(drawer, h.get("drawer"));
        }
    }

    @Test public void themeAndLanguageImmediatelyUpdateTheOpenSidebarBehindTheSameAppearanceWindow() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            org.json.JSONArray chats = new org.json.JSONArray();
            for (int i = 0; i < 30; i++) chats.put(new org.json.JSONObject().put("id", "chat-" + i).put("title", "Conversation " + i)
                    .put("project_id", "project-one").put("backend", "codex").put("status", "completed").put("updated_at", i));
            h.set("chats", chats); h.<NativeSidebarState>get("sidebarState").recentLimit = 30;
            h.composer().input.setText("Draft behind appearance dialog");
            h.call("openSidebar"); layout(h.get("root")); idle();
            NativeSidebar original = h.get("sidebar"); original.scroll.scrollTo(0, 120);
            assertEquals(120, original.scroll.getScrollY());
            h.call("openAppearance"); idle();
            NativeSettings dialog = h.get("appearanceDialog"); View decor = dialog.getWindow().getDecorView();
            assertSame(original, h.get("sidebar"));
            change(h, "dark", "ja"); layout(h.get("root")); idle();
            assertSame(dialog, h.get("appearanceDialog")); assertSame(decor, dialog.getWindow().getDecorView()); assertTrue(dialog.isShowing());
            assertEquals("sidebar", h.<String>get("panel")); assertNotNull(h.get("drawer"));
            assertTrue(h.<NativeUi>get("ui").dark); assertEquals("ja", h.<NativeUi>get("ui").locale);
            NativeSidebar updated = h.get("sidebar");
            assertEquals(120, updated.scroll.getScrollY());
            assertEquals(NativeText.translate("Close sidebar", "ja"), updated.brand.getChildAt(updated.brand.getChildCount() - 1).getContentDescription());
            assertEquals("Draft behind appearance dialog", h.composer().input.getText().toString());
            h.call("closeAppearance"); assertSame(updated, h.get("sidebar"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    private void desktopEvent(String raw) {
        MainActivityWorkspaceEventsTest.event("desktop", raw);
    }

    @Test public void websocketAvailabilityUpdatesTheFooterAndSurvivesAppearanceChangesWithoutPolling() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityWorkspaceEventsTest.install(h); h.call("startWorkspace"); h.io.run();
            MainActivityWorkspaceEventsTest.event("ready", "{}");
            h.call("openSidebar"); idle();
            assertNull(h.<NativeSidebar>get("sidebar").remote);
            desktopEvent("{available:true}");
            NativeSidebar first = h.get("sidebar");
            assertNotNull(first.remote);
            assertSame(first.remote, first.footer.getChildAt(first.footer.getChildCount() - 3));
            snapshot(h.get("root"), "sidebar-remote-light.png");
            change(h, "dark", "ja");
            assertNotSame(first, h.get("sidebar"));
            assertNotNull(h.<NativeSidebar>get("sidebar").remote);
            snapshot(h.get("root"), "sidebar-remote-dark.png");
            change(h, "light", "zh-CN");
            assertNotNull(h.<NativeSidebar>get("sidebar").remote);
            h.call("openSettings"); idle(); h.call("closeSettings");
            assertNotNull(h.<NativeSidebar>get("sidebar").remote);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(10000));
            assertTrue("Availability uses the existing WebSocket without HTTP polling", MainActivityGoalTest.ShadowClient.calls.isEmpty());
            assertTrue(h.io.jobs.isEmpty());
            desktopEvent("{available:false}");
            assertNull(h.<NativeSidebar>get("sidebar").remote);
            desktopEvent("{available:true}");
            h.<NativeSidebar>get("sidebar").remote.performClick();
            assertNotNull(h.get("desktopDialog"));
            assertNull(h.get("sidebar"));
        }
    }

    @Test public void disconnectedOrReplacedWorkspaceCannotRestoreAnOldServersRemoteEntry() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityWorkspaceEventsTest.install(h); h.call("startWorkspace"); h.io.run();
            EventListener old = MainActivityGoalTest.ShadowClient.workspaceListener;
            MainActivityWorkspaceEventsTest.event("ready", "{}");
            h.call("openSidebar"); desktopEvent("{available:true}");
            assertNotNull(h.<NativeSidebar>get("sidebar").remote);
            h.call("stopStreams");
            assertNull(h.<NativeSidebar>get("sidebar").remote);
            old.onEvent("desktop", "{available:true}"); idle();
            assertNull(h.<NativeSidebar>get("sidebar").remote);
            h.call("clearWorkspace"); h.call("render");
            old.onEvent("desktop", "{available:true}"); idle();
            assertFalse(h.<Boolean>get("desktopAvailable"));
            assertNull(h.<NativeSidebar>get("sidebar").remote);
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }
}
