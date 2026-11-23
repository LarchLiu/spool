package com.cloudgeek.spool.android;

import java.util.HashMap;
import java.util.IdentityHashMap;
import java.util.Map;

/** Used only on the serial connection worker. Device leases outlive their independent login views. */
final class ServerConnections<T> {
    interface Factory<T> {
        T device() throws Exception;
        T session(T device) throws Exception;
        void close(T client);
    }
    private static final class Device<T> {
        final T client;
        final Factory<T> factory;
        int leases;
        Device(T client, Factory<T> factory) { this.client = client; this.factory = factory; }
    }
    private final Map<String, Device<T>> devices = new HashMap<>();
    private final Map<T, String> sessions = new IdentityHashMap<>();

    T open(String key, Factory<T> factory) throws Exception {
        Device<T> device = devices.get(key);
        if (device == null) {
            device = new Device<>(factory.device(), factory);
            devices.put(key, device);
        }
        try {
            T session = factory.session(device.client);
            sessions.put(session, key); ++device.leases;
            return session;
        } catch (Exception error) {
            if (device.leases == 0) { devices.remove(key); device.factory.close(device.client); }
            throw error;
        }
    }

    void close(T session) {
        String key = sessions.remove(session);
        if (key == null) return;
        Device<T> device = devices.get(key);
        device.factory.close(session);
        if (--device.leases == 0) { devices.remove(key); device.factory.close(device.client); }
    }
}
