package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.util.*;
import java.util.concurrent.Executor;
import org.junit.Test;

public class ConnectionRouterTest {
    static final class Queue implements Executor {
        final List<Runnable> jobs = new ArrayList<>();
        public void execute(Runnable task) { jobs.add(task); }
        void drain() { while (!jobs.isEmpty()) jobs.remove(0).run(); }
    }
    static final class Backend implements ConnectionRouter.Backend<String> {
        final List<String> calls = new ArrayList<>();
        final Map<String, String> accounts = new HashMap<>(), problems = new HashMap<>();
        final Set<String> live = new HashSet<>();
        final Set<String> sessionFailures = new HashSet<>();
        public String open(ConnectionPlan.Endpoint endpoint) { assertTrue("A server must never have two live networks", live.isEmpty()); live.add(endpoint.origin); calls.add("open " + endpoint.mode + " " + endpoint.origin); return endpoint.origin; }
        public ConnectionRecovery.Snapshot health(String client) {
            calls.add("health " + client);
            String problem = problems.getOrDefault(client, "");
            return new ConnectionRecovery.Snapshot("Running", problem.isEmpty() ? "Verified" : "Failed", problem, "", "");
        }
        public ConnectionRouter.Session session(String client) throws Exception {
            calls.add("session " + client);
            if (sessionFailures.contains(client)) throw new java.io.IOException("Session service temporarily unavailable");
            String user = accounts.get(client); return user == null ? null : new ConnectionRouter.Session(user, user);
        }
        public String login(String client) { calls.add("login " + client); return "https://login.tailscale.com/a/test"; }
        public void close(String client) { calls.add("close " + client); live.remove(client); }
    }
    static class Listener implements ConnectionRouter.Listener<String> {
        ConnectionRouter.Candidate<String> result;
        boolean unavailable;
        public void sampled(ConnectionPlan.Endpoint endpoint, ConnectionRecovery.Snapshot health, boolean signedIn) {}
        public void selected(ConnectionRouter.Candidate<String> candidate) { result = candidate; }
        public void unavailable() { unavailable = true; }
    }
    static final String PUBLIC = "https://chat.example.com", TAIL = "https://spool.example.ts.net";
    final Queue worker = new Queue(), ui = new Queue();
    final Backend backend = new Backend();
    final ConnectionRouter<String> router = new ConnectionRouter<>(worker, ui::execute, backend);
    final ConnectionPlan plan = new ConnectionPlan(PUBLIC, TAIL, ConnectionMode.DIRECT, true);
    Listener select(String account, boolean manual) {
        Listener listener = new Listener(); router.select(plan.candidates(null), account, manual, account.isEmpty(), listener);
        worker.drain(); ui.drain(); return listener;
    }

    @Test public void selectsTheFirstReachableSignedInEntryAndClosesBeforeChangingNetworks() {
        backend.accounts.put(PUBLIC, "alice"); backend.accounts.put(TAIL, "alice");
        assertEquals(PUBLIC, select("alice", false).result.client);
        assertFalse(backend.calls.toString().contains(TAIL));
        backend.problems.put(PUBLIC, "network");
        assertEquals(TAIL, select("alice", false).result.client);
        backend.problems.remove(PUBLIC);
        assertEquals(PUBLIC, select("alice", false).result.client);
        assertEquals(2, backend.calls.stream().filter(value -> value.startsWith("open DIRECT")).count());
        assertEquals(2, backend.calls.stream().filter(value -> value.startsWith("close ")).count());
        assertTrue(backend.calls.stream().noneMatch(value -> value.startsWith("login ")));
        assertEquals(Set.of(PUBLIC), backend.live);
    }

    @Test public void automaticSwitchingRequiresAnExistingMatchingAccountOnTheOtherEntry() {
        backend.problems.put(PUBLIC, "network"); backend.accounts.put(TAIL, "bob");
        assertTrue(select("alice", false).unavailable);
        assertNull(select("alice", true).result.session);
        backend.accounts.put(TAIL, "alice");
        assertEquals(TAIL, select("alice", false).result.client);
    }

    @Test public void authenticationReadFailuresDoNotChangeNetworkHealthOrSwitchToAnotherNetwork() {
        backend.sessionFailures.add(PUBLIC);
        ConnectionRouter.Candidate<String> pending = select("alice", false).result;
        assertNotNull(pending); assertEquals(PUBLIC, pending.client);
        assertTrue(pending.health.healthy()); assertFalse(pending.sessionChecked);
        assertNull(pending.session);
        assertTrue(backend.calls.stream().noneMatch(call -> call.contains(TAIL)));
        backend.sessionFailures.clear(); backend.accounts.put(PUBLIC, "alice");
        ConnectionRouter.Candidate<String> restored = select("alice", false).result;
        assertTrue(restored.sessionChecked); assertEquals("alice", restored.session.userID);
    }

    @Test public void startupPrefersAnExistingSessionBeforeShowingLogin() {
        backend.accounts.put(TAIL, "alice");
        assertEquals(TAIL, select("", false).result.client);
        backend.accounts.clear();
        assertEquals(PUBLIC, select("", false).result.client);
    }

    @Test public void disabledAutomaticSwitchingAndManualSelectionNeverProbeAnotherEntry() {
        backend.problems.put(PUBLIC, "network"); backend.accounts.put(TAIL, "alice");
        Listener result = new Listener();
        router.select(plan.automatic(false).candidates(null), "alice", false, false, result);
        worker.drain(); ui.drain(); assertTrue(result.unavailable);
        assertTrue(backend.calls.stream().noneMatch(value -> value.contains(TAIL)));
        assertEquals(1, plan.candidates(ConnectionMode.TAILSCALE).size());
        assertEquals(TAIL, plan.candidates(ConnectionMode.TAILSCALE).get(0).origin);
    }

    @Test public void staleResultsAreRevokedBeforeDeliveryAndStopClosesEveryOwnedClient() {
        backend.accounts.put(TAIL, "alice");
        Listener stale = new Listener();
        router.select(plan.candidates(null), "alice", false, false, stale);
        worker.drain(); router.stop(); ui.drain(); worker.drain();
        assertNull(stale.result); assertFalse(stale.unavailable);
        assertEquals(2, backend.calls.stream().filter(value -> value.startsWith("close ")).count());
    }

    @Test public void invalidationReopensTheSameEndpointAndDiscardsPendingSelections() {
        backend.accounts.put(PUBLIC, "alice");
        assertEquals(PUBLIC, select("alice", true).result.client);
        Listener stale = new Listener();
        router.select(plan.candidates(null), "alice", true, true, stale);
        worker.drain();
        router.invalidate();
        backend.calls.clear();
        List<String> replaced = new ArrayList<>();
        Listener fresh = new Listener() {
            @Override public void replacing(String client) { replaced.add(client); }
        };
        router.select(plan.candidates(null), "alice", true, true, fresh);
        worker.drain(); ui.drain();
        assertNull(stale.result);
        assertEquals(List.of(PUBLIC), replaced);
        assertEquals(List.of("close " + PUBLIC, "open DIRECT " + PUBLIC, "health " + PUBLIC, "session " + PUBLIC), backend.calls);
        assertEquals(PUBLIC, fresh.result.client);
        backend.calls.clear();
        select("alice", true);
        assertTrue(backend.calls.stream().noneMatch(call -> call.startsWith("open ") || call.startsWith("close ")));
    }

    @Test public void anotherServersRouterKeepsItsOwnClientsAndNeverReceivesFailover() {
        Backend other = new Backend(); other.accounts.put("https://other.example.com", "bob");
        ConnectionRouter<String> second = new ConnectionRouter<>(worker, ui::execute, other);
        Listener result = new Listener();
        second.select(new ConnectionPlan("https://other.example.com", "", ConnectionMode.DIRECT).candidates(null), "", false, true, result);
        worker.drain(); ui.drain(); assertNotNull(result.result);
        backend.problems.put(PUBLIC, "network"); backend.problems.put(TAIL, "network");
        assertTrue(select("alice", false).unavailable);
        router.stop(); worker.drain();
        assertTrue(other.calls.stream().noneMatch(value -> value.startsWith("close ")));
        second.stop(); worker.drain();
        assertEquals(1, other.calls.stream().filter(value -> value.startsWith("close ")).count());
    }
}
