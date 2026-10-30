package com.cloudgeek.spool.android;

import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.Build;
import java.net.NetworkInterface;
import java.util.HashSet;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;

final class NetworkSnapshot {
    static String read(ConnectivityManager connectivity) throws Exception {
        JSONArray result = new JSONArray();
        Set<String> seen = new HashSet<>();
        for (Network network : connectivity.getAllNetworks()) {
            NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
            LinkProperties properties = connectivity.getLinkProperties(network);
            if (capabilities == null || properties == null
                    || !capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    || capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue;
            String name = properties.getInterfaceName();
            if (name == null || !seen.add(name)) continue;
            NetworkInterface device = NetworkInterface.getByName(name);
            if (device == null || device.getIndex() < 1) continue;
            JSONArray addresses = new JSONArray();
            for (LinkAddress address : properties.getLinkAddresses()) {
                // Link-local IPv6 addresses carry a Java scope suffix; CIDR uses the plain address.
                String ip = address.getAddress().getHostAddress().split("%", 2)[0];
                addresses.put(ip + "/" + address.getPrefixLength());
            }
            JSONObject entry = new JSONObject();
            entry.put("name", name);
            entry.put("index", device.getIndex());
            int mtu = Build.VERSION.SDK_INT >= 29 ? properties.getMtu() : device.getMTU();
            entry.put("mtu", mtu >= 576 ? mtu : 1500);
            entry.put("addresses", addresses);
            result.put(entry);
        }
        return result.toString();
    }
}
