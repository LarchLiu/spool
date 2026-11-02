package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import org.junit.Test;

public class NativeChatRefreshTest {
    private static final class Queue implements Executor, NativeChatRefresh.Dispatcher {
        final List<Runnable> immediate = new ArrayList<>(), delayed = new ArrayList<>();
        long delay;
        public void execute(Runnable task) { immediate.add(task); }
        public void post(Runnable task) { immediate.add(task); }
        public void postDelayed(Runnable task, long millis) { delayed.add(task); delay = millis; }
        public void remove(Runnable task) { delayed.removeIf(item -> item == task); }
        void run() { immediate.remove(0).run(); }
        void tick() { delayed.remove(0).run(); }
    }
    private static final class Sink implements NativeChatRefresh.Listener<String> {
        final List<String> values = new ArrayList<>(), errors = new ArrayList<>();
        public void received(String value) { values.add(value); }
        public void failed(Exception error) { errors.add(error.getMessage()); }
    }

    @Test public void immediateAndOneSecondCadenceDoesNotRestartForUnchangedRenders() {
        Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink();
        NativeChatRefresh<String> refresh = new NativeChatRefresh<>(worker, ui);
        refresh.bind("chat", () -> "snapshot", sink);
        assertEquals(1, worker.immediate.size()); assertEquals(1000, ui.delay); assertEquals(1, ui.delayed.size());
        refresh.bind("chat", () -> { fail("Unchanged scope must not replace its source"); return "wrong"; }, new Sink());
        assertEquals(1, worker.immediate.size()); assertEquals(1, ui.delayed.size());
        worker.run(); ui.run(); assertEquals(List.of("snapshot"), sink.values);
        ui.tick(); assertEquals(1, worker.immediate.size()); assertEquals(1, ui.delayed.size());
        worker.run(); ui.run(); assertEquals(List.of("snapshot", "snapshot"), sink.values);
    }

    @Test public void stopRevokesBothQueuedReadsAndAlreadyPostedResults() {
        Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink();
        NativeChatRefresh<String> refresh = new NativeChatRefresh<>(worker, ui);
        refresh.bind("chat", () -> { fail("Revoked queued work cannot read"); return "wrong"; }, sink);
        refresh.stop(); worker.run(); assertTrue(ui.immediate.isEmpty()); assertTrue(ui.delayed.isEmpty());
        refresh.bind("chat", () -> "stale", sink); worker.run();
        refresh.stop(); ui.run(); assertTrue(sink.values.isEmpty()); assertTrue(ui.delayed.isEmpty());
    }

    @Test public void rebindingRevokesOldIdentityEvenWhenTheSameKeyIsReusedLater() {
        Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink();
        NativeChatRefresh<String> refresh = new NativeChatRefresh<>(worker, ui);
        refresh.bind("first", () -> "old-first", sink); worker.run();
        refresh.bind("second", () -> { fail("Second chat was already left"); return "wrong"; }, sink);
        refresh.bind("first", () -> "new-first", sink);
        worker.run(); worker.run(); ui.run(); ui.run();
        assertEquals(List.of("new-first"), sink.values); assertEquals(1, ui.delayed.size());
    }

    @Test public void failuresRetainTheTimerAndStopInsideAListenerCancelsIt() {
        Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink();
        NativeChatRefresh<String> refresh = new NativeChatRefresh<>(worker, ui);
        refresh.bind("chat", () -> { throw new Exception("offline"); }, sink);
        worker.run(); ui.run(); assertEquals(List.of("offline"), sink.errors); assertEquals(1, ui.delayed.size());
        refresh.bind("new", () -> "terminal", new NativeChatRefresh.Listener<String>() {
            public void received(String value) { sink.received(value); refresh.stop(); }
            public void failed(Exception error) { fail(error.toString()); }
        });
        worker.run(); ui.run(); assertEquals(List.of("terminal"), sink.values); assertTrue(ui.delayed.isEmpty());
    }

    @Test public void slowReadsDoNotShiftTheWebIntervalAndMayCompleteOutOfOrder() {
        Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink(); int[] count = {0};
        NativeChatRefresh<String> refresh = new NativeChatRefresh<>(worker, ui);
        refresh.bind("chat", () -> "snapshot-" + ++count[0], sink);
        ui.tick(); ui.tick(); assertEquals(3, worker.immediate.size()); assertEquals(1, ui.delayed.size());
        worker.run(); worker.run(); worker.run();
        ui.immediate.remove(2).run(); ui.run(); ui.run();
        assertEquals(List.of("snapshot-3", "snapshot-1", "snapshot-2"), sink.values);
        refresh.stop(); assertTrue(ui.delayed.isEmpty());
    }

    @Test public void aScopeRevokedDuringTheReadCannotReportSuccessOrFailure() {
        for (boolean fail : new boolean[] {false, true}) {
            Queue worker = new Queue(), ui = new Queue(); Sink sink = new Sink();
            NativeChatRefresh<String> refresh = new NativeChatRefresh<>(worker, ui);
            refresh.bind("chat", () -> { refresh.stop(); if (fail) throw new Exception("expired"); return "stale"; }, sink);
            worker.run(); ui.run(); assertTrue(sink.values.isEmpty()); assertTrue(sink.errors.isEmpty()); assertTrue(ui.delayed.isEmpty());
        }
    }
}
