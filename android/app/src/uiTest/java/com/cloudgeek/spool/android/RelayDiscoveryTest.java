package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.os.Looper;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.List;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {RelayDiscoveryTest.ShadowNsd.class})
@LooperMode(LooperMode.Mode.PAUSED)
public class RelayDiscoveryTest {
    @Implements(NsdManager.class)
    public static class ShadowNsd {
        static NsdManager.DiscoveryListener discovery;
        static NsdManager.ResolveListener resolver;
        static String type;
        static int stops;
        // Android 8 otherwise attempts to bind its real system discovery daemon.
        @Implementation(maxSdk = 26) protected void init() {}
        @Implementation protected void discoverServices(String serviceType, int protocol, NsdManager.DiscoveryListener listener) {
            type = serviceType; discovery = listener;
        }
        @Implementation protected void stopServiceDiscovery(NsdManager.DiscoveryListener listener) { stops++; }
        @Implementation protected void resolveService(NsdServiceInfo service, NsdManager.ResolveListener listener) { resolver = listener; }
    }
    @Before public void resetDiscovery() {
        ShadowNsd.discovery = null; ShadowNsd.resolver = null;
        ShadowNsd.type = null; ShadowNsd.stops = 0;
    }
    private NsdServiceInfo service(String id, String ip) throws Exception {
        NsdServiceInfo service = new NsdServiceInfo();
        service.setServiceName("Spool-test"); service.setServiceType("_spool._tcp.");
        service.setAttribute("id", id); service.setHost(InetAddress.getByName(ip)); service.setPort(28082);
        return service;
    }
    @Test public void resolvesPairedLocalCandidatesAndDropsCallbacksAfterStop() throws Exception {
        List<String> found = new ArrayList<>();
        String id = new RelayPairing(RelayPairingTest.code("https://relay.example.com")).id;
        RelayDiscovery discovery = new RelayDiscovery(RuntimeEnvironment.getApplication(), (key, address) -> found.add(key + "=" + address));
        discovery.refresh(); assertEquals("_spool._tcp.", ShadowNsd.type);
        NsdServiceInfo info = service(id, "192.168.1.20");
        ShadowNsd.discovery.onServiceFound(info); Shadows.shadowOf(Looper.getMainLooper()).idle();
        ShadowNsd.resolver.onServiceResolved(info); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(List.of(id + "=192.168.1.20:28082"), found);
        assertEquals("192.168.1.20:28082", discovery.addresses(id));
        NsdManager.ResolveListener stale = ShadowNsd.resolver;
        discovery.close(); assertEquals("", discovery.addresses(id));
        stale.onServiceResolved(info); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, found.size()); assertTrue(ShadowNsd.stops > 0);
    }
    @Test public void publicAddressesAndUnpairedIdentityFormatsAreNotLANHints() throws Exception {
        List<String> found = new ArrayList<>();
        RelayDiscovery discovery = new RelayDiscovery(RuntimeEnvironment.getApplication(), (id, address) -> found.add(address));
        discovery.refresh();
        String id = new RelayPairing(RelayPairingTest.code("https://relay.example.com")).id;
        for (NsdServiceInfo info : List.of(service(id, "8.8.8.8"), service("untrusted", "192.168.1.20"))) {
            ShadowNsd.discovery.onServiceFound(info); Shadows.shadowOf(Looper.getMainLooper()).idle();
            ShadowNsd.resolver.onServiceResolved(info); Shadows.shadowOf(Looper.getMainLooper()).idle();
        }
        assertTrue(found.isEmpty()); discovery.close();
    }
}
