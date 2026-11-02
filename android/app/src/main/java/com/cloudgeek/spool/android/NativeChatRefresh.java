package com.cloudgeek.spool.android;

import java.util.concurrent.Executor;

/** Web's running-chat fallback. Bind/stop/callbacks are UI-thread; only Source runs on the worker. */
final class NativeChatRefresh<T> {
    interface Dispatcher {
        void post(Runnable task);
        void postDelayed(Runnable task, long millis);
        void remove(Runnable task);
    }
    interface Source<T> { T read() throws Exception; }
    interface Listener<T> {
        void received(T value);
        void failed(Exception error);
    }
    private static final class Scope<T> {
        final String key;
        final Source<T> source;
        final Listener<T> listener;
        Scope(String key, Source<T> source, Listener<T> listener) { this.key = key; this.source = source; this.listener = listener; }
    }
    private final Executor worker;
    private final Dispatcher dispatcher;
    private final Runnable tick = this::refresh;
    private volatile Scope<T> scope;

    NativeChatRefresh(Executor worker, Dispatcher dispatcher) { this.worker = worker; this.dispatcher = dispatcher; }

    // Binding unchanged renders must not restart the cadence or add another timer.
    void bind(String key, Source<T> source, Listener<T> listener) {
        if (scope != null && scope.key.equals(key)) return;
        stop();
        scope = new Scope<>(key, source, listener);
        refresh();
    }

    void stop() { scope = null; dispatcher.remove(tick); }

    private void refresh() {
        Scope<T> captured = scope;
        if (captured == null) return;
        dispatcher.postDelayed(tick, 1000);
        worker.execute(() -> {
            if (scope != captured) return;
            T value = null;
            Exception failure = null;
            try { value = captured.source.read(); }
            catch (Exception error) { failure = error; }
            T result = value;
            Exception error = failure;
            dispatcher.post(() -> {
                if (scope != captured) return;
                if (error == null) captured.listener.received(result);
                else captured.listener.failed(error);
            });
        });
    }
}
