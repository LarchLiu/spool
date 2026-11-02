package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import com.google.gson.*;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;

public class NativeHistoryLoaderTest {
    static NativeHistoryLoader.Cursor cursor(JsonObject page) {
        JsonObject history = page.has("history") ? page.getAsJsonObject("history") : new JsonObject();
        return new NativeHistoryLoader.Cursor(page.get("id").getAsString(), history.has("before") ? history.get("before").getAsString() : "",
                history.has("has_more") && history.get("has_more").getAsBoolean(), history.has("error") ? history.get("error").getAsString() : "");
    }
    static final class Queue implements java.util.concurrent.Executor, NativeHistoryLoader.Scheduler {
        final List<Runnable> tasks = new ArrayList<>();
        public void execute(Runnable task) { tasks.add(task); }
        public void post(Runnable task) { tasks.add(task); }
        void run() { assertEquals(1, tasks.size()); tasks.remove(0).run(); }
    }
    static final class Harness implements NativeHistoryLoader.Source<JsonObject>, NativeHistoryLoader.Listener<JsonObject>, NativeHistoryLoader.Paint {
        final Queue worker = new Queue(), ui = new Queue();
        final NativeHistoryLoader<JsonObject> loader = new NativeHistoryLoader<>(worker, ui);
        final Object surface = new Object();
        final List<Runnable> frames = new ArrayList<>();
        final List<NativeHistoryLoader.Cursor> reads = new ArrayList<>();
        final List<JsonObject> pages = new ArrayList<>(); final List<String> errors = new ArrayList<>();
        JsonObject result; String failure; int yields; boolean stopDuringRead;
        public Runnable afterPaint(Runnable task) { yields++; frames.add(task); return () -> frames.remove(task); }
        public JsonObject read(NativeHistoryLoader.Cursor from) throws Exception {
            reads.add(from); if (stopDuringRead) loader.stop(); if (failure != null) throw new Exception(failure); return result;
        }
        public NativeHistoryLoader.Cursor cursor(JsonObject page) { return NativeHistoryLoaderTest.cursor(page); }
        public void received(NativeHistoryLoader.Cursor from, JsonObject page) { pages.add(page); }
        public void failed(Exception error) { errors.add(error.getMessage()); }
        void bind(String key, JsonObject page) { loader.bind(key, cursor(page), surface, this, this, this); }
        void paint() { assertEquals(1, frames.size()); frames.remove(0).run(); }
        void complete() { paint(); worker.run(); ui.run(); }
    }
    private static JsonObject page(String before, boolean more) {
        return JsonParser.parseString("{id:'chat-one',history:{before:'" + before + "',has_more:" + more + "}}").getAsJsonObject();
    }
    private JsonObject oracle() throws Exception {
        try (InputStream input = getClass().getResourceAsStream("/web-history.json")) {
            assertNotNull(input); return JsonParser.parseString(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getAsJsonObject();
        }
    }

    @Test public void fetchEligibilityValidationAndCancellationMatchActualWeb() throws Exception {
        JsonArray cases = oracle().getAsJsonArray("fetches"); assertEquals(11, cases.size());
        for (JsonElement value : cases) {
            JsonObject item = value.getAsJsonObject(), expected = item.getAsJsonObject("expected"); Harness h = new Harness();
            h.result = item.has("page") ? item.getAsJsonObject("page") : null;
            h.failure = item.has("failure") ? item.get("failure").getAsString() : null;
            h.bind("session", item.getAsJsonObject("from"));
            assertTrue("No fetch before a committed frame", h.reads.isEmpty());
            if (!h.frames.isEmpty()) {
                if (item.has("cancel") && item.get("cancel").getAsString().equals("paint")) h.loader.stop();
                else {
                    h.stopDuringRead = item.has("cancel"); h.complete();
                }
            }
            assertEquals(item.get("id").getAsString(), expected.get("yields").getAsInt(), h.yields);
            assertEquals(item.get("id").getAsString(), expected.getAsJsonArray("requests").size(), h.reads.size());
            if (expected.has("error")) { assertEquals(List.of(expected.get("error").getAsString()), h.errors); assertTrue(h.loader.failed()); }
            else if (!expected.get("page").isJsonNull()) assertEquals(List.of(expected.getAsJsonObject("page")), h.pages);
            else assertTrue(h.pages.isEmpty());
            h.loader.stop(); assertTrue(h.frames.isEmpty());
        }
    }

    @Test public void unchangedRendersCoalesceAndEveryNextPageWaitsForAnotherPaint() {
        Harness h = new Harness(); JsonObject first = page("first", true); h.result = page("second", true);
        h.bind("session", first); h.bind("session", first); assertEquals(1, h.frames.size()); assertEquals(1, h.yields);
        h.complete(); assertEquals(1, h.pages.size()); h.bind("session", first); assertTrue(h.frames.isEmpty());
        h.bind("session", h.result); assertEquals(1, h.frames.size()); assertEquals(1, h.reads.size());
        h.result = page("", false); h.complete(); h.bind("session", h.result); assertTrue(h.frames.isEmpty()); assertEquals(2, h.reads.size());
    }

    @Test public void failedLoadDoesNotLoopAcrossNewCursorsOrRendersAndRetryIsExplicit() {
        Harness h = new Harness(); JsonObject first = page("first", true); h.failure = "offline";
        h.bind("session", first); h.complete(); assertTrue(h.loader.failed());
        h.bind("session", first); h.bind("session", page("second", true)); assertTrue(h.frames.isEmpty());
        h.failure = null; h.result = page("last", false); h.loader.retry(); assertFalse(h.loader.failed()); h.complete();
        assertEquals(2, h.reads.size()); assertEquals("second", h.reads.get(1).before); assertEquals(1, h.pages.size());
    }

    @Test public void canceledFramesQueuedReadsAndPostedResultsCannotReachAnotherScope() {
        for (int stage = 0; stage < 3; stage++) {
            Harness h = new Harness(); h.result = page("last", false); h.bind("old", page("first", true));
            if (stage >= 1) h.paint(); if (stage >= 2) h.worker.run();
            h.bind("new", page("new", true));
            if (stage == 1) h.worker.run(); if (stage >= 2) h.ui.run();
            assertTrue(h.pages.isEmpty()); assertTrue(h.errors.isEmpty()); assertEquals(stage == 2 ? 1 : 0, h.reads.size());
            assertEquals(1, h.frames.size()); h.complete(); assertEquals(1, h.pages.size());
        }
    }

    @Test public void rebindToANewSurfaceReschedulesAnUndrawnPageButDoesNotDuplicateAnInflightRead() {
        Harness h = new Harness(); JsonObject first = page("first", true); h.result = page("last", false);
        h.bind("session", first); h.loader.bind("session", cursor(first), new Object(), h, h, h);
        assertEquals(1, h.frames.size()); assertEquals(2, h.yields);
        h.paint(); h.loader.bind("session", cursor(first), new Object(), h, h, h); assertTrue(h.frames.isEmpty());
        h.worker.run(); h.ui.run(); assertEquals(1, h.reads.size()); assertEquals(1, h.pages.size());
    }

    @Test public void cursorChangeRevokesPostedFailuresAndClearsNoUnrelatedState() {
        Harness h = new Harness(); h.failure = "expired"; h.bind("session", page("old", true)); h.paint(); h.worker.run();
        h.bind("session", page("new", true)); h.ui.run(); assertFalse(h.loader.failed()); assertTrue(h.errors.isEmpty());
        h.failure = null; h.result = page("last", false); h.complete(); assertEquals(1, h.pages.size());
    }

    @Test public void foregroundSuspendRevokesPendingWorkWithoutResettingTheFailureLatch() {
        Harness h = new Harness(); JsonObject first = page("first", true); h.result = page("last", false);
        h.bind("session", first); h.loader.suspend(); assertTrue(h.frames.isEmpty());
        h.bind("session", first); assertEquals(1, h.frames.size()); h.failure = "offline"; h.complete();
        h.loader.suspend(); h.bind("session", first); assertTrue(h.loader.failed()); assertTrue(h.frames.isEmpty());
        h.failure = null; h.loader.retry(); h.complete(); assertEquals(1, h.pages.size());
    }

    @Test public void aRealBlockedWorkerCannotPublishAnOldPageOrExpireANewerScope() throws Exception {
        for (boolean reject : new boolean[] {false,true}) {
            java.util.concurrent.ExecutorService worker = java.util.concurrent.Executors.newSingleThreadExecutor();
            java.util.concurrent.CountDownLatch entered = new java.util.concurrent.CountDownLatch(1), release = new java.util.concurrent.CountDownLatch(1);
            java.util.concurrent.ConcurrentLinkedQueue<Runnable> callbacks = new java.util.concurrent.ConcurrentLinkedQueue<>();
            List<String> received = new ArrayList<>();
            NativeHistoryLoader<String> loader = new NativeHistoryLoader<>(worker, callbacks::add);
            NativeHistoryLoader.Listener<String> listener = new NativeHistoryLoader.Listener<>() {
                public void received(NativeHistoryLoader.Cursor from, String page) { received.add(page); }
                public void failed(Exception error) { received.add(error.getMessage()); }
            };
            NativeHistoryLoader.Source<String> source = new NativeHistoryLoader.Source<>() {
                public String read(NativeHistoryLoader.Cursor from) throws Exception {
                    entered.countDown(); assertTrue(release.await(3, java.util.concurrent.TimeUnit.SECONDS));
                    if (reject) throw new Exception("Old auth failure"); return "Old page";
                }
                public NativeHistoryLoader.Cursor cursor(String value) { return new NativeHistoryLoader.Cursor("chat-one", "", false, ""); }
            };
            try {
                loader.bind("old", cursor(page("old", true)), "surface", task -> { task.run(); return () -> {}; }, source, listener);
                assertTrue(entered.await(3, java.util.concurrent.TimeUnit.SECONDS));
                loader.bind("new", cursor(page("", false)), "surface", task -> { fail("Complete scopes must not schedule"); return () -> {}; }, source, listener);
                release.countDown(); worker.submit(() -> {}).get(3, java.util.concurrent.TimeUnit.SECONDS);
                while (!callbacks.isEmpty()) callbacks.remove().run();
                assertTrue(received.isEmpty()); assertFalse(loader.failed());
            } finally {
                loader.stop(); release.countDown(); worker.shutdownNow(); assertTrue(worker.awaitTermination(3, java.util.concurrent.TimeUnit.SECONDS));
            }
        }
    }
}
