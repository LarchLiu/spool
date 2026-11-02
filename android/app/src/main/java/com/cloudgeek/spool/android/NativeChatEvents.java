package com.cloudgeek.spool.android;

import java.util.concurrent.Executor;

/** SSE-first initialization and failure-only REST fallback, like Web's chat-stream. */
final class NativeChatEvents<T> {
    interface Events<T> {
        void event(String kind, T value);
        void error();
    }
    interface Source<T> {
        Runnable open(Events<T> events) throws Exception;
        T snapshot() throws Exception;
        void checkAuth() throws Exception;
    }
    interface Listener<T> {
        void event(String kind, T value);
        void connected(boolean connected);
        void failed(Exception error);
        void authFailed(Exception error);
    }
    private static final class Attempt {
        Runnable close;
    }
    private static final class Scope<T> {
        final String key;
        final Source<T> source;
        final Listener<T> listener;
        boolean initialized;
        volatile Object fallback;
        Attempt attempt;
        Runnable retry;
        Scope(String key, Source<T> source, Listener<T> listener) { this.key = key; this.source = source; this.listener = listener; }
    }
    private final Executor worker;
    private final NativeChatRefresh.Dispatcher dispatcher;
    private volatile Scope<T> scope;

    NativeChatEvents(Executor worker, NativeChatRefresh.Dispatcher dispatcher) { this.worker = worker; this.dispatcher = dispatcher; }

    void bind(String key, Source<T> source, Listener<T> listener) {
        if (scope != null && scope.key.equals(key)) return;
        stop();
        Scope<T> next = new Scope<>(key, source, listener); scope = next;
        connect(next);
    }

    void stop() {
        Scope<T> old = scope; scope = null;
        if (old == null) return;
        old.fallback = null;
        if (old.retry != null) dispatcher.remove(old.retry);
        close(old);
    }

    private void close(Scope<T> current) {
        Attempt attempt = current.attempt; current.attempt = null;
        if (attempt != null && attempt.close != null) attempt.close.run();
    }

    private void connect(Scope<T> current) {
        if (scope != current) return;
        current.retry = null;
        Attempt attempt = new Attempt(); current.attempt = attempt;
        try {
            Runnable close = current.source.open(new Events<T>() {
                public void event(String kind, T value) {
                    dispatcher.post(() -> {
                        if (scope != current || current.attempt != attempt) return;
                        if (kind.equals("snapshot")) { current.initialized = true; current.fallback = null; }
                        current.listener.connected(true);
                        current.listener.event(kind, value);
                    });
                }
                public void error() { dispatcher.post(() -> lost(current, attempt)); }
            });
            if (scope == current && current.attempt == attempt) attempt.close = close;
            else if (close != null) close.run();
        } catch (Exception error) { lost(current, attempt); }
    }

    private void lost(Scope<T> current, Attempt attempt) {
        if (scope != current || current.attempt != attempt) return;
        close(current);
        current.listener.connected(false);
        if (scope != current) return;
        // The server emits no retry override; emulate EventSource's default reconnect delay.
        current.retry = () -> connect(current);
        dispatcher.postDelayed(current.retry, 3000);
        if (current.initialized) {
            worker.execute(() -> {
                if (scope != current) return;
                try { current.source.checkAuth(); }
                catch (Exception error) { dispatcher.post(() -> { if (scope == current) current.listener.authFailed(error); }); }
            });
        } else if (current.fallback == null) {
            Object request = new Object(); current.fallback = request;
            worker.execute(() -> {
                if (scope != current || current.fallback != request) return;
                T value = null; Exception failure = null;
                try { value = current.source.snapshot(); }
                catch (Exception error) { failure = error; }
                T result = value; Exception error = failure;
                dispatcher.post(() -> {
                    if (scope != current || current.fallback != request) return;
                    current.fallback = null;
                    if (error == null) {
                        current.initialized = true;
                        current.listener.event("snapshot", result);
                    } else current.listener.failed(error);
                });
            });
        }
    }
}
