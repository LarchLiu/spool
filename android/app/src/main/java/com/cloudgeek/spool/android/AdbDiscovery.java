package com.cloudgeek.spool.android;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.os.Handler;
import android.os.Looper;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.util.ArrayDeque;
import java.util.function.IntConsumer;

/**
 * Finds the ports of this phone's own wireless debugging, which change every
 * time it is turned on. Services of other devices on the network are ignored:
 * the port is only ever used on loopback.
 */
final class AdbDiscovery {
    static final String CONNECT = "_adb-tls-connect._tcp", PAIRING = "_adb-tls-pairing._tcp";
    private static final long PATIENCE = 3000;
    private final NsdManager manager;
    private final Handler main = new Handler(Looper.getMainLooper());
    private NsdManager.DiscoveryListener discovery;
    private IntConsumer waiting;
    private int epoch;
    private final Runnable timeout = () -> finish(0);

    AdbDiscovery(Context context) { manager = context.getSystemService(NsdManager.class); }

    /** Reports one port, or 0 when nothing is advertised. A new search replaces the previous one. */
    void find(String type, IntConsumer found) {
        finish(0);
        if (manager == null) { found.accept(0); return; }
        waiting = found;
        final int generation = epoch;
        final ArrayDeque<NsdServiceInfo> queue = new ArrayDeque<>();
        discovery = new NsdManager.DiscoveryListener() {
            boolean resolving;
            void next() {
                if (generation != epoch || resolving || queue.isEmpty()) return;
                resolving = true;
                try { manager.resolveService(queue.remove(), new NsdManager.ResolveListener() {
                    public void onResolveFailed(NsdServiceInfo service, int error) { main.post(() -> { resolving = false; next(); }); }
                    public void onServiceResolved(NsdServiceInfo service) { main.post(() -> {
                        resolving = false;
                        if (generation != epoch) return;
                        int port = service.getPort();
                        if (port > 0 && port <= 65535 && own(service.getHost())) finish(port); else next();
                    }); }
                }); } catch (RuntimeException error) { resolving = false; next(); }
            }
            public void onDiscoveryStarted(String serviceType) {}
            public void onDiscoveryStopped(String serviceType) {}
            public void onStartDiscoveryFailed(String serviceType, int error) { main.post(() -> { if (generation == epoch) finish(0); }); }
            public void onStopDiscoveryFailed(String serviceType, int error) {}
            public void onServiceLost(NsdServiceInfo service) {}
            public void onServiceFound(NsdServiceInfo service) { main.post(() -> {
                if (generation != epoch || queue.size() >= 16) return;
                queue.add(service); next();
            }); }
        };
        try { manager.discoverServices(type, NsdManager.PROTOCOL_DNS_SD, discovery); main.postDelayed(timeout, PATIENCE); }
        catch (RuntimeException error) { finish(0); }
    }

    private static boolean own(InetAddress host) {
        try { return host != null && (host.isLoopbackAddress() || NetworkInterface.getByInetAddress(host) != null); }
        catch (Exception error) { return false; }
    }

    private void finish(int port) {
        main.removeCallbacks(timeout); ++epoch;
        NsdManager.DiscoveryListener current = discovery; discovery = null;
        if (current != null) try { manager.stopServiceDiscovery(current); } catch (RuntimeException ignored) {}
        IntConsumer found = waiting; waiting = null;
        if (found != null) found.accept(port);
    }

    void close() { waiting = null; finish(0); }
}
