package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkInfo;
import java.net.InetAddress;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.shadows.ShadowConnectivityManager;
import org.robolectric.shadows.ShadowNetworkCapabilities;
import org.robolectric.shadows.ShadowNetworkInfo;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NetworkSnapshotTest {
    @Test public void directUsesTheActiveSystemVPNWhileTailscaleWaitsForAnUnderlyingNetwork() throws Exception {
        ConnectivityManager connectivity = (ConnectivityManager) RuntimeEnvironment.getApplication().getSystemService(Context.CONNECTIVITY_SERVICE);
        ShadowConnectivityManager shadow = Shadows.shadowOf(connectivity);
        shadow.clearAllNetworks();
        shadow.setActiveNetworkInfo(ShadowNetworkInfo.newInstance(NetworkInfo.DetailedState.CONNECTED,
                ConnectivityManager.TYPE_VPN, 0, true, true));
        Network active = connectivity.getActiveNetwork();
        assertNotNull(active);
        NetworkCapabilities capabilities = ShadowNetworkCapabilities.newInstance();
        Shadows.shadowOf(capabilities).addTransportType(NetworkCapabilities.TRANSPORT_VPN);
        Shadows.shadowOf(capabilities).addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET);
        shadow.setNetworkCapabilities(active, capabilities);
        LinkProperties properties = new LinkProperties();
        LinkAddress address = ReflectionHelpers.callConstructor(LinkAddress.class,
                ReflectionHelpers.ClassParameter.from(InetAddress.class, InetAddress.getByAddress(new byte[] {10, 8, 0, 2})),
                ReflectionHelpers.ClassParameter.from(int.class, 24));
        ReflectionHelpers.callInstanceMethod(properties, "addLinkAddress",
                ReflectionHelpers.ClassParameter.from(LinkAddress.class, address));
        shadow.setLinkProperties(active, properties);
        assertTrue(NetworkSnapshot.online(connectivity, ConnectionMode.DIRECT));
        assertTrue(NetworkSnapshot.online(connectivity, ConnectionMode.RELAY));
        assertFalse(NetworkSnapshot.online(connectivity, ConnectionMode.TAILSCALE));
        Shadows.shadowOf(capabilities).removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET);
        assertFalse(NetworkSnapshot.online(connectivity, ConnectionMode.DIRECT));
        assertTrue("paired LAN remains usable without an Internet capability", NetworkSnapshot.online(connectivity, ConnectionMode.RELAY));
        shadow.setActiveNetworkInfo(null);
        assertFalse(NetworkSnapshot.online(connectivity, ConnectionMode.DIRECT));
        assertFalse(NetworkSnapshot.online(connectivity, ConnectionMode.RELAY));
    }
}
