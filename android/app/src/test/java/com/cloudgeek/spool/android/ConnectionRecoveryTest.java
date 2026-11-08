package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import org.junit.Test;

public class ConnectionRecoveryTest {
    private static final class Queue implements Executor, ConnectionRecovery.Scheduler {
        final List<Runnable> immediate = new ArrayList<>(), delayed = new ArrayList<>();
        long now, delay;
        public void execute(Runnable task) { immediate.add(task); }
        public void post(Runnable task) { immediate.add(task); }
        public void postDelayed(Runnable task, long millis) { delayed.add(task); delay = millis; }
        public void remove(Runnable task) { delayed.removeIf(value -> value == task); }
        public long now() { return now; }
        void run() { immediate.remove(0).run(); }
        void tick() { now += delay; delayed.remove(0).run(); }
    }
    private static ConnectionRecovery.Snapshot snapshot(String problem) {
        return new ConnectionRecovery.Snapshot("Running", problem.isEmpty() ? "Verified" : "Failed", problem, "", "");
    }
    private static final class Source implements ConnectionRecovery.Source {
        boolean online = true, failLogin;
        int checks, refreshes, reconnects, logins;
        long responseAge = -1;
        ConnectionRecovery.Snapshot value = snapshot("");
        boolean notifications;
        Runnable changed;
        int subscriptions, closes;
        public boolean online() { return online; }
        public ConnectionRecovery.Snapshot check() { checks++; return value; }
        public void refreshNetwork() { refreshes++; }
        public void reconnect() { reconnects++; }
        public long responseAgeMillis() { return responseAge; }
        public void login() throws Exception { logins++; if (failLogin) throw new Exception("login failed"); }
        public ConnectionRecovery.Subscription subscribe(Runnable callback) {
            if (!notifications) return null;
            changed = callback; subscriptions++;
            return () -> { changed = null; closes++; };
        }
    }
    private static final class Harness {
        final Queue worker = new Queue(), ui = new Queue();
        final Source source = new Source();
        final List<Boolean> available = new ArrayList<>();
        final List<String> problems = new ArrayList<>();
        final ConnectionRecovery policy = new ConnectionRecovery(worker, ui);
        Harness() { policy.start(source, (value, usable) -> { problems.add(value.problem); available.add(usable); }); }
        void start() { policy.setActive(true); finish(); }
        void finish() {
            while (!worker.immediate.isEmpty() || !ui.immediate.isEmpty()) {
                if (!worker.immediate.isEmpty()) worker.run();
                if (!ui.immediate.isEmpty()) ui.run();
            }
        }
        void tick() { ui.tick(); finish(); }
        boolean available() { return available.get(available.size() - 1); }
    }

    @Test public void healthyForegroundSamplesRunWithoutSettingsAndOnlyOnceAtATime() {
        Harness h = new Harness();
        assertTrue(h.worker.immediate.isEmpty());
        h.policy.setActive(true); h.policy.setActive(true); h.policy.suspect();
        assertEquals(1, h.worker.immediate.size());
        h.finish(); assertTrue(h.available()); assertEquals(60000, h.ui.delay);
        h.tick(); assertEquals(2, h.source.checks); assertEquals(1, h.ui.delayed.size());
        h.policy.setActive(false); assertTrue(h.ui.delayed.isEmpty());
        h.policy.setActive(true); assertEquals(1, h.worker.immediate.size());
        h.finish(); assertTrue(h.available());
    }

    @Test public void singleFailureDoesNotDisconnectAndPersistentFailureRefreshesThenBouncesWithCooldown() {
        Harness h = new Harness(); h.start(); h.source.value = snapshot("network");
        h.tick(); assertTrue(h.available()); assertEquals(0, h.source.refreshes); assertEquals(2000, h.ui.delay);
        h.tick(); assertFalse(h.available()); assertEquals(1, h.source.refreshes); assertEquals(0, h.source.reconnects);
        while (h.source.reconnects == 0) h.tick();
        long resetAt = h.ui.now;
        assertTrue(resetAt >= 45000); assertEquals(1, h.source.reconnects);
        h.tick(); assertEquals(1, h.source.reconnects);
        h.tick(); assertEquals(2, h.source.reconnects); assertTrue(h.ui.now - resetAt >= 60000);
        h.tick(); h.tick(); h.tick(); assertEquals(2, h.source.reconnects);
        h.source.value = snapshot(""); h.tick(); assertTrue(h.available()); assertEquals(60000, h.ui.delay);
        assertEquals(List.of(true, true, false), h.available.subList(0, 3));
    }

    @Test public void endpointAndServerFailuresNeverResetTheNodeOrInheritNetworkFailureCount() {
        for (String problem : List.of("endpoint", "server", "login", "approval")) {
            Harness h = new Harness(); h.start(); h.source.value = snapshot(problem);
            for (int i = 0; i < 15; i++) h.tick();
            assertFalse(h.available()); assertEquals(0, h.source.reconnects); assertEquals(0, h.source.refreshes);
            h.source.value = snapshot("network"); h.tick();
            assertEquals(0, h.source.reconnects); assertEquals(0, h.source.refreshes);
        }
    }

    @Test public void backendFailureAlsoRequiresAGracePeriodBeforeResetAndLoginIsRequestedOnce() {
        for (String problem : List.of("status", "starting")) {
            Harness h = new Harness(); h.source.value = snapshot(problem); h.start();
            h.tick(); h.tick(); assertEquals(0, h.source.reconnects);
            while (h.source.reconnects == 0) h.tick();
            assertTrue(h.ui.now >= 30000); assertEquals(1, h.source.reconnects);
        }
        Harness h = new Harness(); h.source.value = snapshot("login"); h.start();
        h.tick(); h.tick(); assertEquals(1, h.source.logins);
        h.source.value = snapshot("approval"); h.tick();
        h.source.value = snapshot("login"); h.tick(); assertEquals(2, h.source.logins);
        Harness retry = new Harness(); retry.source.value = snapshot("login"); retry.source.failLogin = true;
        retry.start(); retry.tick(); assertEquals(2, retry.source.logins);
    }

    @Test public void missingNetworkPausesRecoveryAndResumeChecksImmediately() {
        Harness h = new Harness(); h.source.online = false; h.start();
        for (int i = 0; i < 30; i++) h.tick();
        assertEquals(0, h.source.checks); assertEquals(0, h.source.reconnects); assertFalse(h.available());
        assertEquals("offline", h.problems.get(0));
        h.source.online = true; h.policy.checkNow(); h.finish();
        assertEquals(1, h.source.checks); assertTrue(h.available());
    }

    @Test public void oldNetworkBackgroundAndDisconnectResultsCannotTriggerRecovery() {
        Harness h = new Harness(); h.source.value = snapshot("network");
        h.policy.setActive(true); h.worker.run();
        h.policy.checkNow(); h.ui.run();
        assertTrue(h.available.isEmpty()); assertEquals(0, h.ui.delay);
        h.ui.tick(); h.worker.run(); h.policy.setActive(false); h.ui.run();
        assertTrue(h.available.isEmpty()); assertTrue(h.ui.delayed.isEmpty());
        h.policy.setActive(true); h.worker.run(); h.policy.stop(); h.ui.run();
        assertTrue(h.available.isEmpty()); assertTrue(h.ui.delayed.isEmpty());
        assertEquals(0, h.source.refreshes); assertEquals(0, h.source.reconnects);
    }

    @Test public void queuedRecoveryIsRevokedAndRepeatedApiFailuresCannotBypassBackoff() {
        Harness h = new Harness(); h.source.value = snapshot("network"); h.start();
        for (int i = 0; i < 100; i++) h.policy.suspect();
        assertTrue(h.worker.immediate.isEmpty()); assertEquals(2000, h.ui.delay);
        h.ui.tick(); h.worker.run(); h.ui.run(); // Refresh network is queued after second failure.
        assertEquals(1, h.worker.immediate.size());
        h.policy.stop(); h.finish();
        assertEquals(0, h.source.refreshes); assertEquals(0, h.source.reconnects); assertTrue(h.ui.delayed.isEmpty());
    }

    @Test public void aQueuedLoginCanceledByBackgroundCanStillStartAfterReturning() {
        Harness h = new Harness(); h.source.value = snapshot("login");
        h.policy.setActive(true); h.worker.run(); h.ui.run();
        assertEquals(1, h.worker.immediate.size());
        h.policy.setActive(false); h.finish(); assertEquals(0, h.source.logins);
        h.policy.setActive(true); h.finish(); assertEquals(1, h.source.logins);
    }

    @Test public void nodeNotificationsReplaceLoginAndApprovalPolling() {
        Harness h = new Harness(); h.source.notifications = true; h.source.value = snapshot("login");
        h.start();
        assertEquals(1, h.source.logins); assertTrue(h.ui.delayed.isEmpty());
        h.source.value = snapshot("approval"); h.source.changed.run(); h.finish();
        assertEquals(2, h.source.checks); assertTrue(h.ui.delayed.isEmpty());
        h.source.value = snapshot(""); h.source.changed.run(); h.finish();
        assertTrue(h.available()); assertEquals(60000, h.ui.delay);
        assertEquals(1, h.source.subscriptions);
    }

    @Test public void oldSubscriptionsCannotWakeAnotherClientOrBackgroundRecovery() {
        Harness h = new Harness(); h.source.notifications = true; h.start();
        Runnable old = h.source.changed;
        h.policy.setActive(false); old.run(); h.finish();
        assertEquals(1, h.source.closes); assertEquals(1, h.source.checks);
        assertTrue(h.ui.delayed.isEmpty());
        h.policy.setActive(true); h.finish();
        assertEquals(2, h.source.subscriptions);
        old.run(); h.finish(); assertEquals(2, h.source.checks);
        Runnable current = h.source.changed;
        h.policy.stop(); current.run(); h.finish();
        assertEquals(2, h.source.closes); assertEquals(2, h.source.checks);
    }

    @Test public void failedInteractiveLoginStillRetriesWithNotifications() {
        Harness h = new Harness(); h.source.notifications = true; h.source.failLogin = true; h.source.value = snapshot("login");
        h.start(); assertEquals(1500, h.ui.delay);
        h.source.failLogin = false; h.tick();
        assertEquals(2, h.source.logins); assertTrue(h.ui.delayed.isEmpty());
    }

    @Test public void recentResponsesSuppressHealthTrafficUntilTheConnectionIsQuiet() {
        Harness h = new Harness(); h.start();
        assertEquals(1, h.source.checks); assertEquals(60000, h.ui.delay);
        for (int i = 0; i < 10; i++) {
            h.source.responseAge = 15000; h.tick();
            assertEquals("Existing heartbeats replace standalone HTTPS checks", 1, h.source.checks);
            assertEquals(45000, h.ui.delay);
        }
        h.source.responseAge = 60000; h.tick();
        assertEquals("A quiet connection still gets a bounded health check", 2, h.source.checks);
        assertEquals(60000, h.ui.delay);
    }

    @Test public void initialResumeNetworkAndFailureChecksIgnoreRecentResponses() {
        Harness h = new Harness(); h.source.responseAge = 0; h.start();
        assertEquals(1, h.source.checks);
        h.policy.checkNow(); h.finish(); assertEquals(2, h.source.checks);
        h.policy.setActive(false); h.policy.setActive(true); h.finish(); assertEquals(3, h.source.checks);
        h.source.value = snapshot("network"); h.policy.suspect(); h.finish();
        assertEquals(4, h.source.checks); assertEquals(2000, h.ui.delay);
        h.tick(); assertEquals("Recent traffic cannot hide a known failure", 5, h.source.checks);
        for (int i = 0; i < 50; i++) h.policy.suspect();
        assertTrue(h.worker.immediate.isEmpty()); assertEquals(4000, h.ui.delay);
    }

    @Test public void aForcedCheckDuringARequestCannotBeLostOrSuppressedByOtherTraffic() {
        Harness h = new Harness(); h.start(); h.source.responseAge = 0;
        h.policy.checkNow(); h.worker.run();
        h.policy.checkNow(); h.ui.run();
        assertEquals(0, h.ui.delay);
        h.tick(); assertEquals(3, h.source.checks); assertEquals(60000, h.ui.delay);
        h.policy.stop(); h.policy.suspect();
        assertTrue(h.ui.delayed.isEmpty()); assertTrue(h.worker.immediate.isEmpty());
    }
}
