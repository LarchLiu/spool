package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import com.google.gson.*;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.concurrent.Executor;
import org.junit.Test;

public class NativeWorkspaceEventsTest {
    static final class Queue implements Executor {
        final List<Runnable> tasks = new ArrayList<>();
        public void execute(Runnable task) { tasks.add(task); }
        void flush() { while (!tasks.isEmpty()) tasks.remove(0).run(); }
    }
    static final class Timer {
        final Runnable task; final long at;
        Timer(Runnable task, long at) { this.task = task; this.at = at; }
    }
    static final class Clock implements NativeWorkspaceEvents.Scheduler {
        final List<Runnable> tasks = new ArrayList<>(); final List<Timer> timers = new ArrayList<>();
        long now = 100000; double random = .5;
        public void post(Runnable task) { tasks.add(task); }
        public void postDelayed(Runnable task, long millis) { timers.add(new Timer(task, now + millis)); }
        public void remove(Runnable task) { timers.removeIf(timer -> timer.task == task); }
        public long now() { return now; }
        public double random() { return random; }
        void flush() { while (!tasks.isEmpty()) tasks.remove(0).run(); }
    }
    static final class Connection {
        final NativeWorkspaceEvents.Events events; boolean closed;
        Connection(NativeWorkspaceEvents.Events events) { this.events = events; }
    }
    static final class Source implements NativeWorkspaceEvents.Source<Connection> {
        final List<Connection> connections = new ArrayList<>(); final JsonArray controls = new JsonArray();
        final List<Boolean> visibility = new ArrayList<>();
        boolean blocked, failSend, failProbe; int auth, probes;
        Exception authFailure;
        java.util.function.Consumer<NativeWorkspaceEvents.Events> opening = events -> {};
        public Connection open(NativeWorkspaceEvents.Events events) throws Exception {
            if (blocked) throw new Exception("Blocked");
            Connection connection = new Connection(events); connections.add(connection); opening.accept(events); return connection;
        }
        public void close(Connection connection) { if (connection != null) connection.closed = true; }
        public void send(Connection connection, String type, boolean visible) throws Exception {
            if (failSend) throw new Exception("Send failed"); controls.add(type); visibility.add(visible);
        }
        public void probe(Connection connection) throws Exception {
            probes++;
            if (failProbe) throw new Exception("No pong");
        }
        public void checkAuth() throws Exception { auth++; if (authFailure != null) throw authFailure; }
        Connection current() { return connections.get(connections.size() - 1); }
    }
    static final class Sink implements NativeWorkspaceEvents.Listener<Connection> {
        Boolean connected; int expired, failures; JsonElement presence = new JsonArray(); final JsonArray received = new JsonArray();
        final List<String> authErrors = new ArrayList<>();
        public void connection(Connection value) {}
        public void event(String kind, String value) {
            if (kind.equals("presence")) presence = JsonParser.parseString(value);
            else if (List.of("navigation", "agents", "profiles").contains(kind)) {
                JsonArray event = new JsonArray(); event.add(kind); event.add(JsonParser.parseString(value)); received.add(event);
            }
        }
        public void connected(boolean value) { connected = value; if (!value) presence = new JsonArray(); }
        public void expired() { expired++; }
        public void authFailed(Exception error) { authErrors.add(error.getMessage()); }
        public void failed() { failures++; }
    }
    static final class Harness {
        final Queue worker = new Queue(); final Clock clock = new Clock(); final Source source = new Source(); final Sink sink = new Sink();
        final NativeWorkspaceEvents<Connection> stream = new NativeWorkspaceEvents<>(worker, clock);
        void bind(String key) { stream.bind(key, source, sink); flush(); }
        void flush() { while (!worker.tasks.isEmpty() || !clock.tasks.isEmpty()) { worker.flush(); clock.flush(); } }
        void event(String kind, String value) { source.current().events.event(kind, value); flush(); }
        void advance(long milliseconds) {
            long target = clock.now + milliseconds;
            while (true) {
                Timer next = clock.timers.stream().filter(timer -> timer.at <= target).min(Comparator.comparingLong(timer -> timer.at)).orElse(null);
                if (next == null) break;
                clock.now = next.at; clock.timers.remove(next); next.task.run(); flush();
            }
            clock.now = target;
        }
        JsonObject state() {
            JsonObject state = new JsonObject(); state.addProperty("connections", source.connections.size());
            if (sink.connected == null) state.add("connected", JsonNull.INSTANCE); else state.addProperty("connected", sink.connected);
            state.add("presence", sink.presence); state.addProperty("expired", sink.expired); state.addProperty("authRequests", source.auth);
            state.add("received", sink.received); state.add("controls", source.controls);
            JsonArray timers = new JsonArray(); clock.timers.stream().mapToLong(timer -> timer.at - clock.now).sorted().forEach(timers::add); state.add("timers", timers);
            return state;
        }
    }

    @Test public void productionPolicyMatchesAllActualWebLifecycleAndJitterStates() throws Exception {
        JsonArray cases;
        try (InputStream input = getClass().getResourceAsStream("/web-workspace-events.json")) {
            assertNotNull(input); cases = JsonParser.parseString(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getAsJsonObject().getAsJsonArray("cases");
        }
        assertEquals(21, cases.size()); int states = 0;
        for (JsonElement value : cases) {
            JsonObject item = value.getAsJsonObject(); Harness h = new Harness();
            h.clock.random = item.get("random").getAsDouble(); h.source.blocked = item.get("blocked").getAsBoolean(); h.bind("session");
            for (JsonElement record : item.getAsJsonArray("records")) {
                JsonObject step = record.getAsJsonObject(); String type = step.get("type").getAsString();
                switch (type) {
                    case "initial": case "open": break; // Go owns hello and transport open.
                    case "block": h.source.blocked = step.get("value").getAsBoolean(); break;
                    case "advance": h.advance(step.get("value").getAsLong()); break;
                    case "error": h.event("transport_error", "{}"); break;
                    case "old-error": h.source.connections.get(0).events.event("transport_error", "{}"); break;
                    case "policy": h.event("auth_expired", "{}"); break;
                    case "resume": h.stream.resume(); break;
                    case "frame": h.event(step.get("kind").getAsString(), step.get("value").toString()); break;
                    case "old-frame": h.source.connections.get(0).events.event(step.get("kind").getAsString(), step.get("value").toString()); break;
                    default: fail(type);
                }
                h.flush(); states++;
                assertEquals(item.get("id") + "/" + h.clock.random + "/" + type, step.get("expected"), h.state());
            }
            h.stream.stop(); h.flush(); assertTrue(h.clock.timers.isEmpty());
        }
        assertEquals(234, states);
    }

    @Test public void pendingAuthChecksCoalesceAcrossRepeatedSocketFailures() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        h.source.current().events.event("transport_error", "{}"); h.clock.flush(); assertEquals(1, h.worker.tasks.size());
        Timer retry = h.clock.timers.remove(0); h.clock.now = retry.at; retry.task.run();
        h.worker.tasks.remove(1).run(); h.clock.flush();
        h.source.current().events.event("transport_error", "{}"); h.clock.flush(); assertEquals(1, h.worker.tasks.size());
        h.flush(); assertEquals(1, h.source.auth); h.stream.stop();
    }

    @Test public void displayHandoffKeepsTheSocketQueuedFramesAndRetryLifecycle() {
        Harness h = new Harness(); h.bind("shown"); h.event("ready", "true");
        Connection live = h.source.current(); Sink hidden = new Sink();
        live.events.event("navigation", "{\"projects\":[]}");
        int before = h.sink.received.size();
        assertTrue(h.stream.handoff("hidden", hidden)); h.stream.visibility(false); h.flush();
        assertFalse(live.closed); assertEquals(1, h.source.connections.size()); assertTrue(h.stream.ready());
        assertEquals(before, h.sink.received.size()); assertEquals(1, hidden.received.size());
        assertEquals(Boolean.FALSE, h.source.visibility.get(h.source.visibility.size() - 1));
        Sink shownAgain = new Sink(); assertTrue(h.stream.handoff("shown-again", shownAgain));
        h.stream.visibility(true); h.event("profiles", "{\"people\":[]}");
        assertFalse(live.closed); assertEquals(1, h.source.connections.size()); assertEquals(1, shownAgain.received.size());
        assertFalse(h.stream.bind("shown-again", h.source, shownAgain));
        h.event("transport_error", "{}"); assertTrue(live.closed); assertFalse(h.stream.ready());
        Sink hiddenAgain = new Sink(); assertTrue(h.stream.handoff("hidden-retrying", hiddenAgain));
        h.advance(650); assertEquals(2, h.source.connections.size());
        h.event("ready", "true"); assertEquals(Boolean.TRUE, hiddenAgain.connected);
        h.stream.stop(); h.flush(); assertFalse(h.stream.handoff("stopped", hiddenAgain));
        assertTrue(h.clock.timers.isEmpty());
    }

    @Test public void stopAndRebindingCancelQueuedOpensAndCloseAlreadyReturnedHandles() {
        Harness h = new Harness(); h.stream.bind("first", h.source, h.sink); h.stream.bind("second", h.source, h.sink); h.stream.stop(); h.flush();
        assertTrue(h.source.connections.isEmpty()); assertTrue(h.clock.timers.isEmpty());
        h.stream.bind("first", h.source, h.sink); h.worker.flush(); Connection old = h.source.current();
        h.stream.stop(); h.clock.flush(); assertTrue(old.closed); assertTrue(h.clock.timers.isEmpty());
        old.events.event("ready", "true"); old.events.event("presence", "[{id:'stale'}]"); h.flush(); assertEquals(Boolean.FALSE, h.sink.connected);
    }

    @Test public void framesArrivingBeforeTheHandleAssignmentAreDeliveredInOrder() {
        Harness h = new Harness(); h.source.opening = events -> { events.event("presence", "[{\"id\":\"bob\"}]"); events.event("ready", "true"); };
        h.bind("session"); assertEquals(Boolean.TRUE, h.sink.connected); assertEquals("bob", h.sink.presence.getAsJsonArray().get(0).getAsJsonObject().get("id").getAsString());
        assertFalse(h.stream.bind("session", h.source, h.sink)); h.flush(); assertEquals(1, h.source.connections.size()); h.stream.stop();
    }

    @Test public void delayedVisibilityWorkersCannotMarkAReturnedAppAsHidden() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        Connection socket = h.source.current();
        h.stream.visibility(false); h.stream.visibility(true);
        java.util.Collections.reverse(h.worker.tasks); h.flush();
        assertEquals(List.of(true, true), h.source.visibility);
        assertFalse(socket.closed); assertEquals(1, h.source.connections.size());
        h.stream.stop();
    }

    @Test public void repeatedResumeDoesNotReplaceTheSocketWhileItIsConnecting() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        h.event("transport_error", "{}"); h.stream.resume(); h.flush();
        Connection pending = h.source.current();
        for (int i = 0; i < 4; i++) { h.stream.resume(); h.flush(); }
        assertFalse("Focus callbacks must not cancel the handshake already in progress", pending.closed);
        assertEquals(2, h.source.connections.size());
        h.event("ready", "true"); assertEquals(Boolean.TRUE, h.sink.connected);
        h.stream.stop();
    }

    @Test public void returningProbesOnceAndImmediatelyReplacesAnUnresponsiveSocket() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        Connection old = h.source.current(); h.source.failProbe = true;
        long resumed = h.clock.now;
        h.stream.resume(true); h.stream.resume(true); h.stream.resume();
        assertEquals(1, h.worker.tasks.size()); h.flush();
        assertEquals(1, h.source.probes); assertTrue(old.closed);
        assertEquals("Reconnect starts without waiting for the heartbeat or backoff timer", 2, h.source.connections.size());
        assertEquals(resumed, h.clock.now); assertTrue(h.source.controls.isEmpty());
        h.stream.stop();
    }

    @Test public void returningRearmsTheWatchdogOnAHealthyRoundTripWithoutReplacingTheSocket() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        Connection old = h.source.current(); h.advance(44000);
        h.stream.resume(true); h.flush(); h.advance(1001);
        assertFalse(old.closed); assertEquals(Boolean.TRUE, h.sink.connected);
        assertEquals(1, h.source.probes); assertEquals(1, h.source.connections.size());
        assertEquals(1, h.source.controls.size());
        h.stream.stop();
    }

    @Test public void returningResetsAccumulatedBackgroundBackoff() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        for (int i = 0; i < 6; i++) {
            h.event("transport_error", "{}"); h.advance(550L << i);
        }
        h.stream.resume(true); h.flush(); h.event("transport_error", "{}");
        assertEquals(550, h.clock.timers.get(0).at - h.clock.now);
        h.stream.stop();
    }

    @Test public void aRevokedAuthResultCannotExpireTheNextSession() {
        Harness h = new Harness(); h.bind("old"); h.source.authFailure = new Exception("Expired");
        h.source.current().events.event("transport_error", "{}"); h.clock.flush(); h.worker.flush();
        h.stream.bind("new", h.source, h.sink); h.flush(); assertTrue(h.sink.authErrors.isEmpty());
        h.event("ready", "true"); assertEquals(Boolean.TRUE, h.sink.connected); h.stream.stop();
    }

    @Test public void failedControlSendsDisconnectOnceClearPresenceAndScheduleTheSameRetry() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true"); h.event("presence", "[{\"id\":\"bob\"}]"); h.source.failSend = true;
        h.stream.resume(); h.flush();
        assertEquals(Boolean.FALSE, h.sink.connected); assertEquals(0, h.sink.presence.getAsJsonArray().size());
        assertTrue(h.source.current().closed); assertEquals(1, h.source.auth); assertEquals(1, h.clock.timers.size()); assertEquals(550, h.clock.timers.get(0).at - h.clock.now);
        h.stream.stop(); assertTrue(h.clock.timers.isEmpty());
    }

    @Test public void actualSocketFailuresNotifyRecoveryButNormalStopAndStaleFramesDoNot() {
        Harness h = new Harness(); h.bind("session"); h.event("ready", "true");
        Connection first = h.source.current();
        h.event("ping", "true"); assertEquals(0, h.sink.failures);
        h.event("transport_error", "{}"); assertEquals(1, h.sink.failures);
        first.events.event("transport_error", "{}"); h.flush(); assertEquals(1, h.sink.failures);
        h.advance(550); h.event("ready", "true");
        h.stream.stop(); assertEquals(1, h.sink.failures);
    }
}
