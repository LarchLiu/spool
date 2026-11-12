package com.cloudgeek.spool.android;

import android.os.Handler;
import java.util.concurrent.Executor;
import org.json.JSONObject;

/** An explicitly created, revocable view. Late connect replies are always disconnected. */
final class NativeDesktopConnection implements AutoCloseable {
    interface Wire {
        void send(String command) throws Exception;
        default void image(byte[] png) throws Exception { throw new UnsupportedOperationException("Clipboard images unavailable"); }
        void close();
    }
    interface Listener {
        void opened();
        void message(byte[] data, boolean binary);
        void ended();
    }
    interface Transport {
        JSONObject request(String path, boolean post) throws Exception;
        Wire open(String token, Listener listener) throws Exception;
    }
    interface Events {
        void message(byte[] data, boolean binary);
        void failed(String reason);
    }
    private final Transport transport;
    private final Executor io;
    private final Handler main;
    private final Events events;
    private volatile boolean closed;
    private boolean started;
    private String token;
    private Wire wire;
    private final java.util.ArrayDeque<String> initialCommands = new java.util.ArrayDeque<>();

    NativeDesktopConnection(Transport transport, Executor io, Handler main, Events events) {
        this.transport = transport; this.io = io; this.main = main; this.events = events;
    }
    synchronized void start() {
        if (closed || started) return;
        started = true;
        io.execute(() -> {
            if (closed) return;
            try {
                String created = transport.request("/desktop/connect", true).getString("token");
                if (!created.matches("[A-Za-z0-9_-]{32,128}")) throw new IllegalArgumentException("Invalid desktop token");
                boolean cancelled;
                synchronized (this) { cancelled = closed; if (!cancelled) token = created; }
                if (cancelled) { disconnect(created); return; }
                Wire opened = transport.open(created, new Listener() {
                    public void opened() {} // The transport starts with capture/audio disabled.
                    public void message(byte[] data, boolean binary) { if (!closed) events.message(data, binary); }
                    public void ended() { fail("Desktop connection ended"); }
                });
                synchronized (this) {
                    if (closed) opened.close();
                    else {
                        wire = opened;
                        while (!initialCommands.isEmpty()) wire.send(initialCommands.remove());
                    }
                }
                main.post(() -> { if (!closed) poll.run(); });
            } catch (Exception error) { fail("Could not connect to desktop"); }
        });
    }
    synchronized void send(String command) {
        if (closed) return;
        if (wire == null) {
            if (!started) return;
            if (initialCommands.size() < 64) initialCommands.add(command);
            else fail("Desktop connection ended");
            return;
        }
        try { wire.send(command); }
        catch (Exception error) { fail("Desktop connection ended"); }
    }
    synchronized boolean image(byte[] png) {
        if (closed || wire == null) return false;
        try { wire.image(png); return true; }
        catch (Exception error) { fail("Desktop connection ended"); return false; }
    }
    private void fail(String reason) {
        main.post(() -> {
            if (closed) return;
            close(); events.failed(reason);
        });
    }
    private final Runnable poll = new Runnable() {
        public void run() {
            if (closed) return;
            final String current;
            synchronized (NativeDesktopConnection.this) { current = token; }
            if (current == null) return;
            io.execute(() -> {
                if (closed) return;
                try {
                    boolean available = transport.request("/desktop?token=" + current, false).optBoolean("available");
                    if (!available) { fail("Desktop connection ended"); return; }
                    main.post(() -> { if (!closed) main.postDelayed(this, 5000); });
                } catch (Exception error) { fail("Desktop connection ended"); }
            });
        }
    };
    private void disconnect(String created) {
        try { transport.request("/desktop/disconnect?token=" + created, true); }
        catch (Exception ignored) {} // Socket closure still terminates the helper connection.
    }
    @Override public void close() {
        final String created;
        final Wire opened;
        synchronized (this) {
            if (closed) return;
            closed = true;
            created = token; token = null;
            opened = wire; wire = null;
            initialCommands.clear();
        }
        main.removeCallbacks(poll);
        if (opened != null) opened.close();
        if (created != null) io.execute(() -> disconnect(created));
    }
}
