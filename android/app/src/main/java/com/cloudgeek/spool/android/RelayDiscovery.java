package com.cloudgeek.spool.android;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.os.Handler;
import android.os.Looper;
import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.HashMap;
import java.util.Map;
import java.util.function.BiConsumer;

/** Short platform DNS-SD scans. Discovered addresses never authenticate a server. */
final class RelayDiscovery {
    private final NsdManager manager;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final BiConsumer<String, String> found;
    private final Map<String, String> addresses = new HashMap<>();
    private NsdManager.DiscoveryListener discovery;
    private int epoch;
    private long lastScan = -1;
    private final Runnable finish = this::stopScan;

    RelayDiscovery(Context context, BiConsumer<String, String> found) {
        this.manager = context.getSystemService(NsdManager.class); this.found = found;
    }
    synchronized String addresses(String id) { return addresses.getOrDefault(id, ""); }

    void refresh() {
        if (manager == null || discovery != null ||
                (lastScan >= 0 && android.os.SystemClock.elapsedRealtime() - lastScan < 1500)) return;
        lastScan = android.os.SystemClock.elapsedRealtime();
        final int generation = ++epoch;
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
                        byte[] key = service.getAttributes().get("id"); InetAddress host = service.getHost();
                        if (key != null && host != null && service.getPort() > 0 && service.getPort() <= 65535) {
                            String id = new String(key, StandardCharsets.US_ASCII);
                            if (id.matches("[a-z2-7]{52}") && (host.isSiteLocalAddress() || host.isLinkLocalAddress())) {
                                String ip = host.getHostAddress();
                                String address = (ip.contains(":") ? "[" + ip + "]" : ip) + ":" + service.getPort();
                                synchronized (RelayDiscovery.this) {
                                    if (addresses.size() < 32 || addresses.containsKey(id)) addresses.put(id, address);
                                }
                                found.accept(id, address);
                            }
                        }
                        next();
                    }); }
                }); } catch (RuntimeException error) { resolving = false; next(); }
            }
            public void onDiscoveryStarted(String type) {}
            public void onDiscoveryStopped(String type) {}
            public void onStartDiscoveryFailed(String type, int error) { main.post(() -> { if (generation == epoch) stopScan(); }); }
            public void onStopDiscoveryFailed(String type, int error) {}
            public void onServiceLost(NsdServiceInfo service) {}
            public void onServiceFound(NsdServiceInfo service) { main.post(() -> {
                if (generation != epoch || !service.getServiceType().startsWith("_spool._tcp") || queue.size() >= 16) return;
                queue.add(service); next();
            }); }
        };
        try { manager.discoverServices("_spool._tcp.", NsdManager.PROTOCOL_DNS_SD, discovery); main.postDelayed(finish, 5000); }
        catch (RuntimeException error) { stopScan(); }
    }
    private void stopScan() {
        main.removeCallbacks(finish); ++epoch;
        NsdManager.DiscoveryListener current = discovery; discovery = null;
        if (current != null) try { manager.stopServiceDiscovery(current); } catch (RuntimeException ignored) {}
    }
    void networkChanged() { stopScan(); synchronized (this) { addresses.clear(); } lastScan = -1; refresh(); }
    void close() { stopScan(); synchronized (this) { addresses.clear(); } }
}
