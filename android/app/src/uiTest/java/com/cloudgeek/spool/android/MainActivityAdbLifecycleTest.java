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
        String adbAuth;
        int attaches, networkChanges;
        @Implementation protected ADBStream shareADB(String csrf, String device, long port, EventListener listener) {
            attaches++; adb = listener; adbAuth = csrf; stream = new ADBStream();
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
        return startSelected(h);
    }

    private ShadowClient startSelected(MainActivityGoalTest.Harness h) {
        Shadows.shadowOf(h.activity).grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS);
        h.set("adbAvailability", ""); h.set("chatID", ""); h.set("chat", null);
        ReflectionHelpers.callInstanceMethod(h.activity, "startAdbShare", ReflectionHelpers.ClassParameter.from(int.class, 44000));
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(AdbShare.Phase.ONLINE, h.<AdbShare>get("adbShare").phase);
        return org.robolectric.shadow.api.Shadow.extract(h.<Client>get("client"));
    }

    @Test public void openingTheAppClearsOnlyOrphanedAdbNotifications() throws Exception {
        android.content.Context context = org.robolectric.RuntimeEnvironment.getApplication();
        android.app.NotificationManager manager = context.getSystemService(android.app.NotificationManager.class);
        manager.createNotificationChannel(new android.app.NotificationChannel("adb-share", "ADB bridge", android.app.NotificationManager.IMPORTANCE_LOW));
        manager.createNotificationChannel(new android.app.NotificationChannel("other", "Other", android.app.NotificationManager.IMPORTANCE_LOW));
        manager.notify(42, new android.app.Notification.Builder(context, "adb-share").setSmallIcon(R.drawable.web_icon_smartphone).setOngoing(true).build());
        manager.notify(900, new android.app.Notification.Builder(context, "other").setSmallIcon(R.drawable.web_icon_smartphone).setContentTitle("Keep this").build());
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            java.util.List<android.app.Notification> notices = Shadows.shadowOf(manager).getAllNotifications();
            assertEquals(1, notices.size());
            assertEquals("Keep this", notices.get(0).extras.getString(android.app.Notification.EXTRA_TITLE));
        } finally { manager.cancel(42); manager.cancel(900); }
    }

    @Test public void twoServersKeepTheirTunnelsAcrossTabSwitchesAndStopIndependently() throws Exception {
        try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
            ShadowClient home = startSelected(f.h);
            AdbShare homeShare = f.h.get("adbShare");
            ShadowAdbStream homeStream = org.robolectric.shadow.api.Shadow.extract(home.stream);
            f.select(f.office.id);
            assertEquals(AdbShare.Phase.ONLINE, homeShare.phase);
            assertFalse(homeStream.closed);
            ShadowClient office = startSelected(f.h);
            AdbShare officeShare = f.h.get("adbShare");
            ShadowAdbStream officeStream = org.robolectric.shadow.api.Shadow.extract(office.stream);
            assertNotSame(homeShare, officeShare);
            for (int i = 0; i < 3; i++) {
                f.select(f.home); assertSame(homeShare, f.h.get("adbShare"));
                f.select(f.office.id); assertSame(officeShare, f.h.get("adbShare"));
                assertEquals(AdbShare.Phase.ONLINE, homeShare.phase);
                assertEquals(AdbShare.Phase.ONLINE, officeShare.phase);
                assertFalse(homeStream.closed); assertFalse(officeStream.closed);
                assertEquals(1, home.attaches); assertEquals(1, office.attaches);
            }
            f.h.call("stopAdbShare");
            assertTrue(officeStream.closed); assertFalse(homeStream.closed);
            assertEquals(AdbShare.Phase.IDLE, officeShare.phase);
            assertEquals(AdbShare.Phase.ONLINE, homeShare.phase);
        }
    }

    @Test public void anInactiveServersRetryUsesItsOwnClientAndLogin() throws Exception {
        try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
            ShadowClient home = startSelected(f.h);
            AdbShare homeShare = f.h.get("adbShare");
            f.responses(200, null, "office-user", "office-csrf");
            f.select(f.office.id);
            // The background profile already has its own verified session; give the selected
            // account a distinct token to catch accidental use by the home retry.
            f.h.set("user", new org.json.JSONObject().put("id", "office-user"));
            f.h.set("csrf", "office-csrf");
            java.util.function.Function<java.util.List<String>, String> responder = MainActivityGoalTest.ShadowClient.responder;
            MainActivityGoalTest.ShadowClient.responder = call -> call.get(1).equals("/api/adb")
                    ? "{status:200,body:'{available:true}'}" : responder.apply(call);
            ShadowClient office = startSelected(f.h);
            AdbShare officeShare = f.h.get("adbShare");
            home.adb.onEvent("closed", "{\"code\":\"network\"}");
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(AdbShare.Phase.WAITING, homeShare.phase);
            assertEquals(AdbShare.Phase.ONLINE, officeShare.phase);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(2, java.util.concurrent.TimeUnit.SECONDS);
            f.drain();
            assertEquals(2, home.attaches); assertEquals("shared-csrf", home.adbAuth);
            assertEquals(1, office.attaches); assertEquals("office-csrf", office.adbAuth);
            assertEquals(AdbShare.Phase.ONLINE, homeShare.phase);
            assertSame(officeShare, f.h.get("adbShare"));
        }
    }

    @Test public void pendingProbesFinishAndSharesReconnectAfterTheAppStops() throws Exception {
        for (boolean selected : new boolean[] {true, false}) {
            try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
                ShadowClient home = startSelected(f.h);
                AdbShare share = f.h.get("adbShare");
                if (selected) f.checkDirectRoute();
                else { f.select(f.office.id); f.h.call("refreshServerConnections"); }
                Object runtime = f.h.<java.util.Map<String, ?>>get("serverRuntimes").get(f.home);
                assertTrue(ReflectionHelpers.<Boolean>getField(runtime, "selecting"));
                f.h.controller.pause().stop(); f.drainConnections();
                allowAdb();
                home.adb.onEvent("closed", "{code:'network'}");
                Shadows.shadowOf(Looper.getMainLooper()).idleFor(2, java.util.concurrent.TimeUnit.SECONDS);
                f.drain();
                assertEquals(AdbShare.Phase.ONLINE, share.phase);
                assertEquals(2, home.attaches);
                assertEquals("shared-csrf", home.adbAuth);
                assertFalse(f.h.<Boolean>get("foreground"));
            }
        }
    }

    @Test public void anInactiveRouteReplacementFinishesWhileTheAppIsStopped() throws Exception {
        try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
            homeRoutes(f);
            ShadowClient home = startSelected(f.h);
            AdbShare share = f.h.get("adbShare");
            f.select(f.office.id);
            home.healthResponse = "{state:'Offline',https:'Failed',problem:'network'}";
            f.h.call("refreshServerConnections");
            // The worker has closed the old route and posted the new one, but the main
            // thread has not received either callback when Android stops the Activity.
            f.connections.jobs.remove(0).run();
            f.h.controller.pause().stop();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            Object runtime = f.h.<java.util.Map<String, ?>>get("serverRuntimes").get(f.home);
            ConnectionRouter.Candidate<Client> route = ReflectionHelpers.getField(runtime, "candidate");
            assertNotNull(route); assertEquals(ConnectionMode.TAILSCALE, route.endpoint.mode);
            ShadowClient replacement = org.robolectric.shadow.api.Shadow.extract(route.client);
            allowAdb();
            home.adb.onEvent("closed", "{code:'network'}");
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(2, java.util.concurrent.TimeUnit.SECONDS);
            f.drain();
            assertEquals(AdbShare.Phase.ONLINE, share.phase);
            assertEquals(1, home.attaches); assertEquals(1, replacement.attaches);
            assertEquals("shared-csrf", replacement.adbAuth);
        }
    }

    @Test public void aShareWithNoRemainingRouteCanRecoverWithoutReopeningTheApp() throws Exception {
        try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
            homeRoutes(f);
            ShadowClient home = startSelected(f.h);
            AdbShare share = f.h.get("adbShare");
            f.select(f.office.id);
            home.healthResponse = "{state:'Offline',https:'Failed',problem:'network'}";
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = home.healthResponse;
            MainActivityConnectionTest.ShadowSpoolnet.failDirect = true;
            f.h.call("refreshServerConnections"); f.drainConnections();
            Object runtime = f.h.<java.util.Map<String, ?>>get("serverRuntimes").get(f.home);
            assertNull(ReflectionHelpers.getField(runtime, "candidate"));
            f.h.controller.pause().stop();
            MainActivityConnectionTest.ShadowSpoolnet.failDirect = false;
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = null;
            allowAdb();
            home.adb.onEvent("closed", "{code:'network'}");
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(2, java.util.concurrent.TimeUnit.SECONDS);
            f.drainConnections();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(3, java.util.concurrent.TimeUnit.SECONDS);
            f.drain();
            assertEquals(AdbShare.Phase.ONLINE, share.phase);
            assertFalse(f.h.<Boolean>get("foreground"));
        } finally { MainActivityConnectionTest.ShadowSpoolnet.failDirect = false; }
    }

    @Test public void aProbeThatConfirmsExpiryAfterTheAppStopsEndsOnlyItsOwnShare() throws Exception {
        try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
            ShadowClient home = startSelected(f.h);
            AdbShare homeShare = f.h.get("adbShare");
            f.select(f.office.id);
            ShadowClient office = startSelected(f.h);
            AdbShare officeShare = f.h.get("adbShare");
            f.responses(401);
            f.h.call("refreshServerConnections");
            f.h.controller.pause().stop(); f.drainConnections();
            assertEquals(AdbShare.Phase.IDLE, homeShare.phase);
            assertTrue(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(home.stream)).closed);
            assertEquals(AdbShare.Phase.ONLINE, officeShare.phase);
            assertFalse(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(office.stream)).closed);
        }
    }

    @Test public void savingChangedAddressesOnATabSwitchEndsOnlyThatServersShare() throws Exception {
        for (ConnectionMode mode : ConnectionMode.values()) {
            try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
                ShadowClient home = startSelected(f.h);
                AdbShare homeShare = f.h.get("adbShare");
                f.select(f.office.id);
                ShadowClient office = startSelected(f.h);
                AdbShare officeShare = f.h.get("adbShare");
                f.select(f.home); f.h.call("openSettings");
                String address = mode == ConnectionMode.RELAY ? RelayPairingTest.code("https://replacement.example.com")
                        : "https://replacement.example.com";
                f.h.<NativeConnectionSettings>get("connectionSettings").input(mode).setText(address);
                f.h.call("closeSettings"); f.select(f.office.id);
                ServerProfiles.Server saved = f.servers.entries.stream().filter(server -> server.id.equals(f.home)).findFirst().orElseThrow();
                assertEquals(address, saved.plan.origin(mode));
                assertEquals(mode.name(), AdbShare.Phase.IDLE, homeShare.phase);
                assertTrue(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(home.stream)).closed);
                assertSame(officeShare, f.h.get("adbShare"));
                assertEquals(AdbShare.Phase.ONLINE, officeShare.phase);
                assertFalse(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(office.stream)).closed);
            }
        }
    }

    private static void allowAdb() {
        java.util.function.Function<java.util.List<String>, String> responder = MainActivityGoalTest.ShadowClient.responder;
        MainActivityGoalTest.ShadowClient.responder = call -> call.get(1).equals("/api/adb")
                ? "{status:200,body:'{available:true}'}" : responder.apply(call);
    }

    private static void homeRoutes(MainActivityServerTabsTest.Fixture f) {
        ServerProfiles.Server home = f.servers.active();
        home.plan = new ConnectionPlan(home.plan.direct, "https://home.example.ts.net", ConnectionMode.DIRECT, true);
        f.h.set("connectionPlan", home.plan); f.h.set("connectionDraft", home.plan);
    }

    @Test public void disablingOrExpiringAnInactiveServerOnlyStopsItsOwnShare() throws Exception {
        for (boolean disable : new boolean[] {true, false}) {
            try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
                ShadowClient home = startSelected(f.h);
                AdbShare homeShare = f.h.get("adbShare");
                f.select(f.office.id);
                ShadowClient office = startSelected(f.h);
                AdbShare officeShare = f.h.get("adbShare");
                if (disable) f.enable(f.home, false);
                else f.push(f.homeClient, "auth_expired", "{}");
                assertEquals(AdbShare.Phase.IDLE, homeShare.phase);
                assertTrue(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(home.stream)).closed);
                assertEquals(AdbShare.Phase.ONLINE, officeShare.phase);
                assertFalse(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(office.stream)).closed);
            }
        }
    }

    @Test public void disconnectingDeletingOrClearingTheSelectedLoginKeepsTheOtherServerShared() throws Exception {
        for (String action : new String[] {"disconnect", "deleteServer", "clearWorkspace"}) {
            try (MainActivityServerTabsTest.Fixture f = new MainActivityServerTabsTest.Fixture(true)) {
                ShadowClient home = startSelected(f.h);
                AdbShare homeShare = f.h.get("adbShare");
                f.select(f.office.id);
                ShadowClient office = startSelected(f.h);
                AdbShare officeShare = f.h.get("adbShare");
                f.h.call(action);
                assertEquals(action, AdbShare.Phase.IDLE, officeShare.phase);
                assertTrue(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(office.stream)).closed);
                assertEquals(action, AdbShare.Phase.ONLINE, homeShare.phase);
                assertFalse(((ShadowAdbStream) org.robolectric.shadow.api.Shadow.extract(home.stream)).closed);
            }
        }
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
