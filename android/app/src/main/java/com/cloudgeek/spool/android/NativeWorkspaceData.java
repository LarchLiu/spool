package com.cloudgeek.spool.android;

import java.util.concurrent.Executor;
import org.json.JSONArray;
import org.json.JSONObject;

/** Web's batched navigation fallback and push-fenced profile loads, scoped to a live session. */
final class NativeWorkspaceData {
    interface Dispatcher {
        void post(Runnable task);
        void postDelayed(Runnable task, long millis);
        void remove(Runnable task);
    }
    interface Source { Object read(String path) throws Exception; }
    interface Listener {
        void navigation(JSONObject value);
        void agents(JSONArray value);
        void profiles(JSONObject value) throws Exception;
        void failed(Exception error, boolean initialNavigation);
    }
    private static final class Batch {
        final long version;
        final boolean initial;
        JSONObject navigation;
        JSONArray agents;
        Batch(long version, boolean initial) { this.version = version; this.initial = initial; }
    }
    private static final class Scope {
        final String key;
        final Source source;
        final Listener listener;
        long navigationVersion;
        Batch batch;
        Object profileRequest;
        boolean ready;
        String missing = "";
        Scope(String key, Source source, Listener listener) { this.key = key; this.source = source; this.listener = listener; }
    }
    private interface Result { void receive(Object value, Exception error); }
    private final Executor worker;
    private final Dispatcher dispatcher;
    private final NativeProfiles profiles;
    private volatile Scope scope;
    private final Runnable fallback = this::tick;
    private final Runnable missing = this::loadProfiles;

    NativeWorkspaceData(Executor worker, Dispatcher dispatcher, NativeProfiles profiles) {
        this.worker = worker; this.dispatcher = dispatcher; this.profiles = profiles;
    }

    boolean bind(String key, Source source, Listener listener) {
        if (scope != null && scope.key.equals(key)) return false;
        stop(); scope = new Scope(key, source, listener);
        load(true); dispatcher.postDelayed(fallback, 15000);
        return true;
    }

    void reload() { load(false); }
    void ready(boolean ready) { if (scope != null) scope.ready = ready; }

    void navigation(JSONObject value) {
        Scope captured = scope;
        if (captured == null) return;
        captured.navigationVersion++;
        captured.listener.navigation(value);
    }

    void missing(String key) {
        if (scope == null || scope.missing.equals(key)) return;
        scope.missing = key; dispatcher.remove(missing);
        if (!key.isEmpty()) dispatcher.postDelayed(missing, 50);
    }

    void stop() { scope = null; dispatcher.remove(fallback); dispatcher.remove(missing); }

    private void tick() {
        Scope captured = scope;
        if (captured == null) return;
        if (!captured.ready) load(false);
        if (scope == captured) dispatcher.postDelayed(fallback, 15000);
    }

    private void read(Scope captured, String path, Result result) {
        worker.execute(() -> {
            if (scope != captured) return;
            Object value = null; Exception error = null;
            try { value = captured.source.read(path); } catch (Exception failure) { error = failure; }
            Object received = value; Exception failure = error;
            dispatcher.post(() -> { if (scope == captured) result.receive(received, failure); });
        });
    }

    private void load(boolean initial) {
        Scope captured = scope;
        if (captured == null || captured.batch != null) return;
        Batch batch = new Batch(captured.navigationVersion, initial); captured.batch = batch;
        loadProfiles();
        read(captured, "/navigation", (value, error) -> complete(captured, batch, value, error, true));
        read(captured, "/agents", (value, error) -> complete(captured, batch, value, error, false));
    }

    private void complete(Scope captured, Batch batch, Object value, Exception error, boolean navigation) {
        if (captured.batch != batch) {
            if (error != null) captured.listener.failed(error, false);
            return;
        }
        if (error == null && !(navigation ? value instanceof JSONObject : value instanceof JSONArray))
            error = new IllegalArgumentException("Invalid workspace response");
        if (error != null) {
            captured.batch = null;
            captured.listener.failed(error, batch.initial && batch.version == captured.navigationVersion);
            return;
        }
        if (navigation) batch.navigation = (JSONObject) value; else batch.agents = (JSONArray) value;
        if (batch.navigation == null || batch.agents == null) return;
        captured.batch = null;
        if (batch.version != captured.navigationVersion) return;
        navigation(batch.navigation);
        if (scope == captured) captured.listener.agents(batch.agents);
    }

    private void loadProfiles() {
        Scope captured = scope;
        if (captured == null || captured.profileRequest != null) return;
        Object request = new Object(); captured.profileRequest = request;
        long version = profiles.epoch();
        read(captured, "/profiles", (value, error) -> {
            if (captured.profileRequest != request) return;
            captured.profileRequest = null;
            if (error == null && version != profiles.epoch()) return;
            try {
                if (error != null) throw error;
                if (!(value instanceof JSONObject)) throw new IllegalArgumentException("Invalid profiles response");
                captured.listener.profiles((JSONObject) value);
            } catch (Exception failure) { captured.listener.failed(failure, false); }
        });
    }
}
