package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;

/** Web workspace-stream retry/watchdog/resume policy over the existing native socket. */
final class NativeWorkspaceEvents<C> {
    interface Scheduler extends NativeChatRefresh.Dispatcher {
        long now();
        double random();
    }
    interface Events { void event(String kind, String value); }
    interface Source<C> {
        C open(Events events) throws Exception;
        void close(C connection);
        void send(C connection, String type, boolean visible) throws Exception;
        void probe(C connection) throws Exception;
        void checkAuth() throws Exception;
    }
    interface Listener<C> {
        void connection(C connection);
        void event(String kind, String value);
        void connected(boolean value);
        void expired();
        void authFailed(Exception error);
    }
    private static final class Attempt<C> {
        C connection;
        boolean opened;
        final long started;
        Object probe;
        final List<String[]> pending = new ArrayList<>();
        Attempt(long started) { this.started = started; }
    }
    private static final class Scope<C> {
        final String key;
        final Source<C> source;
        final Listener<C> listener;
        final Object sendLock = new Object();
        volatile Attempt<C> attempt;
        boolean ready, checkingAuth;
        volatile boolean visible = true;
        int attempts;
        long lastMessage;
        Runnable retry, watchdog;
        Scope(String key, Source<C> source, Listener<C> listener) { this.key = key; this.source = source; this.listener = listener; }
    }
    private final Executor worker;
    private final Executor probeWorker;
    private final Scheduler scheduler;
    private volatile Scope<C> scope;

    NativeWorkspaceEvents(Executor worker, Scheduler scheduler) { this(worker, worker, scheduler); }

    NativeWorkspaceEvents(Executor worker, Executor probeWorker, Scheduler scheduler) {
        this.worker = worker; this.probeWorker = probeWorker; this.scheduler = scheduler;
    }

    boolean bind(String key, Source<C> source, Listener<C> listener) {
        if (scope != null && scope.key.equals(key)) return false;
        stop(); Scope<C> next = new Scope<>(key, source, listener); scope = next; connect(next);
        return true;
    }

    void stop() {
        Scope<C> old = scope; scope = null;
        if (old == null) return;
        clearTimers(old); disconnect(old);
    }

    void failed() {
        Scope<C> current = scope;
        if (current != null && current.attempt != null) lost(current, current.attempt);
    }

    void resume() {
        resume(false);
    }

    void resume(boolean returning) {
        Scope<C> current = scope;
        if (current == null) return;
        Attempt<C> attempt = current.attempt;
        if (returning) current.attempts = 0;
        if (current.ready && scheduler.now() - current.lastMessage < 45000) {
            if (returning) probe(current, attempt);
            else if (attempt.probe == null) send(current, "refresh");
            return;
        }
        // onStart, window focus and network callbacks can arrive before ready.
        // Give the same in-flight handshake its existing watchdog deadline.
        if (!current.ready && attempt != null && scheduler.now() - attempt.started < 45000) return;
        clearTimers(current); disconnect(current);
        if (scope == current) connect(current);
    }

    private void probe(Scope<C> current, Attempt<C> attempt) {
        if (attempt == null || !attempt.opened || attempt.probe != null) return;
        Object token = new Object(); attempt.probe = token;
        probeWorker.execute(() -> {
            if (scope != current || current.attempt != attempt) return;
            Exception failure = null;
            try { current.source.probe(attempt.connection); } catch (Exception error) { failure = error; }
            Exception error = failure;
            scheduler.post(() -> {
                if (scope != current || current.attempt != attempt || attempt.probe != token) return;
                if (error != null) lost(current, attempt);
                else {
                    attempt.probe = null;
                    current.lastMessage = scheduler.now(); watchdog(current, attempt);
                    if (current.visible) send(current, "refresh");
                }
            });
        });
    }

    /** Updates server-side presence without tearing down a healthy socket. */
    void visibility(boolean value) {
        Scope<C> current = scope;
        if (current == null || current.visible == value) return;
        current.visible = value;
        Attempt<C> attempt = current.attempt;
        if (attempt != null && attempt.opened) send(current, "visibility");
    }

    private void clearTimers(Scope<C> current) {
        if (current.retry != null) scheduler.remove(current.retry);
        if (current.watchdog != null) scheduler.remove(current.watchdog);
        current.retry = current.watchdog = null;
    }

    private void disconnect(Scope<C> current) {
        Attempt<C> attempt = current.attempt; current.attempt = null; current.ready = false;
        if (attempt != null && attempt.opened) current.source.close(attempt.connection);
        current.listener.connection(null);
        current.listener.connected(false);
    }

    private void connect(Scope<C> current) {
        if (scope != current) return;
        clearTimers(current);
        Attempt<C> attempt = new Attempt<>(scheduler.now()); current.attempt = attempt;
        watchdog(current, attempt);
        worker.execute(() -> {
            if (scope != current || current.attempt != attempt) return;
            try {
                C connection = current.source.open((kind, value) -> scheduler.post(() -> receive(current, attempt, kind, value)));
                scheduler.post(() -> {
                    if (scope != current || current.attempt != attempt) { current.source.close(connection); return; }
                    attempt.connection = connection; attempt.opened = true;
                    current.listener.connection(connection);
                    List<String[]> pending = new ArrayList<>(attempt.pending); attempt.pending.clear();
                    for (String[] event : pending) receive(current, attempt, event[0], event[1]);
                    if (!current.visible) send(current, "visibility");
                });
            } catch (Exception error) { scheduler.post(() -> lost(current, attempt)); }
        });
    }

    private void watchdog(Scope<C> current, Attempt<C> attempt) {
        if (current.watchdog != null) scheduler.remove(current.watchdog);
        current.watchdog = () -> lost(current, attempt);
        scheduler.postDelayed(current.watchdog, 45000);
    }

    private void receive(Scope<C> current, Attempt<C> attempt, String kind, String value) {
        if (scope != current || current.attempt != attempt) return;
        if (!attempt.opened) { attempt.pending.add(new String[] {kind, value}); return; }
        if (kind.equals("transport_error")) { lost(current, attempt); return; }
        if (kind.equals("auth_expired")) { stop(); current.listener.expired(); return; }
        current.lastMessage = scheduler.now(); watchdog(current, attempt);
        if (kind.equals("ready")) {
            current.ready = true; current.attempts = 0; current.listener.connected(true);
        } else if (!kind.equals("ping")) current.listener.event(kind, value);
    }

    private void lost(Scope<C> current, Attempt<C> attempt) {
        if (scope != current || current.attempt != attempt) return;
        boolean returning = current.visible && attempt.probe != null;
        clearTimers(current); disconnect(current);
        if (scope != current) return;
        if (!current.checkingAuth) {
            current.checkingAuth = true;
            worker.execute(() -> {
                if (scope != current) return;
                Exception failure = null;
                try { current.source.checkAuth(); } catch (Exception error) { failure = error; }
                Exception error = failure;
                scheduler.post(() -> {
                    if (scope != current) return;
                    current.checkingAuth = false;
                    if (error != null) current.listener.authFailed(error);
                });
            });
        }
        if (returning) {
            current.attempts = 0;
            connect(current);
            return;
        }
        long base = Math.min(30000, 500L << Math.min(current.attempts, 6));
        current.attempts = Math.min(6, current.attempts + 1);
        current.retry = () -> connect(current);
        scheduler.postDelayed(current.retry, (long) (base + scheduler.random() * base * .2));
    }

    private void send(Scope<C> current, String type) {
        Attempt<C> attempt = current.attempt;
        if (attempt == null || !attempt.opened) return;
        worker.execute(() -> {
            synchronized (current.sendLock) {
                if (scope != current || current.attempt != attempt) return;
                // Workers can start out of order during a rapid hide/show.
                // Sample the latest visibility only when the send owns the wire.
                try { current.source.send(attempt.connection, type, current.visible); }
                catch (Exception error) { scheduler.post(() -> lost(current, attempt)); }
            }
        });
    }
}
