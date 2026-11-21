package com.cloudgeek.spool.android;

import static com.cloudgeek.spool.android.AdbShare.Phase.*;
import static org.junit.Assert.*;

import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import java.util.function.IntConsumer;
import org.junit.Test;

public class AdbShareTest {
    private static final class Wire implements AdbShare.Link {
        final int port;
        final AdbShare.Events events;
        boolean closed;
        Wire(int port, AdbShare.Events events) { this.port = port; this.events = events; }
        void event(String kind, String value) { events.event(kind, value, ""); }
        @Override public void close() { closed = true; }
    }

    private static final class Host implements AdbShare.Host {
        Runnable changed = () -> {};
        final AdbShare share = new AdbShare(this, () -> changed.run());
        final List<Wire> shares = new ArrayList<>(), pairs = new ArrayList<>();
        IntConsumer discovery;
        Consumer<String> availability;
        Runnable retry;
        long now, delay;
        boolean pairing;
        @Override public void port(boolean forPairing, IntConsumer found) { pairing = forPairing; discovery = found; }
        @Override public void cancelDiscovery() { discovery = null; }
        @Override public AdbShare.Link share(int port, AdbShare.Events events) {
            Wire wire = new Wire(port, events); shares.add(wire); return wire;
        }
        @Override public AdbShare.Link pair(String code, int port, AdbShare.Events events) {
            assertEquals("012345", code);
            Wire wire = new Wire(port, events); pairs.add(wire); return wire;
        }
        @Override public void availability(Consumer<String> result) { availability = result; }
        @Override public void later(Runnable task, long millis) { retry = task; delay = millis; }
        @Override public void cancel(Runnable task) { retry = null; }
        @Override public long now() { return now; }
        @Override public double random() { return 0.5; }
        void found(int port) { IntConsumer result = discovery; discovery = null; result.accept(port); }
    }

    @Test public void stoppingCancelsDiscoveryAndLateEventsCannotChangeTheNewShare() {
        Host h = new Host();
        h.share.start(0);
        IntConsumer oldDiscovery = h.discovery;
        h.share.stop();
        assertNull(h.discovery);
        h.share.start(10001);
        oldDiscovery.accept(10000);
        assertEquals(1, h.shares.size());
        Wire first = h.shares.get(0);
        h.share.stop();
        assertTrue(first.closed);
        h.share.start(10002);
        first.event("closed", "revoked");
        first.event("ready", "old serial");
        assertEquals(CONNECTING, h.share.phase);
        assertEquals("", h.share.serial);
        Wire current = h.shares.get(1);
        current.event("ready", "127.0.0.1:20000");
        current.event("state", "online");
        assertEquals(ONLINE, h.share.phase);
        assertEquals("127.0.0.1:20000", h.share.serial);
    }

    @Test public void retryChecksAccessBeforeLookingForThePhoneAgain() {
        Host h = new Host();
        h.share.start(0);
        h.found(10000);
        Wire first = h.shares.get(0);
        first.event("state", "online");
        first.event("closed", "network");
        assertEquals(WAITING, h.share.phase);
        assertEquals(1000, h.delay);
        assertTrue(first.closed);
        h.retry.run();
        h.availability.accept(null);
        assertEquals(2000, h.delay);
        h.retry.run();
        h.availability.accept("");
        h.found(10001);
        assertEquals(10001, h.shares.get(1).port);
        h.shares.get(1).event("closed", "network");
        h.retry.run();
        h.availability.accept("disabled");
        assertEquals(FAILED, h.share.phase);
        assertEquals("disabled", h.share.code);
        assertNull(h.retry);
        assertNull(h.discovery);
        assertEquals(2, h.shares.size());
    }

    @Test public void pairingRetriesOnlyRecoverableFailuresAndDoesNotReconnectAfterRevocation() {
        for (String reason : List.of("revoked", "forbidden", "auth_expired", "unavailable")) {
            Host h = new Host();
            h.share.start(10000);
            h.shares.get(0).event("closed", "unpaired");
            h.share.pair("123", 0);
            assertEquals(UNPAIRED, h.share.phase);
            assertNull(h.discovery);
            h.share.pair(" 012345 ", 0);
            assertTrue(h.pairing);
            h.found(10001);
            Wire first = h.pairs.get(0);
            first.event("closed", "device_unreachable");
            assertEquals(UNPAIRED, h.share.phase);
            assertEquals("pairing_unreachable", h.share.code);
            assertTrue(first.closed);
            h.share.pair("012345", 10002);
            Wire second = h.pairs.get(1);
            second.event("closed", reason);
            second.event("paired", "");
            assertTrue(second.closed);
            assertEquals(FAILED, h.share.phase);
            assertEquals(reason, h.share.code);
            assertEquals(1, h.shares.size());
        }
    }

    @Test public void pairingSuccessOpensTheConnectPortAgain() {
        Host h = new Host();
        h.share.start(0);
        h.found(10000);
        h.shares.get(0).event("closed", "unpaired");
        h.share.pair("012345", 10001);
        h.pairs.get(0).event("paired", "");
        assertTrue(h.pairs.get(0).closed);
        assertFalse(h.pairing);
        h.found(10002);
        assertEquals(10002, h.shares.get(1).port);
    }

    @Test public void losingNotificationsClosesAnOnlineShareWithoutRetry() {
        Host h = new Host();
        h.share.start(10000);
        Wire wire = h.shares.get(0);
        wire.event("state", "online");
        h.share.notificationUnavailable();
        assertTrue(wire.closed);
        assertEquals(FAILED, h.share.phase);
        assertEquals("notification_unavailable", h.share.code);
        assertNull(h.retry);
    }

    @Test public void foregroundStartFailureDoesNotOpenAConnectionAndLossClosesOne() {
        Host h = new Host();
        h.changed = h.share::foregroundUnavailable;
        h.share.start(10000);
        assertEquals(FAILED, h.share.phase);
        assertEquals("foreground_unavailable", h.share.code);
        assertTrue(h.shares.isEmpty());
        h.changed = () -> {};
        h.share.start(10000);
        Wire wire = h.shares.get(0);
        wire.event("state", "online");
        h.share.foregroundUnavailable();
        assertTrue(wire.closed);
        assertEquals(FAILED, h.share.phase);
        assertNull(h.retry);
    }
}
