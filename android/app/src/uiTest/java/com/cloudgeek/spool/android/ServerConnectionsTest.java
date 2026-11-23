package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;

public class ServerConnectionsTest {
    @Test public void independentLoginsLeaseOneDeviceUntilTheLastSessionCloses() throws Exception {
        ServerConnections<Object> connections = new ServerConnections<>();
        List<Object> opened = new ArrayList<>(), closed = new ArrayList<>();
        ServerConnections.Factory<Object> factory = new ServerConnections.Factory<>() {
            public Object device() { Object value = new Object(); opened.add(value); return value; }
            public Object session(Object device) { assertSame(opened.get(0), device); return new Object(); }
            public void close(Object client) { closed.add(client); }
        };
        Object ordinary = connections.open("device/https", factory), a = connections.open("device/https", factory), d = connections.open("device/https", factory);
        assertEquals(1, opened.size()); assertNotSame(ordinary, a); assertNotSame(a, d);
        connections.close(a); connections.close(a);
        assertEquals(List.of(a), closed);
        connections.close(ordinary); assertEquals(List.of(a, ordinary), closed);
        connections.close(d); assertEquals(List.of(a, ordinary, d, opened.get(0)), closed);
    }

    @Test public void failedSessionReleasesOnlyAnOtherwiseUnusedDevice() throws Exception {
        ServerConnections<Object> connections = new ServerConnections<>();
        List<Object> closed = new ArrayList<>(); Object device = new Object();
        ServerConnections.Factory<Object> broken = new ServerConnections.Factory<>() {
            public Object device() { return device; }
            public Object session(Object client) throws Exception { throw new java.io.IOException("invalid session directory"); }
            public void close(Object client) { closed.add(client); }
        };
        assertThrows(java.io.IOException.class, () -> connections.open("device", broken));
        assertEquals(List.of(device), closed);
    }
}
