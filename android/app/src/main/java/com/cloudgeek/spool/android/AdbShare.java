package com.cloudgeek.spool.android;

import java.util.function.Consumer;
import java.util.function.IntConsumer;

/**
 * Shares this phone's wireless debugging with the adb of one Spool server.
 * Every method and callback runs on the main thread. A share survives lost
 * networks and replaced routes; it ends when the user stops it, the server
 * refuses it. If the phone is not paired, it waits for the user to pair it.
 */
final class AdbShare {
    enum Phase { IDLE, CONNECTING, UNAUTHORIZED, ONLINE, WAITING, UNPAIRED, PAIRING, FAILED }
    interface Link { void close(); }
    /** ready(serial), state(name), paired, closed(code, message). */
    interface Events { void event(String kind, String value, String detail); }
    interface Host {
        /** Finds the port wireless debugging listens on; 0 when it is not advertised. */
        void port(boolean pairing, IntConsumer found);
        void cancelDiscovery();
        Link share(int port, Events events) throws Exception;
        Link pair(String code, int port, Events events) throws Exception;
        /** "" when sharing is available, the server's reason when not, null when unknown. */
        void availability(Consumer<String> result);
        void later(Runnable task, long millis);
        void cancel(Runnable task);
        long now();
        double random();
    }

    private static final long RETRY_BASE = 1000, RETRY_LIMIT = 30000, STEADY_AFTER = 30000;
    private final Host host;
    private final Runnable changed;
    private final Runnable retry = this::resume;
    private Link link;
    private int epoch, attempts, manualPort;
    private long onlineSince = -1;
    private boolean established;
    Phase phase = Phase.IDLE;
    /** How programs on the server address the phone, once known. */
    String serial = "";
    /** Why the share waits, needs attention or failed. */
    String code = "", detail = "";

    AdbShare(Host host, Runnable changed) { this.host = host; this.changed = changed; }

    /** True while the phone is offered, or about to be, to the server. */
    boolean active() { return phase != Phase.IDLE && phase != Phase.FAILED; }

    /** port is 0 to use the port the phone advertises. */
    void start(int port) {
        if (active()) return;
        manualPort = port; attempts = 0; established = false; serial = "";
        attempt();
    }

    void stop() {
        if (phase == Phase.IDLE) return;
        release(); established = false; serial = "";
        set(Phase.IDLE, "", "");
    }

    void notificationUnavailable() { fail("notification_unavailable", ""); }

    void foregroundUnavailable() {
        if (active()) fail("foreground_unavailable", "");
    }

    /** Pairs the server's adb key with this phone, then shares it. */
    void pair(String pairingCode, int port) {
        if (phase != Phase.UNPAIRED) return;
        String digits = pairingCode == null ? "" : pairingCode.trim();
        if (!digits.matches("[0-9]{6,12}")) { set(Phase.UNPAIRED, "pair_failed", ""); return; }
        int token = release();
        set(Phase.PAIRING, "", "");
        if (token != epoch) return;
        IntConsumer open = found -> {
            if (token != epoch) return;
            if (found <= 0) { set(Phase.UNPAIRED, "pairing_unreachable", ""); return; }
            try {
                use(token, host.pair(digits, found, (kind, value, text) -> {
                    if (token != epoch) return;
                    if (kind.equals("paired")) { attempts = 0; attempt(); }
                    else if (kind.equals("closed")) pairingEnded(value, text);
                }));
            } catch (Exception error) { if (token == epoch) pairingEnded("network", ""); }
        };
        if (port > 0) open.accept(port); else host.port(true, open);
    }

    private void pairingEnded(String reason, String text) {
        switch (reason) {
            case "device_unreachable": reason = "pairing_unreachable"; break;
            case "unpaired": case "device_closed": reason = "pair_failed"; break;
            case "pair_failed": case "pairing_unreachable": case "network": case "busy": break;
            default: fail(reason, text); return;
        }
        release(); set(Phase.UNPAIRED, reason, text);
    }

    private void attempt() {
        int token = release();
        set(Phase.CONNECTING, "", "");
        if (token != epoch) return;
        IntConsumer open = found -> {
            if (token != epoch) return;
            if (found <= 0) { unreachable(); return; }
            try { use(token, host.share(found, (kind, value, text) -> { if (token == epoch) event(kind, value, text); })); }
            catch (Exception error) { if (token == epoch) pause("network"); }
        };
        if (manualPort > 0) open.accept(manualPort); else host.port(false, open);
    }

    private void event(String kind, String value, String text) {
        if (kind.equals("ready")) { serial = value; changed.run(); return; }
        if (kind.equals("state")) {
            if (value.equals("online")) { established = true; onlineSince = host.now(); set(Phase.ONLINE, "", ""); }
            else if (value.equals("unauthorized")) set(Phase.UNAUTHORIZED, "", "");
            return;
        }
        if (!kind.equals("closed")) return;
        switch (value) {
            case "unpaired": release(); set(Phase.UNPAIRED, "", ""); break;
            case "device_unreachable": unreachable(); break;
            case "network": case "adb_closed": case "stopping": case "device_closed": pause(value); break;
            default: fail(value, text);
        }
    }

    // Wireless debugging that was never reached is for the user to turn on; one
    // that went away, as it does when Wi-Fi drops, is waited for.
    private void unreachable() {
        if (established) pause("device_unreachable"); else fail("device_unreachable", "");
    }

    private void pause(String reason) {
        if (onlineSince >= 0 && host.now() - onlineSince >= STEADY_AFTER) attempts = 0;
        onlineSince = -1;
        int token = release();
        long delay = Math.min(RETRY_LIMIT, RETRY_BASE << Math.min(attempts++, 5));
        set(Phase.WAITING, reason, "");
        if (token == epoch) host.later(retry, Math.round(delay * (0.8 + 0.4 * host.random())));
    }

    private void resume() {
        int token = epoch;
        host.availability(reason -> {
            if (token != epoch || phase != Phase.WAITING) return;
            if (reason == null) pause("network");
            else if (!reason.isEmpty()) fail(reason, "");
            else attempt();
        });
    }

    private void fail(String reason, String text) {
        release(); established = false; serial = "";
        set(Phase.FAILED, reason, text == null ? "" : text);
    }

    private void use(int token, Link opened) {
        if (token == epoch) link = opened;
        else opened.close();
    }

    /** Invalidates everything still in flight before closing it. */
    private int release() {
        int token = ++epoch;
        host.cancel(retry);
        host.cancelDiscovery();
        Link current = link; link = null;
        if (current != null) current.close();
        return token;
    }

    private void set(Phase next, String reason, String text) {
        phase = next; code = reason; detail = text;
        changed.run();
    }
}
