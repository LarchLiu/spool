package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.net.ConnectivityManager;
import android.os.Looper;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.ADBStream;
import spoolnet.Client;
import spoolnet.EventListener;

/** Activity lifecycle with the production ADB controller and a simulated Relay transport. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35, shadows = {MainActivityAdbLifecycleTest.ShadowClient.class,
        MainActivityAdbLifecycleTest.ShadowAdbStream.class, MainActivityGoalTest.ShadowSeq.class,
        MainActivityConnectionTest.ShadowSpoolnet.class, MainActivityWorkspaceEventsTest.ShadowStream.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityAdbLifecycleTest {
    @Implements(value = Client.class, isInAndroidSdk = false)
    public static class ShadowClient extends MainActivityGoalTest.ShadowClient {
        EventListener adb;
        ADBStream stream;
        int attaches, networkChanges;
        @Implementation protected ADBStream shareADB(String csrf, String device, long port, EventListener listener) {
            attaches++; adb = listener; stream = new ADBStream();
            adb.onEvent("ready", "{\"serial\":\"127.0.0.1:41000\"}");
            adb.onEvent("state", "{\"state\":\"online\"}");
            return stream;
        }
        @Override @Implementation protected void networkChanged() {
            networkChanges++;
            // The real Relay dialer retires active sockets on a network change.
            if (adb != null) adb.onEvent("closed", "{\"code\":\"network\"}");
        }
    }

    @Implements(value = ADBStream.class, isInAndroidSdk = false)
    public static class ShadowAdbStream {
        boolean closed;
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected void __constructor__() {}
        @Implementation protected void close() { closed = true; }
    }

    private ShadowClient start(MainActivityGoalTest.Harness h) {
        MainActivityConnectionTest.connectionQueue(h);
        Shadows.shadowOf(h.activity).grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS);
        h.set("adbAvailability", ""); h.set("chatID", ""); h.set("chat", null);
        ReflectionHelpers.callInstanceMethod(h.activity, "startAdbShare", ReflectionHelpers.ClassParameter.from(int.class, 44000));
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(AdbShare.Phase.ONLINE, h.<AdbShare>get("adbShare").phase);
        return org.robolectric.shadow.api.Shadow.extract(h.<Client>get("client"));
    }

    @Test public void returningFromAnotherAppKeepsTheSameOnlineAdbTunnel() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            ShadowClient transport = start(h);
            AdbShare share = h.get("adbShare");
            ShadowAdbStream stream = org.robolectric.shadow.api.Shadow.extract(transport.stream);
            for (int i = 0; i < 3; i++) {
                h.controller.pause().stop();
                assertEquals(AdbShare.Phase.ONLINE, share.phase);
                h.controller.start().resume(); h.activity.onWindowFocusChanged(true);
                MainActivityConnectionTest.drain(h.get("connectionWorker"));
                assertEquals(AdbShare.Phase.ONLINE, share.phase);
                assertEquals("127.0.0.1:41000", share.serial);
                assertFalse(stream.closed);
                assertEquals(1, transport.attaches);
                assertEquals(0, transport.networkChanges);
            }
            h.call("stopAdbShare");
            assertTrue(stream.closed);
            assertEquals(AdbShare.Phase.IDLE, share.phase);
        }
    }

    @Test public void anActualNetworkChangeStillRetiresTheTunnelAndWaitsToReconnect() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            ShadowClient transport = start(h);
            ShadowAdbStream stream = org.robolectric.shadow.api.Shadow.extract(transport.stream);
            h.controller.pause().stop();
            h.<ConnectivityManager.NetworkCallback>get("networkCallback").onLost(null);
            MainActivityConnectionTest.drain(h.get("connectionWorker"));
            assertEquals(1, transport.networkChanges);
            assertTrue(stream.closed);
            assertEquals(AdbShare.Phase.WAITING, h.<AdbShare>get("adbShare").phase);
        }
    }
}
