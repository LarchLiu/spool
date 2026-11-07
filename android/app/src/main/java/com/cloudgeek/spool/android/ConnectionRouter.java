package com.cloudgeek.spool.android;

import java.util.List;
import java.util.concurrent.Executor;
import java.util.function.Consumer;

/** One live network per server. Switching closes the old transport before opening the next. */
final class ConnectionRouter<T> {
    static final class Session {
        final String userID, json;
        Session(String userID, String json) { this.userID = userID; this.json = json; }
    }
    interface Backend<T> {
        T open(ConnectionPlan.Endpoint endpoint) throws Exception;
        ConnectionRecovery.Snapshot health(T client) throws Exception;
        Session session(T client) throws Exception;
        String login(T client) throws Exception;
        default void networkChanged(T client) {}
        void close(T client);
    }
    interface Listener<T> {
        default void replacing(T client) {}
        void sampled(ConnectionPlan.Endpoint endpoint, ConnectionRecovery.Snapshot health, boolean signedIn);
        void selected(Candidate<T> candidate);
        void unavailable();
    }
    static final class Candidate<T> {
        final ConnectionPlan.Endpoint endpoint;
        final T client;
        final ConnectionRecovery.Snapshot health;
        final Session session;
        final boolean sessionChecked;
        Candidate(ConnectionPlan.Endpoint endpoint, T client, ConnectionRecovery.Snapshot health, Session session) {
            this(endpoint, client, health, session, session != null || health != null && health.healthy());
        }
        Candidate(ConnectionPlan.Endpoint endpoint, T client, ConnectionRecovery.Snapshot health, Session session, boolean sessionChecked) {
            this.endpoint = endpoint; this.client = client; this.health = health; this.session = session; this.sessionChecked = sessionChecked;
        }
    }
    private final Executor worker;
    private final Consumer<Runnable> dispatch;
    private final Backend<T> backend;
    private Candidate<T> current;
    private volatile long generation;

    ConnectionRouter(Executor worker, Consumer<Runnable> dispatch, Backend<T> backend) {
        this.worker = worker; this.dispatch = dispatch; this.backend = backend;
    }

    void select(List<ConnectionPlan.Endpoint> endpoints, String userID, boolean allowLogin, boolean allowUnhealthy, Listener<T> listener) {
        long token = ++generation;
        worker.execute(() -> {
            ConnectionPlan.Endpoint needsLogin = null, unavailable = null;
            for (ConnectionPlan.Endpoint endpoint : endpoints) {
                if (generation != token) return;
                Candidate<T> candidate = sample(endpoint, userID, token, listener);
                if (generation != token) return;
                if (candidate.client == null) continue;
                if (candidate.health.healthy()) {
                    if (candidate.session != null && (userID.isEmpty() || userID.equals(candidate.session.userID))) {
                        publish(token, () -> listener.selected(candidate)); return;
                    }
                    if (!candidate.sessionChecked) {
                        publish(token, () -> listener.selected(candidate)); return;
                    }
                    if (needsLogin == null) needsLogin = endpoint;
                } else if (unavailable == null) unavailable = endpoint;
            }
            ConnectionPlan.Endpoint endpoint = (allowLogin || userID.isEmpty()) && needsLogin != null ? needsLogin : allowUnhealthy ? unavailable : null;
            if (endpoint == null) { publish(token, listener::unavailable); return; }
            Candidate<T> candidate = current != null && current.endpoint.equals(endpoint) ? current : sample(endpoint, userID, token, listener);
            if (candidate.client == null || !candidate.health.healthy() && !allowUnhealthy) publish(token, listener::unavailable);
            else publish(token, () -> listener.selected(candidate));
        });
    }

    private Candidate<T> sample(ConnectionPlan.Endpoint endpoint, String userID, long token, Listener<T> listener) {
        T client = null;
        ConnectionRecovery.Snapshot health;
        Session session = null;
        boolean sessionChecked = false;
        try {
            if (current != null && !current.endpoint.equals(endpoint)) {
                T previous = current.client;
                publish(token, () -> listener.replacing(previous));
                backend.close(previous); current = null;
            }
            if (generation != token) throw new java.io.IOException("Connection selection cancelled");
            client = current == null ? backend.open(endpoint) : current.client;
            current = new Candidate<>(endpoint, client, null, null);
            health = backend.health(client);
            if (health.healthy()) {
                try {
                    session = backend.session(client);
                    sessionChecked = true;
                    if (!userID.isEmpty() && session != null && !userID.equals(session.userID)) session = null;
                } catch (Exception ignored) {
                    // Failed authentication reads do not change measured network health.
                    // The Activity retries /me while keeping workspace access blocked.
                }
            }
        } catch (Exception error) {
            health = new ConnectionRecovery.Snapshot("Unknown", "Failed", "network", "", error.getMessage());
        }
        Candidate<T> candidate = new Candidate<>(endpoint, client, health, session, sessionChecked);
        if (client != null) current = candidate;
        publish(token, () -> listener.sampled(endpoint, candidate.health, candidate.session != null));
        return candidate;
    }

    private void publish(long token, Runnable task) { dispatch.accept(() -> { if (generation == token) task.run(); }); }
    void cancel() { ++generation; }
    void networkChanged() { worker.execute(() -> { if (current != null) backend.networkChanged(current.client); }); }
    void login(ConnectionPlan.Endpoint endpoint, Consumer<String> result, Consumer<Exception> failed) {
        long token = generation;
        worker.execute(() -> {
            try {
                if (generation != token) return;
                if (current == null || !current.endpoint.equals(endpoint)) throw new java.io.IOException("Connect to Tailscale before signing in");
                String url = backend.login(current.client);
                publish(token, () -> result.accept(url));
            } catch (Exception error) { publish(token, () -> failed.accept(error)); }
        });
    }
    void stop() {
        cancel();
        worker.execute(() -> { if (current != null) { backend.close(current.client); current = null; } });
    }
}
