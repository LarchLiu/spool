package com.cloudgeek.spool.android;

import java.util.concurrent.Executor;

/** Foreground health/recovery policy. Control and listener callbacks run on the UI thread. */
final class ConnectionRecovery {
    static final long QUIET_CHECK_MILLIS = 60000;
    interface Scheduler extends ConnectionMonitor.Dispatcher { long now(); }
    interface Subscription { void close(); }
    interface Source {
        boolean online();
        Snapshot check() throws Exception;
        void refreshNetwork() throws Exception;
        void reconnect() throws Exception;
        void login() throws Exception;
        default Subscription subscribe(Runnable changed) throws Exception { return null; }
        default long responseAgeMillis() { return -1; }
    }
    interface Listener { void update(Snapshot value, boolean available); }
    private interface Action { void run() throws Exception; }
    static final class Snapshot {
        final String state, https, problem, authURL, message;
        Snapshot(String state, String https, String problem, String authURL, String message) {
            this.state = state; this.https = https; this.problem = problem;
            this.authURL = authURL; this.message = message;
        }
        boolean healthy() { return problem.isEmpty() && https.equals("Verified"); }
    }

    private final Executor worker;
    private final Scheduler scheduler;
    private final Runnable refresh = this::refresh;
    private Source source;
    private Listener listener;
    private Subscription subscription;
    private long subscriptionGeneration;
    private volatile long generation;
    private boolean active, busy, available, loginRequested, refreshed, checkRequested;
    private int failures, retryAttempts, resetAttempts;
    private long firstFailure, nextReset;

    ConnectionRecovery(Executor worker, Scheduler scheduler) {
        this.worker = worker; this.scheduler = scheduler;
    }

    void start(Source source, Listener listener) {
        stop(); this.source = source; this.listener = listener;
        checkRequested = true;
        subscribe(); refresh();
    }

    void stop() {
        ++generation; scheduler.remove(refresh);
        unsubscribe();
        source = null; listener = null; available = loginRequested = refreshed = checkRequested = false;
        failures = retryAttempts = resetAttempts = 0; firstFailure = nextReset = 0;
    }

    void setActive(boolean value) {
        if (active == value) return;
        active = value; ++generation; scheduler.remove(refresh);
        if (active) { failures = retryAttempts = 0; refreshed = false; checkRequested = true; subscribe(); refresh(); }
        else unsubscribe();
    }

    private void subscribe() {
        if (!active || source == null || subscription != null) return;
        Source captured = source;
        long token = ++subscriptionGeneration;
        try {
            subscription = captured.subscribe(() -> scheduler.post(() -> {
                if (active && source == captured && subscriptionGeneration == token) checkNow();
            }));
        } catch (Exception ignored) {
            // A client without notifications retains the bounded health fallback.
        }
    }

    private void unsubscribe() {
        ++subscriptionGeneration;
        Subscription previous = subscription; subscription = null;
        if (previous != null) previous.close();
    }

    void checkNow() {
        // A pre-switch/pre-resume sample cannot trigger recovery on the new network.
        ++generation; failures = retryAttempts = 0; refreshed = false;
        checkRequested = true;
        scheduler.remove(refresh); refresh();
    }

    void suspect() {
        // API/stream failures can advance a healthy timer, but cannot defeat
        // failure backoff or keep invalidating an in-flight health request.
        if (!active || busy || retryAttempts != 0) return;
        checkRequested = true;
        scheduler.remove(refresh); refresh();
    }

    private void refresh() {
        if (!active || busy || source == null) return;
        Source captured = source;
        if (!captured.online()) {
            available = false; failures = 0; refreshed = false;
            long token = generation;
            listener.update(new Snapshot("Unknown", "Not checked", "offline", "", "Waiting for network"), false);
            if (active && source == captured && token == generation) scheduler.postDelayed(refresh, 5000);
            return;
        }
        if (!checkRequested && available && retryAttempts == 0) {
            long age = captured.responseAgeMillis();
            if (age >= 0 && age < QUIET_CHECK_MILLIS) {
                scheduler.remove(refresh);
                scheduler.postDelayed(refresh, QUIET_CHECK_MILLIS - age);
                return;
            }
        }
        checkRequested = false;
        busy = true;
        long token = generation;
        worker.execute(() -> {
            if (generation != token) { scheduler.post(() -> finish(token, 0)); return; }
            Snapshot value;
            try { value = captured.check(); }
            catch (Exception error) { value = new Snapshot("Unknown", "Not checked", "status", "", error.getMessage()); }
            Snapshot result = value;
            scheduler.post(() -> receive(token, captured, result));
        });
    }

    private void receive(long token, Source captured, Snapshot value) {
        if (generation != token || !active || source != captured) { finish(token, 0); return; }
        boolean recoverable = value.problem.equals("network") || value.problem.equals("status") || value.problem.equals("starting");
        if (value.healthy()) {
            available = true; failures = retryAttempts = resetAttempts = 0; refreshed = false; nextReset = 0;
        } else {
            retryAttempts = Math.min(5, retryAttempts + 1);
            if (recoverable) {
                if (failures++ == 0) firstFailure = scheduler.now();
            } else failures = 0;
            if (!recoverable || failures >= 2) available = false;
            if (!recoverable) refreshed = false;
        }
        if (!value.problem.equals("login")) loginRequested = false;
        listener.update(value, available);
        // A listener may disconnect or replace the selected server.
        if (generation != token || !active || source != captured) { finish(token, 0); return; }
        long delay = subscription != null && (value.problem.equals("login") || value.problem.equals("approval")) ? -1
                : value.healthy() ? QUIET_CHECK_MILLIS : value.problem.equals("login") ? 1500
                : value.problem.equals("approval") ? 5000 : Math.min(30000, 2000L << Math.max(0, retryAttempts - 1));
        Action action = null;
        boolean loggingIn = value.problem.equals("login") && !loginRequested;
        if (loggingIn) {
            action = captured::login;
        } else if (recoverable && failures >= 3 && scheduler.now() - firstFailure >= 30000 && scheduler.now() >= nextReset) {
            nextReset = scheduler.now() + Math.min(300000, 60000L << Math.min(3, resetAttempts++));
            refreshed = true;
            action = captured::reconnect;
        } else if (recoverable && failures >= 2 && !refreshed) {
            refreshed = true;
            action = captured::refreshNetwork;
        }
        if (action == null) { finish(token, delay); return; }
        Action recovery = action;
        worker.execute(() -> {
            boolean failed = false, ran = false;
            try { if (generation == token) { ran = true; recovery.run(); } }
            catch (Exception ignored) { failed = true; }
            boolean actionSucceeded = ran && !failed;
            scheduler.post(() -> {
                if (source == captured && loggingIn) loginRequested = actionSucceeded;
                // Only a subsequent health response can establish recovery.
                finish(token, loggingIn && !actionSucceeded && delay < 0 ? 1500 : delay);
            });
        });
    }

    private void finish(long token, long delay) {
        busy = false;
        if (active && source != null) {
            scheduler.remove(refresh);
            long nextDelay = generation == token ? delay : 0;
            if (nextDelay >= 0) scheduler.postDelayed(refresh, nextDelay);
        }
    }
}
