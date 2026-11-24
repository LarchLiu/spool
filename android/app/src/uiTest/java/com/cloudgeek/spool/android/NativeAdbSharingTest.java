package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Application;
import android.app.Notification;
import android.app.NotificationManager;
import android.content.Context;
import android.content.Intent;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.os.Bundle;
import android.os.Looper;
import android.view.View;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;
import org.robolectric.android.controller.ServiceController;
import org.robolectric.shadows.ShadowNsdManager;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeAdbSharingTest {
    @Test public void footerOffersAdbBeforeSettingsAndOnlyShowsADotForAnActiveShare() throws Exception {
        NativeUi ui = new NativeUi(RuntimeEnvironment.getApplication(), false, "en");
        List<String> actions = new ArrayList<>();
        NativeSidebar sidebar = new NativeSidebar(ui, new NativeSidebarState(), id -> new org.json.JSONObject(),
                () -> {}, id -> {}, id -> {}, () -> {}, () -> actions.add("settings"), () -> {});
        sidebar.bind(new org.json.JSONObject().put("id", "alice"), new org.json.JSONArray(), new org.json.JSONArray(), "", "", "", false);
        assertNull(sidebar.adb);
        sidebar.adb(true, "IDLE", "", () -> actions.add("adb"));
        assertNotNull(sidebar.adb);
        assertEquals("ADB bridge", sidebar.adb.getTooltipText());
        assertNull(sidebar.findViewWithTag("adb-status"));
        int settings = sidebar.footer.getChildCount() - 2;
        assertSame(sidebar.adb.getParent(), sidebar.footer.getChildAt(settings - 1));
        sidebar.adb.performClick(); sidebar.footer.getChildAt(settings).performClick();
        assertEquals(List.of("adb", "settings"), actions);
        for (String phase : new String[] {"CONNECTING", "ONLINE", "WAITING", "UNPAIRED"}) {
            sidebar.adb(true, phase, "Current share", () -> {});
            assertNotNull(sidebar.findViewWithTag("adb-status"));
            assertTrue(sidebar.adb.getContentDescription().toString().contains("Current share"));
        }
        sidebar.adb(true, "FAILED", "Sharing ended", () -> {});
        assertNull(sidebar.findViewWithTag("adb-status"));
        sidebar.adb(false, "IDLE", "", () -> {});
        assertNull(sidebar.adb);
        sidebar.deactivate();
    }

    private static final class Owner implements AdbShareService.Controls {
        final Application app = RuntimeEnvironment.getApplication();
        final AdbShare share = new AdbShare(null, () -> {});
        final List<String> codes = new ArrayList<>();
        int stops, failures, notificationFailures;
        @Override public void stop() { stops++; AdbShareService.end(app, this); }
        @Override public void pair(String code) { codes.add(code); }
        @Override public void foregroundUnavailable() { failures++; AdbShareService.end(app, this); }
        @Override public void notificationUnavailable() { notificationFailures++; AdbShareService.end(app, this); }
        Intent show() {
            assertTrue(AdbShareService.show(app, this, share, "Home", "en"));
            Intent intent = Shadows.shadowOf(app).getNextStartedService();
            assertNotNull(intent);
            return intent;
        }
    }

    @Test public void oldNotificationActionsDoNotAffectANewShare() {
        Owner owner = new Owner();
        owner.share.phase = AdbShare.Phase.UNPAIRED;
        ServiceController<AdbShareService> controller = Robolectric.buildService(AdbShareService.class).create();
        AdbShareService service = controller.get();
        try {
            Intent oldStart = owner.show();
            service.onStartCommand(oldStart, 0, 1);
            Notification old = Shadows.shadowOf(service).getLastForegroundNotification();
            assertNotNull(old);
            assertTrue((old.flags & Notification.FLAG_ONGOING_EVENT) != 0);
            assertEquals(2, old.actions.length);
            AdbShareService.end(owner.app, owner);
            assertTrue(Shadows.shadowOf(service).isForegroundStopped());
            Intent start = owner.show();
            service.onStartCommand(oldStart, 0, 2);
            service.onStartCommand(start, 0, 3);
            Notification current = Shadows.shadowOf(service).getLastForegroundNotification();
            Intent oldPair = pairReply(old.actions[0], "111111");
            Intent oldStop = Shadows.shadowOf(old.actions[1].actionIntent).getSavedIntent();
            service.onStartCommand(oldPair, 0, 4);
            service.onStartCommand(oldStop, 0, 5);
            assertTrue(owner.codes.isEmpty());
            assertEquals(0, owner.stops);
            service.onStartCommand(pairReply(current.actions[0], "222222"), 0, 6);
            assertEquals(List.of("222222"), owner.codes);
            assertEquals(current.actions[1].actionIntent, current.deleteIntent);
            service.onStartCommand(Shadows.shadowOf(current.deleteIntent).getSavedIntent(), 0, 7);
            assertEquals(1, owner.stops);
            assertEquals(0, owner.failures);
        } finally { AdbShareService.end(owner.app, owner); controller.destroy(); }
    }

    private static Intent pairReply(Notification.Action action, String reply) {
        Intent intent = new Intent(Shadows.shadowOf(action.actionIntent).getSavedIntent());
        Bundle values = new Bundle();
        values.putCharSequence(action.getRemoteInputs()[0].getResultKey(), reply);
        android.app.RemoteInput.addResultsToIntent(action.getRemoteInputs(), intent, values);
        return intent;
    }

    private static Notification notice(NotificationManager manager, String server) {
        return Shadows.shadowOf(manager).getAllNotifications().stream()
                .filter(n -> ("Sharing this phone · " + server).equals(n.extras.getString(Notification.EXTRA_TITLE)))
                .findFirst().orElseThrow();
    }

    private static void loseProcessState(ServiceController<AdbShareService> controller, Owner foreground) {
        java.util.Map<?, ?> sessions = org.robolectric.util.ReflectionHelpers.getStaticField(AdbShareService.class, "sessions");
        int foregroundID = org.robolectric.util.ReflectionHelpers.getField(sessions.get(foreground), "id");
        // Android removes the foreground notice on process death; ordinary notices remain.
        foreground.app.getSystemService(NotificationManager.class).cancel(foregroundID);
        sessions.clear();
        org.robolectric.util.ReflectionHelpers.setStaticField(AdbShareService.class, "running", null);
        org.robolectric.util.ReflectionHelpers.setStaticField(AdbShareService.class, "nextID", foregroundID);
        controller.destroy();
    }

    @Test @Config(sdk = {30, 35})
    public void staleNotificationsAreRemovedWhenAnActionRestartsTheService() {
        Owner home = new Owner(), office = new Owner();
        home.share.phase = office.share.phase = AdbShare.Phase.ONLINE;
        // Android grants this merged AndroidX signature permission on installation.
        Shadows.shadowOf(home.app).grantPermissions(home.app.getPackageName() + ".DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION");
        NotificationManager manager = home.app.getSystemService(NotificationManager.class);
        ServiceController<AdbShareService> old = Robolectric.buildService(AdbShareService.class).create();
        old.get().onStartCommand(home.show(), 0, 1);
        assertTrue(AdbShareService.show(office.app, office, office.share, "Office", "en"));
        Intent stop = Shadows.shadowOf(notice(manager, "Office").deleteIntent).getSavedIntent();
        loseProcessState(old, home);
        assertEquals(1, Shadows.shadowOf(manager).getAllNotifications().size());
        ServiceController<AdbShareService> restarted = Robolectric.buildService(AdbShareService.class).create();
        try {
            restarted.get().onStartCommand(stop, 0, 1);
            assertTrue(Shadows.shadowOf(manager).getAllNotifications().isEmpty());
            assertEquals(0, office.stops);
        } finally { restarted.destroy(); }
    }

    @Test public void staleActionsCannotCancelNewSharesEvenWhenNotificationIDsAreReused() {
        Owner oldHome = new Owner(), oldOffice = new Owner();
        oldHome.share.phase = oldOffice.share.phase = AdbShare.Phase.UNPAIRED;
        NotificationManager manager = oldHome.app.getSystemService(NotificationManager.class);
        ServiceController<AdbShareService> old = Robolectric.buildService(AdbShareService.class).create();
        old.get().onStartCommand(oldHome.show(), 0, 1);
        assertTrue(AdbShareService.show(oldOffice.app, oldOffice, oldOffice.share, "Old office", "en"));
        Notification stale = notice(manager, "Old office");
        Intent oldStop = Shadows.shadowOf(stale.deleteIntent).getSavedIntent();
        loseProcessState(old, oldHome);
        Owner home = new Owner(), office = new Owner();
        home.share.phase = office.share.phase = AdbShare.Phase.UNPAIRED;
        ServiceController<AdbShareService> current = Robolectric.buildService(AdbShareService.class).create();
        try {
            current.get().onStartCommand(home.show(), 0, 1);
            assertTrue(AdbShareService.show(office.app, office, office.share, "Office", "en"));
            current.get().onStartCommand(oldStop, 0, 2);
            current.get().onStartCommand(pairReply(stale.actions[0], "111111"), 0, 3);
            assertEquals(0, home.stops); assertEquals(0, office.stops);
            assertTrue(home.codes.isEmpty()); assertTrue(office.codes.isEmpty());
            assertEquals(2, Shadows.shadowOf(manager).getAllNotifications().size());
            assertNotNull(notice(manager, "Home")); assertNotNull(notice(manager, "Office"));
        } finally { AdbShareService.end(home.app, home); AdbShareService.end(office.app, office); current.destroy(); }
    }

    @Test public void eachServerHasItsOwnNotificationAndStoppingTheForegroundShareKeepsTheOtherRunning() {
        Owner home = new Owner(), office = new Owner();
        home.share.phase = office.share.phase = AdbShare.Phase.UNPAIRED;
        NotificationManager manager = home.app.getSystemService(NotificationManager.class);
        ServiceController<AdbShareService> controller = Robolectric.buildService(AdbShareService.class).create();
        AdbShareService service = controller.get();
        try {
            service.onStartCommand(home.show(), 0, 1);
            Notification homeNotice = Shadows.shadowOf(service).getLastForegroundNotification();
            assertTrue(AdbShareService.show(office.app, office, office.share, "Office", "en"));
            List<Notification> notices = Shadows.shadowOf(manager).getAllNotifications();
            assertEquals(2, notices.size());
            assertEquals("Sharing this phone · Home", homeNotice.extras.getString(Notification.EXTRA_TITLE));
            Notification officeNotice = notices.stream().filter(n -> "Sharing this phone · Office".equals(n.extras.getString(Notification.EXTRA_TITLE)))
                    .findFirst().orElseThrow();
            service.onStartCommand(pairReply(officeNotice.actions[0], "222222"), 0, 2);
            assertEquals(List.of("222222"), office.codes); assertTrue(home.codes.isEmpty());
            Intent homeStop = Shadows.shadowOf(homeNotice.deleteIntent).getSavedIntent();
            service.onStartCommand(homeStop, 0, 3);
            assertEquals(1, home.stops); assertEquals(0, office.stops);
            assertEquals(1, Shadows.shadowOf(manager).getAllNotifications().size());
            assertEquals("Sharing this phone · Office", Shadows.shadowOf(service).getLastForegroundNotification().extras.getString(Notification.EXTRA_TITLE));
            assertFalse(Shadows.shadowOf(service).isForegroundStopped());
            service.onStartCommand(homeStop, 0, 4);
            assertEquals(1, home.stops); assertEquals(0, office.stops);
            service.onStartCommand(Shadows.shadowOf(officeNotice.deleteIntent).getSavedIntent(), 0, 5);
            assertEquals(1, office.stops);
            assertTrue(Shadows.shadowOf(service).isForegroundStopped());
            assertTrue(Shadows.shadowOf(manager).getAllNotifications().isEmpty());
        } finally { AdbShareService.end(home.app, home); AdbShareService.end(office.app, office); controller.destroy(); }
    }

    @Test public void notificationRevocationAndServiceDestructionEndAllServerShares() {
        for (boolean revoke : new boolean[] {true, false}) {
            Owner home = new Owner(), office = new Owner();
            home.share.phase = office.share.phase = AdbShare.Phase.ONLINE;
            NotificationManager manager = home.app.getSystemService(NotificationManager.class);
            ServiceController<AdbShareService> controller = Robolectric.buildService(AdbShareService.class).create();
            AdbShareService service = controller.get();
            try {
                service.onStartCommand(home.show(), 0, 1);
                assertTrue(AdbShareService.show(office.app, office, office.share, "Office", "en"));
                if (revoke) {
                    Shadows.shadowOf(manager).setNotificationsEnabled(false);
                    Shadows.shadowOf(Looper.getMainLooper()).idleFor(5, java.util.concurrent.TimeUnit.SECONDS);
                    assertEquals(1, home.notificationFailures); assertEquals(1, office.notificationFailures);
                } else {
                    controller.destroy();
                    assertEquals(1, home.failures); assertEquals(1, office.failures);
                }
                assertTrue(Shadows.shadowOf(manager).getAllNotifications().isEmpty());
            } finally {
                AdbShareService.end(home.app, home); AdbShareService.end(office.app, office);
                if (revoke) controller.destroy();
                Shadows.shadowOf(manager).setNotificationsEnabled(true);
            }
        }
    }

    @Test public void rejectedPromotionAndServiceDestructionNotifyTheCurrentOwner() {
        for (boolean reject : new boolean[] {true, false}) {
            Owner owner = new Owner();
            owner.share.phase = AdbShare.Phase.CONNECTING;
            ServiceController<AdbShareService> controller = Robolectric.buildService(AdbShareService.class).create();
            AdbShareService service = controller.get();
            try {
                if (reject) Shadows.shadowOf(service).setThrowInStartForeground(new SecurityException("fixture rejects promotion"));
                service.onStartCommand(owner.show(), 0, 1);
                assertEquals(reject ? 1 : 0, owner.failures);
            } finally { controller.destroy(); AdbShareService.end(owner.app, owner); }
            assertEquals(1, owner.failures);
        }
    }

    @Test public void blockingTheChannelEndsAnAlreadyOnlineShare() {
        Owner owner = new Owner();
        owner.share.phase = AdbShare.Phase.ONLINE;
        NotificationManager manager = owner.app.getSystemService(NotificationManager.class);
        ServiceController<AdbShareService> controller = Robolectric.buildService(AdbShareService.class).create();
        AdbShareService service = controller.get();
        try {
            service.onStartCommand(owner.show(), 0, 1);
            assertEquals(0, owner.notificationFailures);
            manager.deleteNotificationChannel("adb-share");
            manager.createNotificationChannel(new android.app.NotificationChannel("adb-share", "ADB bridge", NotificationManager.IMPORTANCE_NONE));
            assertFalse(AdbShareService.canNotify(owner.app));
            owner.app.sendBroadcast(new Intent(NotificationManager.ACTION_NOTIFICATION_CHANNEL_BLOCK_STATE_CHANGED)
                    .setPackage(owner.app.getPackageName())
                    .putExtra(NotificationManager.EXTRA_NOTIFICATION_CHANNEL_ID, "adb-share"));
            idle();
            assertEquals(1, owner.notificationFailures);
            assertTrue(Shadows.shadowOf(service).isForegroundStopped());
        } finally { AdbShareService.end(owner.app, owner); controller.destroy(); }
    }

    @Test public void disablingAppNotificationsWithoutABroadcastEndsTheShare() {
        Owner owner = new Owner();
        owner.share.phase = AdbShare.Phase.ONLINE;
        NotificationManager manager = owner.app.getSystemService(NotificationManager.class);
        ServiceController<AdbShareService> controller = Robolectric.buildService(AdbShareService.class).create();
        AdbShareService service = controller.get();
        try {
            service.onStartCommand(owner.show(), 0, 1);
            Shadows.shadowOf(manager).setNotificationsEnabled(false);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(5, java.util.concurrent.TimeUnit.SECONDS);
            assertEquals(1, owner.notificationFailures);
            assertTrue(Shadows.shadowOf(service).isForegroundStopped());
        } finally { AdbShareService.end(owner.app, owner); controller.destroy(); }
    }

    @Test public void disabledNotificationsAreReportedAndUnauthorizedSettingsDoNotOfferSharing() {
        Context context = RuntimeEnvironment.getApplication();
        NotificationManager manager = context.getSystemService(NotificationManager.class);
        Shadows.shadowOf(manager).setNotificationsEnabled(false);
        assertFalse(AdbShareService.canNotify(context));
        Shadows.shadowOf(manager).setNotificationsEnabled(true);
        assertTrue(AdbShareService.canNotify(context));

        Owner owner = new Owner();
        List<Integer> ports = new ArrayList<>();
        NativeAdbSettings view = new NativeAdbSettings(new NativeUi(context, false, "en"), ports::add, owner::stop, (code, port) -> owner.pair(code));
        view.bind(owner.share, "not_authorized", true);
        assertEquals(View.GONE, view.getVisibility());
        view.bind(owner.share, "disabled", true);
        assertEquals(View.VISIBLE, view.getVisibility());
        assertEquals(View.GONE, view.action.getVisibility());
        view.bind(owner.share, "", true);
        view.port.setText("44123");
        view.action.performClick();
        assertEquals(List.of(44123), ports);
        owner.share.phase = AdbShare.Phase.UNPAIRED;
        view.bind(owner.share, "", true);
        assertEquals(View.VISIBLE, view.pairing.getVisibility());
        view.pairingCode.setText("123456");
        view.pair.performClick();
        assertEquals(List.of("123456"), owner.codes);
        owner.share.phase = AdbShare.Phase.PAIRING;
        view.bind(owner.share, "", true);
        assertEquals(View.GONE, view.pairing.getVisibility());
        assertEquals(0, view.pairingCode.length());
        view.action.performClick();
        assertEquals(1, owner.stops);
    }

    @Test public void discoveryIgnoresOtherDevicesAndLateResultsAfterStop() throws Exception {
        Context context = RuntimeEnvironment.getApplication();
        ShadowNsdManager manager = Shadows.shadowOf(context.getSystemService(NsdManager.class));
        AdbDiscovery discovery = new AdbDiscovery(context);
        List<Integer> found = new ArrayList<>();
        try {
            discovery.find(AdbDiscovery.CONNECT, found::add);
            NsdManager.DiscoveryListener scanner = manager.getDiscoveryListeners(AdbDiscovery.CONNECT).get(0);
            NsdServiceInfo remote = service(AdbDiscovery.CONNECT, "remote", "192.0.2.1", 44000);
            NsdServiceInfo local = service(AdbDiscovery.CONNECT, "local", "127.0.0.1", 44001);
            scanner.onServiceFound(remote);
            scanner.onServiceFound(local);
            idle();
            manager.getResolveListeners(remote).get(0).onServiceResolved(remote);
            idle();
            assertTrue(found.isEmpty());
            manager.getResolveListeners(local).get(0).onServiceResolved(local);
            idle();
            assertEquals(List.of(44001), found);
            assertNull(manager.getDiscoveryListenerServiceType(scanner));

            discovery.find(AdbDiscovery.PAIRING, found::add);
            NsdServiceInfo pairing = service(AdbDiscovery.PAIRING, "local", "127.0.0.1", 44002);
            NsdManager.DiscoveryListener pairScanner = manager.getDiscoveryListeners(AdbDiscovery.PAIRING).get(0);
            pairScanner.onServiceFound(pairing);
            idle();
            NsdManager.ResolveListener pending = manager.getResolveListeners(pairing).get(0);
            discovery.close();
            pending.onServiceResolved(pairing);
            idle();
            assertEquals(List.of(44001), found);
            assertNull(manager.getDiscoveryListenerServiceType(pairScanner));
        } finally { discovery.close(); }
    }

    private static NsdServiceInfo service(String type, String name, String host, int port) throws Exception {
        NsdServiceInfo value = new NsdServiceInfo();
        value.setServiceType(type);
        value.setServiceName(name);
        value.setHost(InetAddress.getByName(host));
        value.setPort(port);
        return value;
    }

    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
}
