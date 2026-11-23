package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONException;
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
        final ConnectionMode clientMode;
        Target(String id, String name, String origin) { this(id, name, origin, ConnectionMode.DIRECT); }
        Target(String id, String name, String origin, ConnectionMode clientMode) {
            this.id = id; this.name = name; this.origin = origin; this.clientMode = clientMode;
        }

        ConnectionPlan newPlan() {
            if (origin.isEmpty() || clientMode == null) return null;
            return clientMode == ConnectionMode.TAILSCALE ? new ConnectionPlan("", origin, clientMode)
                    : new ConnectionPlan(origin, "", clientMode);
        }
    }

    /**
     * One entry in the server tabs: a saved profile the source trusts, or a target not saved yet.
     * Every authorized target is listed; one without a saved route or usable address still appears.
     */
    static final class Tab {
        final Target target;
        final ServerProfiles.Server source, server, route;
        Tab(ServerProfiles.Server source, Target target, ServerProfiles.Server server, ServerProfiles.Server route) {
            this.source = source; this.target = target; this.server = server; this.route = route;
        }
        String key() { return tabKey(source.id, target.id); }
        boolean reachable() { return route != null || target.newPlan() != null; }
    }

    private NativeFederation() {}

    static String tabKey(String source, String target) { return PREFIX + source + ":" + target; }

    static String accountKey(String account, String csrf) {
        try {
            byte[] bytes = java.security.MessageDigest.getInstance("SHA-256").digest((account + "\0" + csrf).getBytes(java.nio.charset.StandardCharsets.UTF_8));
            return android.util.Base64.encodeToString(bytes, android.util.Base64.URL_SAFE | android.util.Base64.NO_WRAP | android.util.Base64.NO_PADDING);
        } catch (java.security.NoSuchAlgorithmException error) { throw new IllegalStateException(error); }
    }

    static List<Target> parse(Object value) {
        List<Target> result = new ArrayList<>();
        if (!(value instanceof JSONArray)) return result;
        JSONArray array = (JSONArray) value;
        for (int i = 0; i < array.length() && result.size() < 64; i++) {
            JSONObject entry = array.optJSONObject(i);
            if (entry == null) continue;
            String id = entry.optString("id"), name = entry.optString("name").trim();
            if (!validID(id) || result.stream().anyMatch(target -> target.id.equals(id))) continue;
            if (name.isEmpty()) name = id.substring(0, 8);
            if (name.length() > 80) name = name.substring(0, 80);
            result.add(new Target(id, name, origin(entry.optString("client_url")), clientMode(entry.opt("client_mode"))));
        }
        return result;
    }

    private static ConnectionMode clientMode(Object mode) {
        if (mode == null || "".equals(mode)) return ConnectionMode.DIRECT;
        return ConnectionMode.TAILSCALE.value.equals(mode) ? ConnectionMode.TAILSCALE : null;
    }

    static boolean validID(String id) { return id.matches("[a-z2-7]{52}"); }

    /** Unlike tab rendering, automatic sign-in must distinguish a malformed list from a denial. */
    static boolean authorizedRoute(JSONArray peers, String id, ConnectionMode mode, String address,
                                   boolean requirePublishedMode) throws JSONException {
        Set<String> seen = new HashSet<>();
        boolean authorized = false;
        for (int i = 0; i < peers.length(); i++) {
            JSONObject peer = peers.getJSONObject(i);
            Object key = peer.opt("id"), url = peer.opt("client_url"), advertised = peer.opt("client_mode");
            if (!(key instanceof String) || !validID((String) key) || !seen.add((String) key)
                    || url != null && !(url instanceof String) || advertised != null && !(advertised instanceof String))
                throw new JSONException("Invalid trusted server list");
            if (id.equals(key)) authorized = (!requirePublishedMode || clientMode(advertised) == mode)
                    && allows(new Target(id, "", origin((String) url)), mode, address);
        }
        return authorized;
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

    static List<Tab> tabs(ServerProfiles profiles, ServerProfiles.Server source, List<Target> targets) {
        return tabs(profiles, source, targets, "");
    }

    static List<Tab> tabs(ServerProfiles profiles, ServerProfiles.Server source, List<Target> targets, String account) {
        List<Tab> result = new ArrayList<>();
        for (Target target : targets) {
            if (route(source, target, null) != null) continue;
            ServerProfiles.Server saved = null, connection = null;
            for (ServerProfiles.Server server : profiles.entries) {
                if (server.federationSource.equals(source.id) && server.federationTarget.equals(target.id)
                        && (account.isEmpty() || server.federationAccount.equals(account))) saved = server;
                if (route(server, target, null) != null && (connection == null || connection.trusted() && !server.trusted()
                        || !connection.enabled && server.enabled)) connection = server;
            }
            result.add(new Tab(source, target, saved, connection));
        }
        return result;
    }

    static Tab find(List<Tab> tabs, String key) {
        for (Tab tab : tabs) if (tab.key().equals(key)) return tab;
        return null;
    }
}
