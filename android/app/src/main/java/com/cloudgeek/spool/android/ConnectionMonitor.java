package com.cloudgeek.spool.android;

import java.util.concurrent.Executor;

// Control methods and Dispatcher callbacks run on the UI thread; only Probe runs off-thread.
final class ConnectionMonitor {
    interface Dispatcher {
        void post(Runnable task);
        void postDelayed(Runnable task, long delayMillis);
        void remove(Runnable task);
    }

    interface Probe { String read() throws Exception; }
    interface Listener { void update(String snapshot); }

    private final Executor worker;
    private final Dispatcher dispatcher;
    private final Runnable refresh = this::refresh;
    private Probe probe;
    private Listener listener;
    private long generation;
    private boolean active;
    private boolean busy;

    ConnectionMonitor(Executor worker, Dispatcher dispatcher) {
        this.worker = worker;
        this.dispatcher = dispatcher;
    }

    void start(Probe probe, Listener listener) {
        stop();
        this.probe = probe;
        this.listener = listener;
        refresh();
    }

    void stop() {
        ++generation;
        dispatcher.remove(refresh);
        probe = null;
        listener = null;
    }

    void setActive(boolean active) {
        this.active = active;
        dispatcher.remove(refresh);
        if (active) refresh();
    }

    private void refresh() {
        if (!active || busy || probe == null) return;
        busy = true;
        long token = generation;
        Probe captured = probe;
        worker.execute(() -> {
            String snapshot;
            try { snapshot = captured.read(); }
            catch (Exception ignored) { snapshot = null; }
            String result = snapshot;
            dispatcher.post(() -> {
                busy = false;
                if (active && generation == token && listener != null) listener.update(result);
                if (active && probe != null) {
                    dispatcher.remove(refresh);
                    dispatcher.postDelayed(refresh, generation == token ? 5000 : 0);
                }
            });
        });
    }
}
