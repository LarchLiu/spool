package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.ScrollView;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.Client;

/** Actual startup/session callbacks and dialog windows, without a real node or account. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityConnectionTest {
    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    private void response(int status, String body) throws Exception {
        MainActivityGoalTest.ShadowClient.response = new JSONObject().put("status", status).put("body", body).toString();
    }

    private void restore(MainActivityGoalTest.Harness h) {
        ReflectionHelpers.callInstanceMethod(h.activity, "restoreSession",
                ReflectionHelpers.ClassParameter.from(Client.class, h.get("client")),
                ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("generation")));
        h.io.jobs.remove(0).run();
        idle();
    }

    private void waiting(MainActivityGoalTest.Harness h) {
        h.call("clearWorkspace");
        h.set("origin", "https://fixture.invalid");
        h.set("connected", true);
        h.set("restoringSession", true);
        h.call("render");
        assertTrue(h.<ViewGroup>get("root").getChildAt(0) instanceof NativeConnecting);
        assertNull(h.get("username"));
        assertNull(h.get("password"));
    }

    private void health(MainActivityGoalTest.Harness h, String problem, boolean available) {
        ConnectionRecovery.Snapshot value = new ConnectionRecovery.Snapshot("Running",
                problem.isEmpty() ? "Verified" : "Failed", problem, "", problem.isEmpty() ? "" : "Fixture failure");
        ReflectionHelpers.callInstanceMethod(h.activity, "acceptConnectionHealth",
                ReflectionHelpers.ClassParameter.from(Client.class, h.get("client")),
                ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("generation")),
                ReflectionHelpers.ClassParameter.from(ConnectionRecovery.Snapshot.class, value),
                ReflectionHelpers.ClassParameter.from(boolean.class, available));
    }

    @Test public void healthLossAndRecoveryRetainTheActualWorkspaceDraftAndSessionWithoutReplayingWrites() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("connected", true);
            h.composer().input.setText("Draft waiting for network");
            NativeUi.Composer composer = h.composer();
            View header = h.get("workspaceHeader");
            JSONObject user = h.get("user"), chat = h.get("chat");
            Client client = h.get("client");
            int generation = h.get("generation"), session = h.get("authEpoch");
            health(h, "network", true); assertTrue(h.<Boolean>get("connected"));
            health(h, "network", false); assertFalse(h.<Boolean>get("connected"));
            assertTrue(h.<Boolean>get("connecting")); assertEquals("Failed", h.<String>get("httpsState"));
            assertSame(user, h.get("user")); assertSame(chat, h.get("chat"));
            assertSame(header, h.get("workspaceHeader")); assertSame(composer, h.composer());
            assertEquals("Draft waiting for network", h.composer().input.getText().toString());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
            health(h, "", true);
            assertTrue(h.<Boolean>get("connected")); assertFalse(h.<Boolean>get("connecting"));
            assertEquals("Verified", h.<String>get("httpsState")); assertEquals("", h.<String>get("connectionMessage"));
            assertSame(client, h.get("client")); assertSame(user, h.get("user")); assertSame(chat, h.get("chat"));
            assertEquals(generation, h.<Integer>get("generation").intValue());
            assertEquals(session, h.<Integer>get("authEpoch").intValue());
            assertEquals("chat-one", h.<String>get("chatID")); assertEquals("test-csrf", h.<String>get("csrf"));
            assertSame(composer, h.composer()); assertEquals("Draft waiting for network", composer.input.getText().toString());
            assertEquals(1, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(0).equals("POST")));
            int reads = h.io.jobs.size(), streams = MainActivityGoalTest.ShadowClient.chatSubscriptions.size();
            health(h, "", true); // Healthy sampling must not repeatedly reopen streams or reload data.
            assertEquals(reads, h.io.jobs.size()); assertEquals(streams, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
        }
    }

    @Test public void repeatedHealthySamplesSerializeSessionRestoreAndRetryOnlyTheRead() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            waiting(h);
            health(h, "", true); health(h, "", true);
            assertEquals(1, h.io.jobs.size());
            response(503, "{error:'Server unavailable'}"); h.io.run();
            assertTrue(h.<Boolean>get("restoringSession")); assertFalse(h.<Boolean>get("sessionCheckBusy"));
            health(h, "", true); health(h, "", true);
            assertEquals(1, h.io.jobs.size());
            response(401, "{error:'Sign in required'}"); h.io.run();
            assertFalse(h.<Boolean>get("restoringSession")); assertNotNull(h.get("username"));
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
            for (java.util.List<String> call : MainActivityGoalTest.ShadowClient.calls) {
                assertEquals("GET", call.get(0)); assertEquals("/api/me", call.get(1));
            }
        }
    }

    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void savedServerStartsWithConnectingBeforeAnyTransportWorkAndFreshInstallShowsLogin() {
        for (String origin : new String[] {"https://fixture.invalid", ""}) {
            ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class);
            MainActivity activity = controller.get();
            activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().putString("serverOrigin", origin).commit();
            MainActivityGoalTest.Queue connections = new MainActivityGoalTest.Queue();
            ExecutorService original = ReflectionHelpers.getField(activity, "connectionWorker");
            original.shutdown();
            ReflectionHelpers.setField(activity, "connectionWorker", connections);
            try {
                controller.create().start().resume().visible();
                ViewGroup root = ReflectionHelpers.getField(activity, "root");
                if (origin.isEmpty()) assertTrue(root.getChildAt(0) instanceof NativeLogin);
                else {
                    assertTrue(root.getChildAt(0) instanceof NativeConnecting);
                    assertNull(ReflectionHelpers.getField(activity, "username"));
                    assertFalse(connections.jobs.isEmpty());
                }
            } finally {
                activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
                controller.pause().stop().destroy();
                connections.jobs.clear();
            }
        }
    }

    @Test public void restoredSessionKeepsLocalPreferencesAndTheSettingsWindow() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            h.set("theme", "light"); h.set("language", "en");
            waiting(h);
            h.call("openSettings");
            idle();
            NativeSettings dialog = h.get("settingsDialog");
            View decor = dialog.getWindow().getDecorView();
            AtomicInteger dismissed = new AtomicInteger(), detached = new AtomicInteger();
            dialog.setOnDismissListener(value -> dismissed.incrementAndGet());
            decor.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
                public void onViewAttachedToWindow(View view) {}
                public void onViewDetachedFromWindow(View view) { detached.incrementAndGet(); }
            });
            layout(dialog.modal, 360, 800);
            ScrollView scroll = ReflectionHelpers.getField(dialog, "scroll");
            scroll.scrollTo(0, 40);
            assertEquals(40, scroll.getScrollY());
            response(200, "{user:{id:'alice'},csrf:'restored-csrf',preferences:{theme:'dark',language:'ja'}}");
            restore(h);
            layout(dialog.modal, 360, 800);
            dialog.modal.getViewTreeObserver().dispatchOnPreDraw();
            assertSame(dialog, h.get("settingsDialog"));
            assertSame(decor, dialog.getWindow().getDecorView());
            assertTrue(dialog.isShowing());
            assertEquals(0, dismissed.get());
            assertEquals(0, detached.get());
            ScrollView restored = ReflectionHelpers.getField(dialog, "scroll");
            int maximum = restored.getChildAt(0).getHeight() - restored.getHeight();
            assertTrue(maximum > 0);
            assertEquals(Math.min(40, maximum), restored.getScrollY());
            assertFalse(h.<Boolean>get("restoringSession"));
            assertNotNull(h.get("workspaceHeader"));
            assertNull(h.get("username"));
            assertEquals("alice", h.<JSONObject>get("user").getString("id"));
            assertEquals("restored-csrf", h.<String>get("csrf"));
            assertFalse(h.<NativeUi>get("ui").dark);
            assertEquals("en", h.<NativeUi>get("ui").locale);
            dialog.content.close.performClick();
            idle();
            assertFalse(dialog.isShowing());
            assertNull(h.get("settingsDialog"));
            assertFalse(h.<Boolean>get("settingsOpen"));
        }
    }

    @Test public void onlyAnUnauthorizedSessionRevealsLoginAndKeepsAnOpenSettingsWindow() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            waiting(h);
            h.call("openSettings");
            NativeSettings dialog = h.get("settingsDialog");
            response(401, "{error:'Session expired'}");
            restore(h);
            assertFalse(h.<Boolean>get("restoringSession"));
            assertTrue(h.<ViewGroup>get("root").getChildAt(0) instanceof NativeLogin);
            assertNotNull(h.get("username"));
            assertSame(dialog, h.get("settingsDialog"));
            assertTrue(dialog.isShowing());
        }
    }

    @Test public void failedSessionCheckKeepsLoginHiddenAndRetryCanRestoreTheSession() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            waiting(h);
            response(503, "{error:'Server unavailable'}");
            restore(h);
            NativeConnecting page = (NativeConnecting) h.<ViewGroup>get("root").getChildAt(0);
            assertNull(h.get("username"));
            Button retry = findButton(page, "Retry");
            assertNotNull(retry);
            response(200, "{user:{id:'alice'},csrf:'restored-csrf'}");
            retry.performClick();
            assertTrue(h.<ViewGroup>get("root").getChildAt(0) instanceof NativeConnecting);
            h.io.jobs.remove(0).run();
            idle();
            assertNotNull(h.get("workspaceHeader"));
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
            for (java.util.List<String> call : MainActivityGoalTest.ShadowClient.calls) {
                assertEquals("GET", call.get(0));
                assertEquals("/api/me", call.get(1));
            }
        }
    }

    private Button findButton(View view, String text) {
        if (view instanceof Button && text.contentEquals(((Button) view).getText())) return (Button) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            Button found = findButton(((ViewGroup) view).getChildAt(i), text);
            if (found != null) return found;
        }
        return null;
    }
}
