package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * Servers that the signed-in source server lets this account enter. The list
 * only selects where a one-time ticket may be sent; the target still decides.
 */
final class NativeFederation {
    static final String PREFIX = "federation:";

    static final class Target {
        final String id, name;
        /** Normalized HTTPS origin supplied by the source administrator, or empty. */
        final String origin;
        Target(String id, String name, String origin) { this.id = id; this.name = name; this.origin = origin; }
    }

    /**
     * One entry in the server tabs: a saved profile the source trusts, or a target not saved yet.
     * Every authorized target is listed; one without a saved route or usable address still appears.
     */
    static final class Tab {
        final Target target;
        final ServerProfiles.Server server;
        Tab(Target target, ServerProfiles.Server server) { this.target = target; this.server = server; }
        String key() { return server == null ? PREFIX + target.id : server.id; }
        boolean reachable() { return server != null || !target.origin.isEmpty(); }
    }

    private NativeFederation() {}

    static List<Target> parse(Object value) {
        List<Target> result = new ArrayList<>();
        if (!(value instanceof JSONArray)) return result;
        JSONArray array = (JSONArray) value;
        for (int i = 0; i < array.length() && result.size() < 64; i++) {
            JSONObject entry = array.optJSONObject(i);
            if (entry == null) continue;
            String id = entry.optString("id"), name = entry.optString("name").trim();
            if (!id.matches("[a-z2-7]{52}") || result.stream().anyMatch(target -> target.id.equals(id))) continue;
            if (name.isEmpty()) name = id.substring(0, 8);
            if (name.length() > 80) name = name.substring(0, 80);
            result.add(new Target(id, name, origin(entry.optString("client_url"))));
        }
        return result;
    }

    /** Native transports address one HTTPS origin; URLs with a path (Relay Web) are not usable here. */
    static String origin(String address) {
        if (address == null || address.trim().isEmpty()) return "";
        try { return new OriginPolicy(address).origin; } catch (RuntimeException invalid) { return ""; }
    }

    /**
     * The route on which a ticket for this target may be sent, or null. HTTPS and
     * Tailscale must be the exact origin the source listed; a server's own
     * identity claim never selects a destination. Relay is pinned to the paired key.
     */
    static ConnectionMode route(ServerProfiles.Server server, Target target, ConnectionMode preferred) {
        List<ConnectionMode> order = new ArrayList<>();
        if (preferred != null) order.add(preferred);
        for (ConnectionMode mode : ConnectionMode.values()) if (mode != preferred) order.add(mode);
        for (ConnectionMode mode : order) if (allows(target, mode, server.plan.origin(mode))) return mode;
        return null;
    }

    static boolean allows(Target target, ConnectionMode mode, String address) {
        if (address == null || address.isEmpty()) return false;
        if (mode == ConnectionMode.RELAY) {
            try { return new RelayPairing(address).id.equals(target.id); } catch (RuntimeException invalid) { return false; }
        }
        return !target.origin.isEmpty() && target.origin.equals(origin(address));
    }

    static List<Tab> tabs(ServerProfiles profiles, List<Target> targets) {
        List<Tab> result = new ArrayList<>();
        for (Target target : targets) {
            ServerProfiles.Server saved = null;
            for (ServerProfiles.Server server : profiles.entries)
                if (saved == null && route(server, target, null) != null) saved = server;
            if (saved != null && saved.id.equals(profiles.selected)) continue;
            ServerProfiles.Server match = saved;
            if (saved != null && result.stream().anyMatch(tab -> tab.server == match)) continue;
            result.add(new Tab(target, saved));
        }
        return result;
    }

    static Tab find(List<Tab> tabs, String key) {
        for (Tab tab : tabs) if (tab.key().equals(key)) return tab;
        return null;
    }
}
