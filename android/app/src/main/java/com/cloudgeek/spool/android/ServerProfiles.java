package com.cloudgeek.spool.android;

import java.io.File;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import org.json.JSONArray;
import org.json.JSONObject;

/** Server groups own their network plans, sessions and embedded-node state. */
final class ServerProfiles {
    static final class Server {
        final String id;
        String name;
        ConnectionPlan plan;
        JSONObject account = new JSONObject();
        boolean enabled = true;
        Server(String id, String name, ConnectionPlan plan) { this.id = id; this.name = name; this.plan = plan; }
        String sessionDirectory(File root) {
            return new File(id.equals("legacy") ? root : new File(new File(root, "servers"), id), "session").getAbsolutePath();
        }
        String directory(File root, ConnectionMode mode) {
            return new File(id.equals("legacy") ? root : new File(new File(root, "servers"), id), mode.directory).getAbsolutePath();
        }
    }
    final List<Server> entries = new ArrayList<>();
    String selected;

    ServerProfiles(ConnectionPlan legacy) { entries.add(new Server("legacy", "Spool", legacy)); selected = "legacy"; }

    static ServerProfiles restore(String encoded, String selected, ConnectionPlan legacy) {
        ServerProfiles profiles = new ServerProfiles(legacy);
        if (encoded != null) try {
            JSONArray array = new JSONArray(encoded);
            List<Server> restored = new ArrayList<>();
            for (int i = 0; i < array.length(); i++) {
                JSONObject value = array.getJSONObject(i);
                String id = value.getString("id"), name = value.getString("name").trim();
                if (!id.matches("[A-Za-z0-9_-]{1,64}") || name.isEmpty() || name.length() > 80 || restored.stream().anyMatch(server -> server.id.equals(id))) continue;
                String network = value.optString("selectedNetwork", value.optString("priority"));
                if (!value.has("selectedNetwork") && id.equals(selected)
                        && !value.optString(legacy.selected == ConnectionMode.DIRECT ? "direct" : "tailscale").isEmpty()) network = legacy.selected.value;
                ConnectionPlan plan = new ConnectionPlan(value.optString("direct"), value.optString("tailscale"), value.optString("relay"),
                        ConnectionMode.restore(network, ""), value.optBoolean("automatic", true));
                Server server = new Server(id, name, plan); server.enabled = value.optBoolean("enabled", true);
                server.account = displayAccount(value.optJSONObject("account")); restored.add(server);
            }
            if (array.length() == 0 || !restored.isEmpty()) { profiles.entries.clear(); profiles.entries.addAll(restored); }
        } catch (Exception ignored) { /* Preserve the previous single-server configuration on malformed storage. */ }
        profiles.selected = profiles.entries.isEmpty() ? "" : profiles.entries.get(0).id;
        for (Server server : profiles.entries) if (server.id.equals(selected)) profiles.selected = selected;
        return profiles;
    }

    Server active() { for (Server server : entries) if (server.id.equals(selected)) return server; return entries.isEmpty() ? null : entries.get(0); }
    void remove(String id) {
        entries.removeIf(server -> server.id.equals(id));
        if (selected.equals(id)) selected = entries.isEmpty() ? "" : entries.get(0).id;
    }
    Server add(String name) {
        Server server = new Server(UUID.randomUUID().toString(), name.trim(), new ConnectionPlan("", "", ConnectionMode.DIRECT, true));
        entries.add(server); return server;
    }
    /** Display-only identity; cached profile data never authenticates a connection. */
    static JSONObject displayAccount(JSONObject user) {
        JSONObject result = new JSONObject();
        if (user == null || user.optString("id").isEmpty()) return result;
        try {
            for (String field : new String[] {"id", "username", "nickname"}) if (user.has(field)) result.put(field, user.optString(field));
            JSONObject avatar = user.optJSONObject("avatar");
            if (avatar != null) result.put("avatar", new JSONObject().put("style", avatar.optString("style")).put("seed", avatar.optString("seed")));
        } catch (Exception error) { throw new IllegalArgumentException(error); }
        return result;
    }
    String encode() {
        JSONArray array = new JSONArray();
        try { for (Server server : entries) array.put(new JSONObject().put("id", server.id).put("name", server.name)
                .put("direct", server.plan.direct).put("tailscale", server.plan.tailscale).put("relay", server.plan.relay).put("selectedNetwork", server.plan.selected.value).put("automatic", server.plan.autoSwitch).put("enabled", server.enabled).put("account", displayAccount(server.account))); }
        catch (Exception error) { throw new IllegalArgumentException(error); }
        return array.toString();
    }
}
