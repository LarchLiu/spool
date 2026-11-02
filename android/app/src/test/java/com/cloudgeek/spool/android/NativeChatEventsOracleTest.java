package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonNull;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

/** Actual Web async traces, with a blocked worker read so SSE can win a real in-flight race. */
public class NativeChatEventsOracleTest {
    private static final class Dispatch implements NativeChatRefresh.Dispatcher {
        final ConcurrentLinkedQueue<Runnable> tasks = new ConcurrentLinkedQueue<>();
        final List<Runnable> timers = new ArrayList<>();
        public void post(Runnable task) { tasks.add(task); }
        public void postDelayed(Runnable task, long millis) { timers.add(task); }
        public void remove(Runnable task) { timers.removeIf(item -> item == task); }
        void flush() { Runnable task; while ((task = tasks.poll()) != null) task.run(); }
    }
    private static final class Read {
        final CountDownLatch finish = new CountDownLatch(1);
        String value, error;
        boolean settled;
    }
    private static final class Harness implements AutoCloseable {
        final ExecutorService worker = Executors.newSingleThreadExecutor();
        final Dispatch ui = new Dispatch();
        final NativeChatEvents<String> subscription = new NativeChatEvents<>(worker, ui);
        final List<NativeChatEvents.Events<String>> connections = new ArrayList<>();
        final List<Read> reads = new CopyOnWriteArrayList<>();
        final AtomicInteger auth = new AtomicInteger();
        final JsonArray events = new JsonArray(), errors = new JsonArray();
        Boolean connected;
        Harness() {
            subscription.bind("chat-one", new NativeChatEvents.Source<String>() {
                public Runnable open(NativeChatEvents.Events<String> receiver) { connections.add(receiver); return () -> {}; }
                public String snapshot() throws Exception {
                    Read read = new Read(); reads.add(read);
                    if (!read.finish.await(5, TimeUnit.SECONDS)) throw new IllegalStateException("Unresolved fixture read");
                    if (read.error != null) throw new Exception(read.error);
                    return read.value;
                }
                public void checkAuth() { auth.incrementAndGet(); }
            }, new NativeChatEvents.Listener<String>() {
                public void event(String kind, String value) { JsonArray event = new JsonArray(); event.add(kind); event.add(JsonParser.parseString(value)); events.add(event); }
                public void connected(boolean value) { connected = value; }
                public void failed(Exception error) { errors.add(error.getMessage()); }
                public void authFailed(Exception error) { throw new AssertionError(error); }
            });
        }
        void act(JsonObject action) throws Exception {
            String kind = action.get("type").getAsString();
            NativeChatEvents.Events<String> receiver = connections.get(connections.size() - 1);
            switch (kind) {
                case "initial": break;
                case "error": receiver.error(); break;
                case "close": subscription.stop(); break;
                case "reconnect": if (!ui.timers.isEmpty()) ui.timers.remove(0).run(); break;
                case "resolve": case "reject": {
                    Read read = reads.stream().filter(value -> !value.settled).findFirst().orElseThrow(); read.settled = true;
                    if (kind.equals("resolve")) read.value = action.get("value").toString();
                    else read.error = action.get("value").getAsString();
                    read.finish.countDown(); worker.submit(() -> {}).get(5, TimeUnit.SECONDS); break;
                }
                default: receiver.event(kind, action.get("value").toString());
            }
            ui.flush();
            JsonObject expected = action.getAsJsonObject("expected");
            long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
            while ((reads.size() < expected.get("snapshotRequests").getAsInt() || auth.get() < expected.get("authRequests").getAsInt()) && System.nanoTime() < deadline) Thread.sleep(1);
            JsonObject actual = new JsonObject(); actual.add("events", events); actual.add("errors", errors);
            actual.addProperty("snapshotRequests", reads.size()); actual.addProperty("authRequests", auth.get());
            if (connected == null) actual.add("connected", JsonNull.INSTANCE); else actual.addProperty("connected", connected);
            assertEquals("Exact actual-Web state after " + kind, expected, actual);
        }
        public void close() throws Exception {
            subscription.stop(); for (Read read : reads) read.finish.countDown();
            worker.shutdown(); assertTrue("Owned fixture worker must finish", worker.awaitTermination(5, TimeUnit.SECONDS));
            assertTrue(ui.timers.isEmpty());
        }
    }

    @Test public void productionCoordinatorMatchesEveryActualWebFallbackAndRevocationState() throws Exception {
        JsonArray cases;
        try (InputStream input = getClass().getResourceAsStream("/web-chat-events.json")) {
            assertNotNull(input); cases = JsonParser.parseString(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getAsJsonObject().getAsJsonArray("cases");
        }
        assertEquals(7, cases.size());
        for (JsonElement item : cases) try (Harness h = new Harness()) {
            for (JsonElement action : item.getAsJsonObject().getAsJsonArray("records")) h.act(action.getAsJsonObject());
        }
    }
}
