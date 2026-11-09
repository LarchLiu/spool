package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Objects;

/** Both server entries and the user's saved network choice. Empty entries are optional. */
final class ConnectionPlan {
    static final class Endpoint {
        final ConnectionMode mode;
        final String origin;
        Endpoint(ConnectionMode mode, String origin) { this.mode = mode; this.origin = origin; }
        @Override public boolean equals(Object other) {
            return other instanceof Endpoint && mode == ((Endpoint) other).mode && origin.equals(((Endpoint) other).origin);
        }
        @Override public int hashCode() { return Objects.hash(mode, origin); }
    }

    final String direct, tailscale, relay;
    final ConnectionMode selected;
    final boolean autoSwitch;

    ConnectionPlan(String direct, String tailscale, ConnectionMode selected) {
        this(direct, tailscale, selected, false);
    }

    ConnectionPlan(String direct, String tailscale, ConnectionMode selected, boolean autoSwitch) {
        this(direct, tailscale, "", selected, autoSwitch);
    }

    ConnectionPlan(String direct, String tailscale, String relay, ConnectionMode selected, boolean autoSwitch) {
        this.direct = direct.trim(); this.tailscale = tailscale.trim(); this.relay = relay.trim(); this.selected = selected; this.autoSwitch = autoSwitch;
    }

    static ConnectionPlan restore(String direct, String tailscale, String selection, String legacyOrigin, String legacyMode) {
        if (direct == null && tailscale == null) {
            ConnectionMode mode = ConnectionMode.restore(legacyMode, legacyOrigin);
            return new ConnectionPlan(mode == ConnectionMode.DIRECT ? legacyOrigin : "",
                    mode == ConnectionMode.TAILSCALE ? legacyOrigin : "", mode == ConnectionMode.RELAY ? legacyOrigin : "", mode, false);
        }
        return new ConnectionPlan(direct == null ? "" : direct, tailscale == null ? "" : tailscale,
                ConnectionMode.restore(selection, ""));
    }

    String origin(ConnectionMode mode) { return mode == ConnectionMode.DIRECT ? direct : mode == ConnectionMode.TAILSCALE ? tailscale : relay; }

    ConnectionPlan automatic(boolean value) { return new ConnectionPlan(direct, tailscale, relay, selected, value); }

    ConnectionPlan select(ConnectionMode mode) { return new ConnectionPlan(direct, tailscale, relay, mode, autoSwitch); }

    List<Endpoint> candidates(ConnectionMode preferred) {
        List<Endpoint> entries = ordered(preferred);
        return autoSwitch && preferred == null || entries.isEmpty() ? entries : new ArrayList<>(entries.subList(0, 1));
    }

    List<Endpoint> ordered(ConnectionMode preferred) {
        List<Endpoint> result = new ArrayList<>();
        ConnectionMode start = preferred == null ? selected : preferred;
        List<ConnectionMode> order = new ArrayList<>(); order.add(start);
        for (ConnectionMode mode : ConnectionMode.values()) if (mode != start) order.add(mode);
        for (ConnectionMode mode : order)
            if (!origin(mode).isEmpty()) result.add(new Endpoint(mode, origin(mode)));
        return result;
    }

    ConnectionPlan validated() {
        return new ConnectionPlan(direct.isEmpty() ? "" : new OriginPolicy(direct).origin,
                tailscale.isEmpty() ? "" : new OriginPolicy(tailscale).origin,
                relay.isEmpty() || selected != ConnectionMode.RELAY ? relay : new RelayPairing(relay).code, selected, autoSwitch);
    }
}
