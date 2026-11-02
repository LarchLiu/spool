package com.cloudgeek.spool.android;

import java.util.Objects;
import java.util.concurrent.Executor;

/** Sequential history hydration, yielding to a committed frame before each page. */
final class NativeHistoryLoader<T> {
    static final class Cursor {
        final String id, before, error;
        final boolean more;
        Cursor(String id, String before, boolean more, String error) { this.id = id; this.before = before; this.more = more; this.error = error; }
        boolean available() { return !id.isEmpty() && !before.isEmpty() && more && error.isEmpty(); }
        @Override public boolean equals(Object value) {
            if (!(value instanceof Cursor)) return false;
            Cursor other = (Cursor) value;
            return id.equals(other.id) && before.equals(other.before) && more == other.more && error.equals(other.error);
        }
        @Override public int hashCode() { return Objects.hash(id, before, more, error); }
    }
    interface Scheduler { void post(Runnable task); }
    interface Paint { Runnable afterPaint(Runnable task); }
    interface Source<T> {
        T read(Cursor from) throws Exception;
        Cursor cursor(T page);
    }
    interface Listener<T> {
        void received(Cursor from, T page);
        void failed(Exception error);
    }
    private static final class Attempt { Runnable cancelPaint; boolean started; }
    private static final class Scope<T> {
        final String key;
        final Source<T> source;
        final Listener<T> listener;
        Cursor cursor;
        Object surface;
        Paint paint;
        volatile Attempt attempt;
        boolean consumed, failed;
        Scope(String key, Source<T> source, Listener<T> listener) { this.key = key; this.source = source; this.listener = listener; }
    }
    private final Executor worker;
    private final Scheduler scheduler;
    private volatile Scope<T> scope;

    NativeHistoryLoader(Executor worker, Scheduler scheduler) { this.worker = worker; this.scheduler = scheduler; }

    void bind(String key, Cursor cursor, Object surface, Paint paint, Source<T> source, Listener<T> listener) {
        if (scope == null || !scope.key.equals(key)) { stop(); scope = new Scope<>(key, source, listener); }
        Scope<T> current = scope;
        boolean changed = !cursor.equals(current.cursor);
        if (changed || current.surface != surface && current.attempt != null && !current.attempt.started) {
            cancel(current); current.consumed = false;
        }
        current.cursor = cursor; current.surface = surface; current.paint = paint;
        schedule(current);
    }

    boolean failed() { return scope != null && scope.failed; }

    void retry() {
        if (scope == null || !scope.failed) return;
        scope.failed = false; scope.consumed = false; schedule(scope);
    }

    void stop() { Scope<T> old = scope; scope = null; if (old != null) cancel(old); }

    void suspend() {
        Scope<T> current = scope;
        if (current != null && current.attempt != null) { cancel(current); current.consumed = false; }
    }

    private void cancel(Scope<T> current) {
        Attempt attempt = current.attempt; current.attempt = null;
        if (attempt != null && attempt.cancelPaint != null) attempt.cancelPaint.run();
    }

    private void schedule(Scope<T> current) {
        if (current.failed || current.consumed || !current.cursor.available()) return;
        Cursor cursor = current.cursor;
        Attempt attempt = new Attempt(); current.attempt = attempt; current.consumed = true;
        attempt.cancelPaint = current.paint.afterPaint(() -> {
            if (scope != current || current.attempt != attempt) return;
            attempt.started = true;
            worker.execute(() -> {
                if (scope != current || current.attempt != attempt) return;
                T result = null; Exception failure = null;
                try {
                    result = current.source.read(cursor);
                    Cursor next = current.source.cursor(result);
                    if (!cursor.id.equals(next.id) || next.more && (next.before.isEmpty() || next.before.equals(cursor.before)))
                        throw new IllegalArgumentException("Invalid history cursor");
                } catch (Exception error) { failure = error; }
                T page = result; Exception error = failure;
                scheduler.post(() -> {
                    if (scope != current || current.attempt != attempt) return;
                    current.attempt = null;
                    if (error == null) current.listener.received(cursor, page);
                    else { current.failed = true; current.listener.failed(error); }
                });
            });
        });
    }
}
