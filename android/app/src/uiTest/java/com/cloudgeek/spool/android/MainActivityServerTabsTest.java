package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.view.View;
import java.time.Duration;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.Client;
import spoolnet.EventListener;
import spoolnet.EventStream;

/** Independent authenticated workspace streams, including servers that have never been selected. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class,
        MainActivityConnectionTest.ShadowSpoolnet.class, MainActivityWorkspaceEventsTest.ShadowStream.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityServerTabsTest {
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    private static String navigation(String label) {
        return "{projects:[{id:'project-one',name:'" + label + " project',can_create:true}],chats:[{id:'chat-one',title:'" + label
                + " chat',project_id:'project-one',backend:'codex',updated_at:1,can_control:true,participants:[]}]}";
    }

    private static final class Fixture implements AutoCloseable {
        final MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true);
        final MainActivityGoalTest.Queue connections = MainActivityConnectionTest.connectionQueue(h);
        final ServerProfiles servers = h.get("serverProfiles");
        final String home = servers.selected;
        final ServerProfiles.Server office;
        final Client homeClient, officeClient;

        Fixture(boolean finishBackgroundReads) throws Exception {
            MainActivityConnectionTest.ShadowSpoolnet.failDirect = false;
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = null;
            MainActivityWorkspaceEventsTest.install(h);
            responses(200);
            ConnectionPlan plan = new ConnectionPlan("https://home.example.com", "", ConnectionMode.DIRECT);
            servers.active().plan = plan; h.set("connectionPlan", plan); h.set("connectionDraft", plan);
            h.call("openSidebar");
            ReflectionHelpers.callInstanceMethod(h.activity, "selectConnections", ReflectionHelpers.ClassParameter.from(ConnectionMode.class, ConnectionMode.DIRECT),
                    ReflectionHelpers.ClassParameter.from(boolean.class, true));
            drainConnections(); drain(); homeClient = h.get("client");
            push(homeClient, "ready", "true"); push(homeClient, "navigation", navigation("Home"));
            office = servers.add("Office"); office.plan = new ConnectionPlan("https://office.example.com", "", ConnectionMode.DIRECT);
            h.call("refreshServerConnections"); drainConnections();
            Object runtime = h.<Map<String, ?>>get("serverRuntimes").get(office.id);
            officeClient = ((ConnectionRouter.Candidate<Client>) ReflectionHelpers.getField(runtime, "candidate")).client;
            if (finishBackgroundReads) { drain(); push(officeClient, "ready", "true"); }
        }

        void responses(int meStatus) {
            MainActivityGoalTest.ShadowClient.responder = call -> {
                String path = call.get(1);
                String body = path.equals("/api/me") ? "{user:{id:'alice',nickname:'Alice'},csrf:'shared-csrf'}"
                        : path.equals("/api/navigation") ? navigation("REST snapshot")
                        : path.equals("/api/profiles") ? "{revision:1,people:[{id:'alice',nickname:'Alice'}]}"
                        : path.equals("/api/agents") ? "[]" : "{}";
                try { return new JSONObject().put("status", path.equals("/api/me") ? meStatus : 200).put("body", body).toString(); }
                catch (Exception error) { throw new AssertionError(error); }
            };
        }

        void drainConnections() { MainActivityConnectionTest.drain(connections); }
        void drain() {
            int turns = 0;
            while (!h.io.jobs.isEmpty()) { assertTrue("Workspace work must settle", ++turns < 100); h.io.jobs.remove(0).run(); idle(); }
        }
        EventListener listener(Client client) { return MainActivityGoalTest.ShadowClient.workspaceListeners.get(client); }
        EventStream stream(Client client) { return MainActivityGoalTest.ShadowClient.workspaceStreams.get(client); }
        MainActivityWorkspaceEventsTest.ShadowStream socket(Client client) {
            return MainActivityWorkspaceEventsTest.ShadowStream.streams.get(stream(client));
        }
        void push(Client client, String kind, String raw) { assertNotNull(listener(client)); listener(client).onEvent(kind, raw); idle(); }
        void select(String id) { ReflectionHelpers.callInstanceMethod(h.activity, "selectServer", ReflectionHelpers.ClassParameter.from(String.class, id)); }
        void enable(String id, boolean value) { ReflectionHelpers.callInstanceMethod(h.activity, "enableServer", ReflectionHelpers.ClassParameter.from(String.class, id), ReflectionHelpers.ClassParameter.from(boolean.class, value)); }
        JSONObject cachedProject(String id) {
            Object cache = h.<Map<String, ?>>get("serverWorkspaces").get(id); assertNotNull(cache);
            return ((JSONArray) ReflectionHelpers.getField(cache, "projects")).optJSONObject(0);
        }
        String visibleProject() { return h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString(); }
        @Override public void close() {
            h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = null; h.close();
        }
    }

    @Test public void anUnselectedServerReceivesItsOwnPushesAndStaleRestCannotReplaceThem() throws Exception {
        try (Fixture f = new Fixture(false)) {
            assertEquals("The inactive tab starts its own fallback batch and socket", 4, f.h.io.jobs.size());
            f.h.io.jobs.remove(3).run(); idle();
            f.push(f.officeClient, "navigation", navigation("Office pushed"));
            f.push(f.officeClient, "profiles", "{revision:9,people:[{id:'alice',nickname:'Office Alice'}]}");
            f.push(f.officeClient, "agents", "[{backend:'claude',enabled:true,installed:true}]");
            f.push(f.officeClient, "ready", "true");
            assertEquals("Home project", f.visibleProject()); assertSame(f.homeClient, f.h.get("client"));
            f.drain();
            assertEquals("Office pushed project", f.cachedProject(f.office.id).getString("name"));
            Object cache = f.h.<Map<String, ?>>get("serverWorkspaces").get(f.office.id);
            assertEquals("Office Alice", ((JSONArray) ReflectionHelpers.getField(cache, "profiles")).getJSONObject(0).getString("nickname"));
            assertEquals("claude", ((JSONArray) ReflectionHelpers.getField(cache, "agents")).getJSONObject(0).getString("backend"));
            assertTrue(f.socket(f.officeClient).sent.stream().anyMatch(value -> value.optString("type").equals("visibility") && !value.optBoolean("visible")));
            EventListener oldOffice = f.listener(f.officeClient);
            MainActivityWorkspaceEventsTest.ShadowStream oldSocket = f.socket(f.officeClient);
            f.select(f.office.id);
            assertEquals("Office pushed project", f.visibleProject()); assertEquals("sidebar", f.h.<String>get("panel"));
            assertFalse(oldSocket.closed); assertSame(oldOffice, f.listener(f.officeClient));
            assertEquals("Office pushed project", f.visibleProject());
            f.drain(); f.push(f.officeClient, "navigation", navigation("Office live"));
            f.push(f.homeClient, "navigation", navigation("Home updated in background"));
            assertEquals("Office live project", f.visibleProject());
            f.select(f.home);
            assertEquals("Home updated in background project", f.visibleProject());
            assertEquals("sidebar", f.h.<String>get("panel"));
            f.drain();
            assertEquals(2, MainActivityWorkspaceEventsTest.ShadowStream.streams.values().stream().filter(socket -> !socket.closed).count());
        }
    }

    @Test public void rapidTabSelectionKeepsWindowsAndBothServerSocketsWithoutBuildingHiddenChats() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            View window = f.h.get("root"), drawer = f.h.get("drawer");
            NativeSidebar sidebar = f.h.get("sidebar");
            NativeServerTabs tabs = ReflectionHelpers.getField(sidebar, "serverTabs");
            View homeTab = tabs.tabs.findViewWithTag(f.home), officeTab = tabs.tabs.findViewWithTag(f.office.id);
            EventStream home = f.stream(f.homeClient), office = f.stream(f.officeClient);
            EventListener homeEvents = f.listener(f.homeClient), officeEvents = f.listener(f.officeClient);
            int subscriptions = MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size();
            MainActivityGoalTest.Queue closeWork = new MainActivityGoalTest.Queue(); f.h.set("streamCloser", closeWork);
            JSONObject transcript = new JSONObject() { @Override public String toString() { throw new AssertionError("A tab switch must not serialize hidden transcripts"); } };
            f.h.<JSONArray>get("chats").getJSONObject(0).put("transcript", transcript);
            f.h.<JSONObject>get("chat").put("transcript", transcript);
            f.h.call("renderNavigation");
            for (int i = 0; i < 16; i++) {
                String selected = i % 2 == 0 ? f.office.id : f.home;
                f.select(selected);
                assertEquals(i % 2 == 0 ? "Office project" : "Home project", f.visibleProject());
                assertSame(window, f.h.get("root")); assertSame(drawer, f.h.get("drawer")); assertSame(sidebar, f.h.get("sidebar"));
                assertSame(tabs, ReflectionHelpers.getField(sidebar, "serverTabs"));
                assertSame(homeTab, tabs.tabs.findViewWithTag(f.home)); assertSame(officeTab, tabs.tabs.findViewWithTag(f.office.id));
                assertEquals(selected.equals(f.home), homeTab.isSelected()); assertEquals(selected.equals(f.office.id), officeTab.isSelected());
                assertNull(f.h.get("history")); assertNull(f.h.get("transcriptViews")); assertNull(f.h.get("composerView"));
                assertSame(homeEvents, f.listener(f.homeClient)); assertSame(officeEvents, f.listener(f.officeClient));
                assertSame(home, f.stream(f.homeClient)); assertSame(office, f.stream(f.officeClient));
                assertFalse(f.socket(f.homeClient).closed); assertFalse(f.socket(f.officeClient).closed);
                assertTrue("Switching servers must not queue workspace closes", closeWork.jobs.isEmpty());
                assertEquals(subscriptions, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            }
            f.h.<JSONArray>get("chats").getJSONObject(0).remove("transcript");
            f.h.<JSONObject>get("chat").remove("transcript");
            sidebar.chatRows.get(0).open.performClick();
            assertEquals("", f.h.<String>get("panel")); assertNotNull(f.h.get("history")); assertNotNull(f.h.get("composerView"));
            assertSame(home, f.h.get("workspaceStream")); assertFalse(f.socket(f.homeClient).closed);
        }
    }

    @Test public void tabSwitchDoesNotWaitForRetiredNetworkCleanupButDisconnectStillClosesIt() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            MainActivityGoalTest.Queue closeWork = new MainActivityGoalTest.Queue(); f.h.set("streamCloser", closeWork);
            f.select(f.office.id);
            assertEquals("Office project", f.visibleProject()); assertTrue(closeWork.jobs.isEmpty());
            EventListener office = f.listener(f.officeClient);
            f.enable(f.office.id, false);
            assertFalse("Native cleanup has not run on the UI thread", f.socket(f.officeClient).closed);
            assertEquals(1, closeWork.jobs.size());
            office.onEvent("navigation", navigation("Revoked office")); idle();
            assertEquals("Office project", f.visibleProject());
            f.select(f.home);
            assertEquals("Home project", f.visibleProject());
            assertEquals(1, closeWork.jobs.size());
            closeWork.jobs.remove(0).run();
            assertTrue(f.socket(f.officeClient).closed); assertFalse(f.socket(f.homeClient).closed);
        }
    }

    @Test public void serverVisibilityAndDesktopAvailabilityFollowTheSelectedTabWithoutReconnecting() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.homeClient, "desktop", "{available:true}");
            f.push(f.officeClient, "desktop", "{available:true}");
            EventStream home = f.stream(f.homeClient), office = f.stream(f.officeClient);
            assertNotNull(f.h.<NativeSidebar>get("sidebar").remote);
            f.select(f.office.id); f.drain();
            assertNotNull(f.h.<NativeSidebar>get("sidebar").remote);
            assertSame(office, f.h.get("workspaceStream")); assertSame(home, f.stream(f.homeClient));
            assertTrue(f.socket(f.homeClient).sent.stream().anyMatch(value -> value.optString("type").equals("visibility") && !value.optBoolean("visible")));
            assertTrue(f.socket(f.officeClient).sent.stream().anyMatch(value -> value.optString("type").equals("visibility") && value.optBoolean("visible")));
            f.push(f.homeClient, "desktop", "{available:false}");
            assertNotNull(f.h.<NativeSidebar>get("sidebar").remote);
            f.select(f.home);
            assertNull(f.h.<NativeSidebar>get("sidebar").remote);
            assertSame(home, f.h.get("workspaceStream")); assertFalse(f.socket(f.officeClient).closed);
        }
    }

    @Test public void framesAlreadyBeingDecodedFollowTheirSocketAcrossATabSwitch() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            MainActivityGoalTest.Queue decode = new MainActivityGoalTest.Queue(); f.h.set("workspaceDecode", decode);
            f.push(f.homeClient, "navigation", navigation("Home queued before hiding"));
            f.push(f.officeClient, "navigation", navigation("Office queued before showing"));
            assertEquals(2, decode.jobs.size());
            f.select(f.office.id);
            while (!decode.jobs.isEmpty()) { decode.jobs.remove(0).run(); idle(); }
            assertEquals("Office queued before showing project", f.visibleProject());
            f.select(f.home);
            assertEquals("Home queued before hiding project", f.visibleProject());
            assertFalse(f.socket(f.homeClient).closed); assertFalse(f.socket(f.officeClient).closed);
        }
    }

    @Test public void inactiveSocketRetriesPreserveItsCacheAndOnlyConfirmedExpiryInvalidatesIt() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            EventListener old = f.listener(f.officeClient);
            MainActivityWorkspaceEventsTest.ShadowStream socket = f.socket(f.officeClient);
            f.push(f.officeClient, "transport_error", "{}"); f.drain();
            assertTrue(socket.closed); assertEquals("Office project", f.cachedProject(f.office.id).getString("name"));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(650)); f.drain();
            assertNotSame(old, f.listener(f.officeClient));
            old.onEvent("navigation", navigation("Old connection")); idle();
            assertEquals("Office project", f.cachedProject(f.office.id).getString("name"));
            f.push(f.officeClient, "navigation", navigation("Office reconnected"));
            MainActivityWorkspaceEventsTest.ShadowStream reconnected = f.socket(f.officeClient);
            f.responses(503); f.h.call("refreshServerConnections"); f.drainConnections();
            assertSame(reconnected, f.socket(f.officeClient)); assertFalse(reconnected.closed);
            assertEquals("Office reconnected project", f.cachedProject(f.office.id).getString("name"));
            f.responses(401); f.h.call("refreshServerConnections"); f.drainConnections();
            assertTrue(reconnected.closed); assertFalse(f.h.<Map<?, ?>>get("serverWorkspaces").containsKey(f.office.id));
            assertEquals(0, f.office.account.length()); assertEquals("Home project", f.visibleProject());
            assertEquals("alice", f.h.<JSONObject>get("user").getString("id")); assertSame(f.homeClient, f.h.get("client"));
            f.listener(f.officeClient).onEvent("navigation", navigation("Revoked connection")); idle();
            assertFalse(f.h.<Map<?, ?>>get("serverWorkspaces").containsKey(f.office.id));
        }
    }

    @Test public void disabledTabsAndBackgroundedAppsStopOnlyTheirOwnedSubscriptions() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            EventListener old = f.listener(f.officeClient);
            MainActivityWorkspaceEventsTest.ShadowStream first = f.socket(f.officeClient);
            f.enable(f.office.id, false); f.drainConnections();
            assertTrue(first.closed); assertFalse(f.socket(f.homeClient).closed);
            old.onEvent("navigation", navigation("Disabled connection")); idle();
            assertEquals("Office project", f.cachedProject(f.office.id).getString("name"));
            f.enable(f.office.id, true); f.drainConnections();
            assertEquals("Office project", f.cachedProject(f.office.id).getString("name")); f.drain();
            Object runtime = f.h.<Map<String, ?>>get("serverRuntimes").get(f.office.id);
            Client replacement = ((ConnectionRouter.Candidate<Client>) ReflectionHelpers.getField(runtime, "candidate")).client;
            f.push(replacement, "navigation", navigation("Office resumed"));
            MainActivityWorkspaceEventsTest.ShadowStream resumed = f.socket(replacement);
            f.h.controller.pause().stop();
            assertTrue(resumed.closed); assertEquals("Office resumed project", f.cachedProject(f.office.id).getString("name"));
            f.listener(replacement).onEvent("navigation", navigation("Stopped app")); idle();
            assertEquals("Office resumed project", f.cachedProject(f.office.id).getString("name"));
            f.h.controller.start().resume(); idle(); f.drainConnections(); f.drain();
            assertFalse(f.socket(replacement).closed); assertNotSame(resumed, f.socket(replacement));
            assertSame(f.homeClient, f.h.get("client"));
        }
    }

    @Test public void anInactiveAuthenticationExpirySignalClearsOnlyThatServersCache() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            EventListener old = f.listener(f.officeClient);
            MainActivityWorkspaceEventsTest.ShadowStream socket = f.socket(f.officeClient);
            f.push(f.officeClient, "auth_expired", "{}");
            assertTrue(socket.closed); assertEquals(0, f.office.account.length());
            assertFalse(f.h.<Map<?, ?>>get("serverWorkspaces").containsKey(f.office.id));
            assertEquals("Home project", f.visibleProject()); assertEquals("alice", f.h.<JSONObject>get("user").getString("id"));
            old.onEvent("navigation", navigation("Expired connection")); idle();
            assertFalse(f.h.<Map<?, ?>>get("serverWorkspaces").containsKey(f.office.id));
        }
    }

    @Test public void anInactiveServersNetworkHandoffKeepsItsCacheAndRejectsTheOldSocket() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            EventListener old = f.listener(f.officeClient);
            MainActivityWorkspaceEventsTest.ShadowStream socket = f.socket(f.officeClient);
            f.office.plan = new ConnectionPlan(f.office.plan.direct, "https://office.example.ts.net", ConnectionMode.DIRECT, true);
            MainActivityGoalTest.ShadowClient shadow = org.robolectric.shadow.api.Shadow.extract(f.officeClient);
            shadow.healthResponse = "{state:'Running',https:'Failed',problem:'network'}";
            f.h.call("refreshServerConnections"); f.drainConnections();
            assertTrue(socket.closed); assertEquals("Office project", f.cachedProject(f.office.id).getString("name"));
            Object runtime = f.h.<Map<String, ?>>get("serverRuntimes").get(f.office.id);
            ConnectionRouter.Candidate<Client> candidate = ReflectionHelpers.getField(runtime, "candidate");
            assertEquals(ConnectionMode.TAILSCALE, candidate.endpoint.mode); assertNotSame(f.officeClient, candidate.client);
            old.onEvent("navigation", navigation("Old public connection")); idle();
            assertEquals("Office project", f.cachedProject(f.office.id).getString("name"));
            assertEquals("Home project", f.visibleProject()); assertSame(f.homeClient, f.h.get("client"));
            f.drain(); f.push(candidate.client, "navigation", navigation("Office via Tailscale"));
            f.select(f.office.id);
            assertEquals("Office via Tailscale project", f.visibleProject());
        }
    }

    @Test public void aDefinitiveInactiveEndpointFailureClearsOnlyItsFetchedNavigation() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            MainActivityGoalTest.ShadowClient shadow = org.robolectric.shadow.api.Shadow.extract(f.officeClient);
            shadow.healthResponse = "{state:'Running',https:'Failed',problem:'endpoint'}";
            f.h.call("refreshServerConnections"); f.drainConnections();
            assertNull(f.cachedProject(f.office.id)); assertTrue(f.socket(f.officeClient).closed);
            assertEquals("alice", f.office.account.getString("id"));
            assertEquals("Home project", f.visibleProject()); assertSame(f.homeClient, f.h.get("client"));
            f.listener(f.officeClient).onEvent("navigation", navigation("Failed connection")); idle();
            assertNull(f.cachedProject(f.office.id));
        }
    }
}
