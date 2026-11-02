package com.cloudgeek.spool.android;

import android.net.Uri;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.Executor;
import org.json.JSONObject;

/** Authorized, bounded per-account tool pages; no bodies are inserted into chat state. */
final class NativeToolDetails {
    static final int PAGE_BYTES = 32 * 1024;
    interface Source { JSONObject get(String path) throws Exception; }
    interface Callback { void received(JSONObject page); void failed(Exception error); }
    private final Executor worker, main;
    private final Source source;
    private final LinkedHashMap<String, JSONObject> cache = new LinkedHashMap<>(16, .75f, true);
    private int bytes;
    private final int maxBytes, maxPages;
    private static final class Timing {
        long requests, hits, lastQueue, lastRequest, lastDelivery, lastRender, maxRender;
        double lastServerMs = -1;
    }
    private final Map<String, Timing> timings = new LinkedHashMap<>();
    private Timing timing(String kind) {
        String key = kind.matches("read|write|create|delete|search|list|web|shell|thinking|thinking_preview") ? kind : "other";
        return timings.computeIfAbsent(key, ignored -> new Timing());
    }

    NativeToolDetails(Executor worker, Executor main, Source source) { this(worker, main, source, 8 * 1024 * 1024, 128); }
    NativeToolDetails(Executor worker, Executor main, Source source, int maxBytes, int maxPages) {
        this.worker = worker; this.main = main; this.source = source; this.maxBytes = maxBytes; this.maxPages = maxPages;
    }
    void load(JSONObject summary, int offset, String prefix, Callback callback) {
        String href = summary.optString("href"), version = summary.optString("version");
        String kind = summary.optString("kind");
        if (!href.matches("/chats/[a-zA-Z0-9_-]+/tool-detail\\?ref=[a-f0-9]{64}")) {
            callback.failed(new IllegalArgumentException("Invalid tool detail reference")); return;
        }
        String path = href + "&offset=" + offset + "&prefix=" + Uri.encode(prefix);
        String key = version + "\n" + path;
        JSONObject hit = cache.get(key);
        if (hit != null) { timing(kind).hits++; callback.received(NativeChatState.copy(hit)); return; }
        long queued = System.nanoTime();
        worker.execute(() -> {
            long started = System.nanoTime();
            try {
                JSONObject page = source.get(path);
                long finished = System.nanoTime();
                main.execute(() -> {
                    Timing timing = timing(kind); timing.requests++;
                    timing.lastQueue = started - queued; timing.lastRequest = finished - started;
                    timing.lastDelivery = System.nanoTime() - finished;
                    timing.lastServerMs = page.optDouble("server_ms", -1);
                    int size = size(key, page);
                    if (size <= maxBytes) {
                        JSONObject old = cache.put(key, NativeChatState.copy(page));
                        if (old != null) bytes -= size(key, old);
                        bytes += size;
                        while (bytes > maxBytes || cache.size() > maxPages) {
                            Map.Entry<String, JSONObject> entry = cache.entrySet().iterator().next();
                            bytes -= size(entry.getKey(), entry.getValue()); cache.remove(entry.getKey());
                        }
                    }
                    callback.received(page);
                });
            } catch (Exception error) { main.execute(() -> callback.failed(error)); }
        });
    }
    void rendered(String kind, long nanos) {
        Timing timing = timing(kind); timing.lastRender = nanos; timing.maxRender = Math.max(timing.maxRender, nanos);
    }
    JSONObject metrics() {
        JSONObject result = new JSONObject();
        try {
            for (Map.Entry<String, Timing> entry : timings.entrySet()) {
                Timing value = entry.getValue();
                result.put(entry.getKey(), new JSONObject().put("requests", value.requests).put("cacheHits", value.hits)
                        .put("lastQueueMs", value.lastQueue / 1e6).put("lastRequestMs", value.lastRequest / 1e6)
                        .put("lastServerMs", value.lastServerMs)
                        .put("lastDeliveryMs", value.lastDelivery / 1e6).put("lastRenderMs", value.lastRender / 1e6).put("maxRenderMs", value.maxRender / 1e6));
            }
        } catch (org.json.JSONException error) { throw new IllegalStateException(error); }
        return result;
    }
    private static int size(String key, JSONObject page) { return key.length() * 2 + page.optString("text").length() * 2 + 256; }

    static JSONObject merge(JSONObject previous, JSONObject next) {
        if (previous == null || next.optBoolean("reset") || next.optInt("offset") != previous.optInt("next")) return next;
        if (next.optInt("next") - previous.optInt("offset") > PAGE_BYTES) {
            JSONObject copy = NativeChatState.copy(previous);
            try { copy.put("total", next.optInt("total")); copy.put("version", next.optString("version")); }
            catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
            return copy;
        }
        JSONObject copy = NativeChatState.copy(next);
        try { copy.put("offset", previous.optInt("offset")); copy.put("text", previous.optString("text") + next.optString("text")); }
        catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
        return copy;
    }
}
