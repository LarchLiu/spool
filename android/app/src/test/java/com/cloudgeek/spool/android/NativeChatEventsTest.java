package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import org.junit.Test;

public class NativeChatEventsTest {
    private static final class Queue implements Executor, NativeChatRefresh.Dispatcher {
        final List<Runnable> tasks = new ArrayList<>(), timers = new ArrayList<>();
        long delay;
        public void execute(Runnable task) { tasks.add(task); }
        public void post(Runnable task) { tasks.add(task); }
        public void postDelayed(Runnable task, long millis) { timers.add(task); delay = millis; }
        public void remove(Runnable task) { timers.removeIf(item -> item == task); }
        void run() { tasks.remove(0).run(); }
        void flush() { while (!tasks.isEmpty()) run(); }
        void tick() { timers.remove(0).run(); }
    }
    private static final class Source implements NativeChatEvents.Source<String> {
        final List<NativeChatEvents.Events<String>> streams = new ArrayList<>();
        int closed, reads, auth;
        String result = "fallback";
        Exception failure, authFailure, openFailure;
        Runnable reading = () -> {};
        public Runnable open(NativeChatEvents.Events<String> events) throws Exception {
            if (openFailure != null) throw openFailure;
            streams.add(events); return () -> closed++;
        }
        public String snapshot() throws Exception { reads++; reading.run(); if (failure != null) throw failure; return result; }
        public void checkAuth() throws Exception { auth++; if (authFailure != null) throw authFailure; }
        NativeChatEvents.Events<String> current() { return streams.get(streams.size() - 1); }
    }
    private static final class Sink implements NativeChatEvents.Listener<String> {
        final List<String> events = new ArrayList<>(), errors = new ArrayList<>(), authErrors = new ArrayList<>();
        final List<Boolean> connected = new ArrayList<>();
        public void event(String kind, String value) { events.add(kind + ":" + value); }
        public void connected(boolean value) { connected.add(value); }
        public void failed(Exception error) { errors.add(error.getMessage()); }
        public void authFailed(Exception error) { authErrors.add(error.getMessage()); }
    }
    private static final class Harness {
        final Queue worker = new Queue(), ui = new Queue();
        final Source source = new Source(); final Sink sink = new Sink();
        final NativeChatEvents<String> stream = new NativeChatEvents<>(worker, ui);
        Harness() { stream.bind("chat", source, sink); }
        void event(String kind, String value) { source.current().event(kind, value); ui.flush(); }
        void error() { source.current().error(); ui.flush(); }
        void read() { worker.flush(); ui.flush(); }
    }

    @Test public void healthySseInitializesWithoutAGetAndUnchangedBindsDoNotReconnect() {
        Harness h = new Harness(); assertEquals(1, h.source.streams.size()); assertTrue(h.worker.tasks.isEmpty());
        h.event("snapshot", "first"); h.event("metadata", "title"); h.event("turn", "reply");
        h.stream.bind("chat", new Source(), new Sink());
        assertEquals(1, h.source.streams.size()); assertEquals(List.of("snapshot:first", "metadata:title", "turn:reply"), h.sink.events);
        h.error(); h.read(); assertEquals(1, h.source.auth); assertEquals(0, h.source.reads);
        assertEquals(3000, h.ui.delay); assertEquals(1, h.ui.timers.size()); assertEquals(1, h.source.closed);
        h.ui.tick(); h.event("snapshot", "reconnected");
        assertEquals(2, h.source.streams.size()); assertEquals(0, h.source.reads); assertEquals(Boolean.TRUE, h.sink.connected.get(h.sink.connected.size() - 1));
        h.stream.stop(); assertEquals(2, h.source.closed); assertTrue(h.ui.timers.isEmpty());
    }

    @Test public void repeatedFailedAttemptsShareOnePendingInitializationGet() {
        Harness h = new Harness(); h.error(); h.error(); assertEquals(1, h.worker.tasks.size());
        h.ui.tick(); h.error(); assertEquals(1, h.worker.tasks.size());
        h.read(); assertEquals(1, h.source.reads); assertEquals(List.of("snapshot:fallback"), h.sink.events);
        assertEquals(Boolean.FALSE, h.sink.connected.get(h.sink.connected.size() - 1));
        h.ui.tick(); h.error(); h.read(); assertEquals(1, h.source.auth); assertEquals(1, h.source.reads);
    }

    @Test public void aFailedInitializationCanRetryOnTheNextStreamFailureWithoutBecomingANewChat() {
        Harness h = new Harness(); h.source.failure = new Exception("Chat not found"); h.error(); h.read();
        assertEquals(List.of("Chat not found"), h.sink.errors); assertTrue(h.sink.events.isEmpty());
        h.ui.tick(); h.source.failure = null; h.error(); h.read();
        assertEquals(2, h.source.reads); assertEquals(List.of("snapshot:fallback"), h.sink.events);
    }

    @Test public void aNewSseSnapshotRevokesQueuedAndInFlightFallbackSuccessAndFailure() {
        for (boolean alreadyReading : new boolean[] {false, true}) for (boolean fail : new boolean[] {false, true}) {
            Harness h = new Harness(); h.error(); h.ui.tick();
            if (fail) h.source.failure = new Exception("Stale failure");
            if (alreadyReading) h.source.reading = () -> h.source.current().event("snapshot", "newer");
            else h.event("snapshot", "newer");
            h.read(); assertEquals(alreadyReading ? 1 : 0, h.source.reads);
            assertEquals(List.of("snapshot:newer"), h.sink.events); assertTrue(h.sink.errors.isEmpty());
        }
    }

    @Test public void leavingAndReturningToTheSameKeyRevokesOldFramesReadsAndRetryTimers() {
        Harness h = new Harness(); NativeChatEvents.Events<String> old = h.source.current();
        h.error(); h.stream.bind("second", h.source, h.sink); h.stream.bind("chat", h.source, h.sink);
        old.event("snapshot", "stale"); old.event("metadata", "stale"); old.event("turn", "stale"); old.error();
        h.read(); assertEquals(0, h.source.reads); assertTrue(h.sink.events.isEmpty()); assertTrue(h.ui.timers.isEmpty());
        h.event("snapshot", "current"); assertEquals(List.of("snapshot:current"), h.sink.events);
        assertEquals(3, h.source.streams.size()); assertEquals(2, h.source.closed);
    }

    @Test public void backgroundingRevokesAlreadyReadFallbackAndAuthResults() {
        Harness h = new Harness(); h.error(); h.worker.flush(); h.stream.stop(); h.ui.flush();
        assertTrue(h.sink.events.isEmpty()); assertTrue(h.sink.errors.isEmpty()); assertTrue(h.ui.timers.isEmpty());
        h.stream.bind("chat", h.source, h.sink); h.event("snapshot", "current");
        h.source.authFailure = new Exception("Expired"); h.error(); h.worker.flush(); h.stream.stop(); h.ui.flush();
        assertTrue(h.sink.authErrors.isEmpty()); assertTrue(h.ui.timers.isEmpty());
    }

    @Test public void delayedOldConnectionFramesCannotReplaceAReconnectedSnapshot() {
        Harness h = new Harness(); NativeChatEvents.Events<String> old = h.source.current();
        h.event("snapshot", "first"); h.error(); h.ui.tick(); h.event("snapshot", "second");
        old.event("snapshot", "old"); old.error(); h.ui.flush(); h.read();
        assertEquals(List.of("snapshot:first", "snapshot:second"), h.sink.events); assertTrue(h.ui.timers.isEmpty());
        assertEquals(1, h.source.auth); assertEquals(0, h.source.reads);
    }

    @Test public void synchronousOpenFailureUsesTheSameFallbackAndReconnectLifecycle() {
        Harness h = new Harness(); h.stream.stop(); h.source.openFailure = new Exception("Offline");
        h.stream.bind("new", h.source, h.sink); assertEquals(1, h.worker.tasks.size()); assertEquals(1, h.ui.timers.size());
        h.read(); assertEquals(List.of("snapshot:fallback"), h.sink.events);
        h.source.openFailure = null; h.ui.tick(); h.event("snapshot", "online");
        h.source.authFailure = new Exception("Expired"); h.error(); h.read(); assertEquals(List.of("Expired"), h.sink.authErrors);
    }
}
