package com.cloudgeek.spool.android;

/** Persisted routing choice; existing installations retain their embedded node. */
enum ConnectionMode {
    DIRECT("direct", "https"), TAILSCALE("tailscale", "tsnet");

    final String value, directory;

    ConnectionMode(String value, String directory) {
        this.value = value;
        this.directory = directory;
    }

    static ConnectionMode restore(String value, String origin) {
        for (ConnectionMode mode : values()) if (mode.value.equals(value)) return mode;
        return origin.isEmpty() ? DIRECT : TAILSCALE;
    }
}
