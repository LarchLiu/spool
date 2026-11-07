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
        boolean blocked, failSend; int auth;
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
        public void checkAuth() throws Exception { auth++; if (authFailure != null) throw authFailure; }
        Connection current() { return connections.get(connections.size() - 1); }
    }
    static final class Sink implements NativeWorkspaceEvents.Listener<Connection> {
        Boolean connected; int expired; JsonElement presence = new JsonArray(); final JsonArray received = new JsonArray();
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
}
