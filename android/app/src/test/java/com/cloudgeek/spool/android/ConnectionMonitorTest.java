package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import org.junit.Test;

public class ConnectionMonitorTest {
    private static final class Queue implements Executor, ConnectionMonitor.Dispatcher {
        final List<Runnable> immediate = new ArrayList<>();
        final List<Runnable> delayed = new ArrayList<>();
        long delay;
        @Override public void execute(Runnable task) { immediate.add(task); }
        @Override public void post(Runnable task) { immediate.add(task); }
        @Override public void postDelayed(Runnable task, long millis) { delayed.add(task); delay = millis; }
        @Override public void remove(Runnable task) { immediate.removeIf(t -> t == task); delayed.removeIf(t -> t == task); }
        void run() { immediate.remove(0).run(); }
        void tick() { delayed.remove(0).run(); }
    }

    @Test public void foregroundOnlyAndNoOverlappingProbes() {
        Queue worker = new Queue(), ui = new Queue();
        List<String> updates = new ArrayList<>();
        ConnectionMonitor monitor = new ConnectionMonitor(worker, ui);
        monitor.start(() -> "direct", updates::add);
        assertTrue(worker.immediate.isEmpty());
        monitor.setActive(true);
        monitor.setActive(true);
        assertEquals(1, worker.immediate.size());
        worker.run(); ui.run();
        assertEquals(List.of("direct"), updates);
        assertEquals(5000, ui.delay);
        ui.tick();
        monitor.setActive(false);
        worker.run(); ui.run();
        assertEquals(1, updates.size());
        assertTrue(ui.delayed.isEmpty());
        monitor.setActive(true);
        assertEquals(1, worker.immediate.size());
    }

    @Test public void reconnectDropsOldResultAndSchedulesNewNode() {
        Queue worker = new Queue(), ui = new Queue();
        List<String> updates = new ArrayList<>();
        ConnectionMonitor monitor = new ConnectionMonitor(worker, ui);
        monitor.setActive(true);
        monitor.start(() -> "old-node", updates::add);
        monitor.start(() -> "new-node", updates::add);
        assertEquals(1, worker.immediate.size());
        worker.run(); ui.run();
        assertTrue(updates.isEmpty());
        assertEquals(0, ui.delay);
        ui.tick(); worker.run(); ui.run();
        assertEquals(List.of("new-node"), updates);
    }

    @Test public void failureClearsPreviousDataAndDisconnectStopsPolling() {
        Queue worker = new Queue(), ui = new Queue();
        List<String> updates = new ArrayList<>();
        ConnectionMonitor monitor = new ConnectionMonitor(worker, ui);
        monitor.setActive(true);
        monitor.start(() -> { throw new Exception("private error"); }, updates::add);
        worker.run(); ui.run();
        assertEquals(1, updates.size());
        assertNull(updates.get(0));
        ui.tick();
        monitor.stop();
        worker.run(); ui.run();
        assertEquals(1, updates.size());
        assertTrue(ui.delayed.isEmpty());
    }
}
