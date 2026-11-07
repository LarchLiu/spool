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

    @Test public void rapidChoicesUpdateOnlyTheDialogAndStayLocalWithoutReconnecting() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityWorkspaceEventsTest.install(h); h.call("startWorkspace"); h.io.run();
            MainActivityWorkspaceEventsTest.event("ready", "{}"); h.call("startChat");
            Client client = h.get("client"); Object stream = h.get("workspaceStream");
            EventListener workspaceListener = MainActivityGoalTest.ShadowClient.workspaceListener, chatListener = MainActivityGoalTest.ShadowClient.chatListener;
            View root = h.get("root"), history = h.get("history"), editor = h.composer();
            MainActivityGoalTest.ShadowClient.responder = call -> {
                assertEquals("Appearance must not request or save server state", "/api/desktop", call.get(1));
                return "{status:200,body:'{}'}";
            };
            h.call("openSettings"); idle(); h.io.run();
            MainActivityGoalTest.ShadowClient.calls.clear();
            NativeSettings dialog = h.get("settingsDialog");
            assertFalse(text(dialog.content).contains("Choose your avatar"));
            dialog.content.appearance.themes.getChildAt(1).performClick();
            assertEquals("dark", h.<String>get("theme")); assertTrue(dialog.content.appearance.themes.getChildAt(1).isSelected());
            assertSame(root, h.get("root")); assertSame(history, h.get("history")); assertSame(editor, h.composer());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
            change(h, "dark", "ja");
            assertSame(dialog, h.get("settingsDialog")); assertTrue(dialog.isShowing());
            assertTrue(text(dialog.content.heading).contains("外観と言語"));
            assertTrue(dialog.content.appearance.language.isEnabled()); assertTrue(h.io.jobs.isEmpty());
            assertEquals("dark", h.activity.getPreferences(android.app.Activity.MODE_PRIVATE).getString("theme", ""));
            assertEquals("ja", h.activity.getPreferences(android.app.Activity.MODE_PRIVATE).getString("language", ""));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertSame(root, h.get("root"));
            dialog.content.close.performClick();
            assertNotSame("The workspace applies the accumulated appearance once on close", root, h.get("root"));
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
            h.set("user", null); h.set("client", null); h.call("openSettings"); idle();
            NativeSettings dialog = h.get("settingsDialog"); View root = h.get("root");
            change(h, "dark", "zh-CN");
            assertEquals("dark", h.<String>get("theme")); assertEquals("zh-CN", h.<String>get("language"));
            assertTrue(dialog.content.appearance.language.isEnabled()); assertTrue(dialog.content.appearance.themes.getChildAt(1).isEnabled());
            assertSame(root, h.get("root")); assertNull(h.get("client"));
            assertEquals("zh-CN", h.activity.getPreferences(android.app.Activity.MODE_PRIVATE).getString("language", ""));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void appearanceChangesKeepRemoteAvailabilityVisibleAndPreserveThePollingDeadline() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.ShadowClient.response = "{status:200,body:'{\"available\":true}'}";
            h.call("openSettings"); idle(); h.io.run();
            long due = SystemClock.uptimeMillis() + 5000;
            Object poll = h.get("desktopAvailability");
            assertEquals(View.VISIBLE, h.<View>get("desktopSettings").getVisibility());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(2000));
            change(h, "dark", "ja");
            assertEquals("The known remote connection entry must stay visible after rebinding", View.VISIBLE, h.<View>get("desktopSettings").getVisibility());
            assertSame("Appearance must keep the active availability monitor", poll, h.get("desktopAvailability"));
            change(h, "light", "zh-CN");
            assertEquals(View.VISIBLE, h.<View>get("desktopSettings").getVisibility());
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size()); assertTrue(h.io.jobs.isEmpty());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(due - SystemClock.uptimeMillis()));
            assertEquals("The original five-second deadline must survive repeated appearance changes", 1, h.io.jobs.size());
            h.io.run(); assertEquals(View.VISIBLE, h.<View>get("desktopSettings").getVisibility());
            MainActivityGoalTest.ShadowClient.response = "{status:200,body:'{\"available\":false}'}";
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(5000)); h.io.run();
            assertEquals("Actual server unavailability still hides the entry", View.GONE, h.<View>get("desktopSettings").getVisibility());
            h.call("closeSettings");
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(5000)); assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void inFlightAvailabilityUpdatesTheReboundSectionAndCannotLeakIntoReopenedSettings() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.ShadowClient.response = "{status:200,body:'{\"available\":true}'}";
            h.call("openSettings"); idle(); assertEquals(1, h.io.jobs.size());
            View first = h.get("desktopSettings"); change(h, "dark", "ja");
            View current = h.get("desktopSettings"); assertNotSame(first, current);
            h.io.run();
            assertEquals("A request started before the appearance change must update the current section", View.VISIBLE, current.getVisibility());
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(5000));
            Runnable previousRequest = h.io.jobs.remove(0); previousRequest.run();
            h.call("closeSettings"); h.call("openSettings"); idle();
            assertEquals("A result posted by the closed settings session cannot authorize a new section", View.GONE, h.<View>get("desktopSettings").getVisibility());
            MainActivityGoalTest.ShadowClient.response = "{status:200,body:'{\"available\":false}'}"; h.io.run();
            assertEquals(View.GONE, h.<View>get("desktopSettings").getVisibility());
        }
    }
}
