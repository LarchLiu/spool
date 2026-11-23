package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.view.View;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadow.api.Shadow;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.Client;
import spoolnet.EventListener;
import spoolnet.EventStream;

/** Independent authenticated workspace streams, including servers that have never been selected. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class,
        MainActivityConnectionTest.ShadowSpoolnet.class, MainActivityWorkspaceEventsTest.ShadowStream.class,
        MainActivityServerTabsTest.ShadowFederationSession.class})
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
            this(finishBackgroundReads, null);
        }

        Fixture(boolean finishBackgroundReads, JSONArray homeTargets) throws Exception {
            this(finishBackgroundReads, homeTargets, new ConnectionPlan("https://office.example.com", "", ConnectionMode.DIRECT));
        }

        Fixture(boolean finishBackgroundReads, JSONArray homeTargets, ConnectionPlan officePlan) throws Exception {
            MainActivityConnectionTest.ShadowSpoolnet.failDirect = false;
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = null;
            MainActivityWorkspaceEventsTest.install(h);
            responses(200, homeTargets);
            ConnectionPlan plan = new ConnectionPlan("https://home.example.com", "", ConnectionMode.DIRECT);
            servers.active().plan = plan; h.set("connectionPlan", plan); h.set("connectionDraft", plan);
            h.call("openSidebar");
            checkDirectRoute();
            drainConnections(); drain(); homeClient = h.get("client");
            push(homeClient, "ready", "true"); push(homeClient, "navigation", navigation("Home"));
            if (homeTargets != null) responses(200);
            office = servers.add("Office"); office.plan = officePlan;
            h.call("refreshServerConnections"); drainConnections();
            Object runtime = h.<Map<String, ?>>get("serverRuntimes").get(office.id);
            officeClient = ((ConnectionRouter.Candidate<Client>) ReflectionHelpers.getField(runtime, "candidate")).client;
            if (finishBackgroundReads) { drain(); push(officeClient, "ready", "true"); }
        }

        void responses(int meStatus) { responses(meStatus, null); }

        void responses(int meStatus, JSONArray targets) { responses(meStatus, targets, "alice", "shared-csrf"); }

        void responses(int meStatus, JSONArray targets, String account, String csrf) {
            MainActivityGoalTest.ShadowClient.responder = call -> {
                String path = call.get(1);
                String body = path.equals("/api/me") ? "{user:{id:'" + account + "',nickname:'Alice'},csrf:'" + csrf + "',server_name:'Server from sign-in'"
                        + (targets == null ? "}" : ",federation_targets:" + targets + "}")
                        : path.equals("/api/navigation") ? navigation("REST snapshot")
                        : path.equals("/api/profiles") ? "{revision:1,people:[{id:'alice',nickname:'Alice'}]}"
                        : path.equals("/api/agents") ? "[]" : "{}";
                try { return new JSONObject().put("status", path.equals("/api/me") ? meStatus : 200).put("body", body).toString(); }
                catch (Exception error) { throw new AssertionError(error); }
            };
        }

        void checkDirectRoute() {
            ReflectionHelpers.callInstanceMethod(h.activity, "selectConnections", ReflectionHelpers.ClassParameter.from(ConnectionMode.class, ConnectionMode.DIRECT),
                    ReflectionHelpers.ClassParameter.from(boolean.class, true));
        }
        NativeServerTabs tabs() { return ReflectionHelpers.getField(h.<NativeSidebar>get("sidebar"), "serverTabs"); }
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
        String status(String id) {
            Map<String, String> states = ReflectionHelpers.callInstanceMethod(h.activity, "serverConnectionStates");
            return states.get(id);
        }
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

    @Test public void trustedTabsStayWithTheirSignedInServerWhenTheSidebarReopens() throws Exception {
        String target = "b".repeat(52);
        JSONArray targets = new JSONArray().put(new JSONObject().put("id", target).put("name", "Lab"));
        try (Fixture f = new Fixture(true, targets)) {
            String key = NativeFederation.tabKey(f.home, target);
            f.h.call("closeDrawer");
            f.h.call("openSidebar");
            assertEquals("Spool → Lab, Trusted server", f.tabs().button(key).getContentDescription());

            f.select(f.office.id);
            assertNotNull(f.tabs().button(key));
            f.push(f.homeClient, "auth_expired", "{}");
            assertNull(f.tabs().button(key));
            assertEquals(f.office.id, f.servers.selected);
        }
    }

    private static JSONArray target(String id, String name, String address) throws Exception {
        return new JSONArray().put(new JSONObject().put("id", id).put("name", name).put("client_url", address));
    }

    @Test public void trustedSignInKeepsTheOrdinaryRelayLoginAndUsesItsOwnAuthorizedRoute() throws Exception {
        String id = "b".repeat(52), address = "https://office.example.com";
        String relay = RelayPairingTest.code("https://relay.example.com");
        ConnectionPlan plan = new ConnectionPlan(address, "", relay, ConnectionMode.RELAY, false);
        JSONArray allowed = target(id, "Office", address);
        try (Fixture f = new Fixture(true, allowed, plan)) {
            Client ordinary = f.officeClient;
            MainActivityWorkspaceEventsTest.ShadowStream socket = f.socket(ordinary);
            clientShadow(f.homeClient).responses = call -> reply(200, call.get(1).equals("/api/me")
                    ? "{user:{id:'alice'},csrf:'shared-csrf'}" : allowed);
            spoolnet.FederationSession exchange = Shadow.newInstanceOf(spoolnet.FederationSession.class);
            clientShadow(f.homeClient).federationPreparation = (destination, target, account, language) -> {
                assertNotSame(ordinary, destination);
                assertEquals(address, clientShadow(destination).directOrigin);
                return exchange;
            };
            String key = NativeFederation.tabKey(f.home, id);
            assertNotNull(f.tabs().button(f.office.id));
            f.tabs().button(key).performClick(); f.drainConnections(); f.drain();
            assertTrue(f.servers.active().trusted()); assertNotEquals(f.office.id, f.servers.selected);
            assertSame(ConnectionMode.DIRECT, f.h.get("connectionMode"));
            assertEquals("external-office", f.h.<JSONObject>get("user").optString("id"));
            assertSame(plan, f.office.plan); assertEquals(0, clientShadow(ordinary).closes); assertFalse(socket.closed);
            f.select(f.office.id);
            assertSame(ordinary, f.h.get("client")); assertEquals("alice", f.h.<JSONObject>get("user").optString("id"));
        }
    }

    @Implements(value = spoolnet.FederationSession.class, isInAndroidSdk = false)
    public static class ShadowFederationSession {
        int activations;
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected void __constructor__() {}
        @Implementation protected String activate() {
            activations++;
            return "{user:{id:'external-office',nickname:'Alice',source_server_id:'" + "a".repeat(52) + "'},csrf:'office-csrf'}";
        }
    }

    private static String reply(int status, Object body) {
        try { return new JSONObject().put("status", status).put("body", body.toString()).toString(); }
        catch (Exception error) { throw new AssertionError(error); }
    }

    private static MainActivityGoalTest.ShadowClient clientShadow(Client client) { return Shadow.extract(client); }

    @Test public void newTrustedTailscaleServerExposesEnrollmentAndRetriesTheChosenAccount() throws Exception {
        checkTrustedTailscaleEnrollment(false);
    }

    @Test public void enrollmentRecoveryDoesNotPrepareATicketIfThePublishedModeChanges() throws Exception {
        checkTrustedTailscaleEnrollment(true);
    }

    private void checkTrustedTailscaleEnrollment(boolean modeChanged) throws Exception {
        String id = "b".repeat(52), address = "https://lab.example.ts.net";
        JSONArray allowed = target(id, "Lab", address);
        allowed.getJSONObject(0).put("client_mode", "tailscale");
        try (Fixture f = new Fixture(true, allowed)) {
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = "{state:'Starting',https:'Not checked',problem:'starting'}";
            f.tabs().button(NativeFederation.tabKey(f.home, id)).performClick();
            NativeDialog confirm = (NativeDialog) org.robolectric.shadows.ShadowDialog.getLatestDialog();
            assertTrue(confirm.isShowing());
            confirm.actions.getChildAt(1).performClick();
            f.drainConnections(); f.drain();

            ServerProfiles.Server lab = f.servers.active();
            Client labClient = f.h.get("client");
            assertEquals("Lab", lab.name);
            assertSame(ConnectionMode.TAILSCALE, lab.plan.selected);
            assertSame(ConnectionMode.TAILSCALE, f.h.get("connectionMode"));
            assertEquals("", lab.plan.direct);
            assertEquals(address, lab.plan.tailscale);
            assertEquals("tailscale", MainActivityConnectionTest.ShadowSpoolnet.mode);
            assertEquals(address, clientShadow(labClient).tailscaleOrigin);
            assertNull(f.h.get("user"));

            JSONArray freshTargets = modeChanged ? target(id, "Lab", address) : allowed;
            clientShadow(f.homeClient).responses = call -> reply(200, call.get(1).equals("/api/me")
                    ? "{user:{id:'alice'},csrf:'shared-csrf'}" : freshTargets);
            clientShadow(labClient).responses = call -> reply(call.get(1).equals("/api/me") ? 401 : 200, "{}");
            spoolnet.FederationSession exchange = Shadow.newInstanceOf(spoolnet.FederationSession.class);
            ShadowFederationSession result = Shadow.extract(exchange);
            clientShadow(f.homeClient).federationPreparation = (destination, target, account, language) -> {
                assertFalse("Do not prepare a ticket on the stale route", modeChanged);
                assertSame(labClient, destination);
                assertEquals(id, target);
                assertEquals("alice", account);
                return exchange;
            };

            health(f, labClient, new ConnectionRecovery.Snapshot("NeedsLogin", "Not checked", "login", "", ""));
            NativeConnectionSettings settings = f.h.get("connectionSettings");
            assertNotNull(settings);
            assertTrue(settings.login.isEnabled());
            settings.login.performClick();
            f.drainConnections();
            assertEquals("https://login.tailscale.com/a/test", Shadows.shadowOf(f.h.activity).getNextStartedActivity().getDataString());
            assertEquals(0, result.activations);

            health(f, labClient, new ConnectionRecovery.Snapshot("Running", "Verified", "", "", ""));
            f.drain();
            assertEquals(modeChanged ? 0 : 1, result.activations);
            if (modeChanged) {
                assertNull(f.h.get("user"));
                assertFalse(f.h.<Boolean>get("restoringSession"));
            } else assertEquals("external-office", f.h.<JSONObject>get("user").getString("id"));
        }
    }

    private static void health(Fixture f, Client client, ConnectionRecovery.Snapshot value) {
        ReflectionHelpers.callInstanceMethod(f.h.activity, "acceptConnectionHealth",
                ReflectionHelpers.ClassParameter.from(Client.class, client),
                ReflectionHelpers.ClassParameter.from(int.class, f.h.<Integer>get("generation")),
                ReflectionHelpers.ClassParameter.from(ConnectionRecovery.Snapshot.class, value),
                ReflectionHelpers.ClassParameter.from(boolean.class, value.healthy()));
    }

    private void checkOrdinarySignIn(String scenario) throws Exception {
        String id = "b".repeat(52);
        JSONArray allowed = target(id, "Office", "https://office.example.com");
        try (Fixture f = new Fixture(true, allowed)) {
            clientShadow(f.homeClient).federationPreparation = (destination, target, account, language) -> {
                throw new AssertionError("Ordinary server selection must not exchange a trusted login");
            };
            clientShadow(f.officeClient).responses = call -> reply(scenario.equals("signed-out") ? 401 : scenario.equals("unavailable") ? 503 : 200,
                    "{user:{id:'office-local'},csrf:'office-csrf'}");
            if (!scenario.equals("cached")) { f.h.call("refreshServerConnections"); f.drainConnections(); }
            f.tabs().button(f.office.id).performClick(); f.drain();
            assertSame(f.office, f.servers.active()); assertSame(f.officeClient, f.h.get("client"));
            if (scenario.equals("signed-out")) {
                assertNull(f.h.get("user")); assertSingleNormalLogin(f);
            } else assertEquals(scenario.equals("existing") ? "office-local" : "alice", f.h.<JSONObject>get("user").optString("id"));
        }
    }

    @Test public void eachTrustedAliasUsesItsOwnSourceFromAnUnrelatedServer() throws Exception {
        checkGroupedSignIn("home");
        checkGroupedSignIn("studio");
    }

    @Test public void independentTrustedLoginsReuseTheirOwnCachesAndRevocationOnlyClosesItsSource() throws Exception {
        String id = "b".repeat(52);
        JSONArray allowed = target(id, "Office", "https://office.example.com");
        try (Fixture f = new Fixture(true, allowed)) {
            ServerProfiles.Server studio = f.servers.add("Studio");
            studio.plan = new ConnectionPlan("https://studio.example.com", "", ConnectionMode.DIRECT);
            clientShadow(f.homeClient).responses = call -> reply(200, call.get(1).equals("/api/me")
                    ? "{user:{id:'alice'},csrf:'shared-csrf',federation_targets:" + allowed + "}" : allowed);
            f.h.call("refreshServerConnections"); f.drainConnections(); f.drain();
            Client studioClient = ((ConnectionRouter.Candidate<Client>) ReflectionHelpers.getField(
                    f.h.<Map<String, ?>>get("serverRuntimes").get(studio.id), "candidate")).client;
            f.push(studioClient, "ready", "true"); f.push(studioClient, "federation_targets", allowed.toString());
            clientShadow(studioClient).responses = call -> reply(200, call.get(1).equals("/api/me")
                    ? "{user:{id:'alice'},csrf:'shared-csrf'}" : allowed);
            List<Client> destinations = new ArrayList<>();
            for (Client source : List.of(f.homeClient, studioClient)) {
                clientShadow(source).federationPreparation = (destination, target, account, language) -> {
                    assertNotSame(f.officeClient, destination);
                    assertSame(clientShadow(f.officeClient).device, clientShadow(destination).device);
                    assertFalse(destinations.stream().anyMatch(previous -> previous == destination)); destinations.add(destination);
                    return Shadow.newInstanceOf(spoolnet.FederationSession.class);
                };
            }
            String a = NativeFederation.tabKey(f.home, id), d = NativeFederation.tabKey(studio.id, id);
            f.tabs().button(a).performClick(); f.drainConnections(); f.drain();
            ServerProfiles.Server aProfile = f.servers.active(); Client aClient = f.h.get("client");
            assertTrue(aProfile.trusted()); assertEquals(f.home, aProfile.federationSource);
            f.tabs().button(d).performClick(); f.drainConnections(); f.drain();
            ServerProfiles.Server dProfile = f.servers.active(); Client dClient = f.h.get("client");
            assertNotSame(aClient, dClient); assertNotEquals(aProfile.sessionDirectory(new java.io.File("/private/fixture")), dProfile.sessionDirectory(new java.io.File("/private/fixture")));
            assertEquals(2, destinations.size());
            f.select(f.home);
            f.h.set("restoringTrustedServer", dProfile.id); f.h.call("renderNavigation");
            assertSame(dClient, f.h.get("client")); assertEquals(2, destinations.size());
            f.tabs().button(f.office.id).performClick();
            assertSame(f.officeClient, f.h.get("client")); assertEquals("alice", f.h.<JSONObject>get("user").optString("id"));
            f.tabs().button(a).performClick();
            assertSame(aClient, f.h.get("client")); assertEquals(2, destinations.size());
            f.push(f.homeClient, "federation_targets", "[]");
            assertEquals(f.home, f.servers.selected); assertNull(f.tabs().button(a)); assertNotNull(f.tabs().button(d));
            assertFalse(f.h.<Map<?, ?>>get("serverWorkspaces").containsKey(aProfile.id));
            f.drainConnections();
            assertEquals(1, clientShadow(aClient).closes); assertEquals(0, clientShadow(dClient).closes); assertEquals(0, clientShadow(f.officeClient).closes);
            f.tabs().button(d).performClick(); assertSame(dClient, f.h.get("client")); assertEquals(2, destinations.size());
        }
    }

    @Test public void revokingTheClickedAliasDoesNotUseTheOtherSourceOrActivateTheExchange() throws Exception {
        checkGroupedSignIn("revoked");
    }

    @Test public void changingTheClickingPageOrSourceAccountDiscardsTheExchange() throws Exception {
        checkGroupedSignIn("switched");
        checkGroupedSignIn("expired");
        checkGroupedSignIn("account changed");
    }

    private void checkGroupedSignIn(String scenario) throws Exception {
        String id = "b".repeat(52);
        JSONArray peers = target(id, "Office", "https://office.example.com");
        try (Fixture f = new Fixture(true)) {
            ServerProfiles.Server studio = f.servers.add("Studio"), third = f.servers.add("Third");
            studio.plan = new ConnectionPlan("https://studio.example.com", "", ConnectionMode.DIRECT);
            third.plan = new ConnectionPlan("https://third.example.com", "", ConnectionMode.DIRECT);
            f.responses(200, null, "studio-user", "studio-csrf");
            f.h.call("refreshServerConnections"); f.drainConnections(); f.drain();
            Object runtime = f.h.<Map<String, ?>>get("serverRuntimes").get(studio.id);
            Client studioClient = ((ConnectionRouter.Candidate<Client>) ReflectionHelpers.getField(runtime, "candidate")).client;
            f.push(studioClient, "ready", "true");
            f.select(third.id); f.drain();
            String fromHome = NativeFederation.tabKey(f.home, id), fromStudio = NativeFederation.tabKey(studio.id, id);
            f.push(f.homeClient, "federation_targets", peers.toString());
            f.push(studioClient, "federation_targets", peers.toString());
            assertNotNull(f.tabs().button(fromHome));
            assertNotNull(f.tabs().button(fromStudio));
            assertNotNull(f.tabs().button(f.office.id));
            assertEquals(4, f.servers.entries.size());

            clientShadow(f.officeClient).responses = call -> reply(call.get(1).equals("/api/me") ? 401 : 200, "{}");
            clientShadow(f.homeClient).responses = call -> reply(200, call.get(1).equals("/api/me")
                    ? "{user:{id:'alice'},csrf:'shared-csrf'}" : peers);
            clientShadow(studioClient).responses = call -> reply(200, call.get(1).equals("/api/me")
                    ? "{user:{id:'studio-user'},csrf:'studio-csrf'}" : peers);
            spoolnet.FederationSession exchange = Shadow.newInstanceOf(spoolnet.FederationSession.class);
            ShadowFederationSession result = Shadow.extract(exchange);
            List<String> prepared = new ArrayList<>();
            clientShadow(f.homeClient).federationPreparation = (destination, target, account, language) -> {
                assertNotSame(f.officeClient, destination);
                assertSame(clientShadow(f.officeClient).device, clientShadow(destination).device);
                assertEquals(id, target);
                prepared.add(account);
                if (scenario.equals("revoked")) f.push(f.homeClient, "federation_targets", "[]");
                if (scenario.equals("expired")) f.push(f.homeClient, "auth_expired", "{}");
                if (scenario.equals("switched")) f.select(studio.id);
                if (scenario.equals("account changed")) {
                    Object sourceRuntime = f.h.<Map<String, ?>>get("serverRuntimes").get(f.home);
                    ConnectionRouter.Candidate<Client> current = ReflectionHelpers.getField(sourceRuntime, "candidate");
                    ReflectionHelpers.setField(sourceRuntime, "candidate", new ConnectionRouter.Candidate<>(current.endpoint, current.client, current.health,
                            new ConnectionRouter.Session("bob", "{user:{id:'bob'},csrf:'new-csrf',federation_targets:" + peers + "}")));
                    f.h.call("renderNavigation");
                }
                return exchange;
            };
            clientShadow(studioClient).federationPreparation = (destination, target, account, language) -> {
                assertNotSame(f.officeClient, destination);
                assertSame(clientShadow(f.officeClient).device, clientShadow(destination).device);
                assertEquals(id, target);
                prepared.add(account);
                return exchange;
            };
            boolean useStudio = scenario.equals("studio");
            f.tabs().button(useStudio ? fromStudio : fromHome).performClick();
            f.drainConnections(); f.drain();
            assertEquals(List.of(useStudio ? "studio-user" : "alice"), prepared);
            if (scenario.equals("home") || useStudio) {
                assertEquals(1, result.activations);
                assertTrue(f.servers.active().trusted());
                assertNotEquals(f.office.id, f.servers.selected);
                assertTrue(f.tabs().button(useStudio ? fromStudio : fromHome).isSelected());
                assertFalse(f.tabs().button(useStudio ? fromHome : fromStudio).isSelected());
            } else {
                assertEquals(0, result.activations);
                assertSame(scenario.equals("switched") ? studio : third, f.servers.active());
                if (!scenario.equals("switched") && !scenario.equals("account changed")) assertNull(f.tabs().button(fromHome));
                assertNotNull(f.tabs().button(fromStudio));
            }
            assertEquals(5, f.servers.entries.size());
        }
    }

    @Test public void ordinaryTabRestoresItsOwnCachedLoginWithoutExchange() throws Exception { checkOrdinarySignIn("cached"); }
    @Test public void ordinaryTabKeepsItsExistingLocalAccount() throws Exception { checkOrdinarySignIn("existing"); }
    @Test public void signedOutOrdinaryTabDoesNotUseAnAuthorizedSource() throws Exception { checkOrdinarySignIn("signed-out"); }
    @Test public void ordinaryTabDoesNotTreatNetworkFailureAsSignedOut() throws Exception { checkOrdinarySignIn("unavailable"); }

    @Test public void signedOutDestinationWithNoSourceShowsTheNormalLoginForm() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.homeClient, "auth_expired", "{}");
            clientShadow(f.officeClient).responses = call -> reply(401, "{}");
            f.tabs().button(f.office.id).performClick();
            f.drain();
            f.h.call("closeDrawer"); idle();
            assertSame(f.office, f.servers.active());
            assertNull(f.h.get("connectingView"));
            assertNotNull(f.h.get("username"));
        }
    }

    @Test public void workspaceSnapshotsUpdateTrustedTabsWithoutReselectingTheServer() throws Exception {
        String lab = "b".repeat(52), office = "c".repeat(52);
        JSONArray labOnly = target(lab, "Lab", "https://lab.example.com");
        JSONArray targets = target(office, "Office", "https://office.example.com").put(labOnly.getJSONObject(0));
        try (Fixture f = new Fixture(true)) {
            String labKey = NativeFederation.tabKey(f.home, lab), officeKey = NativeFederation.tabKey(f.home, office);
            JSONObject account = f.h.get("user");
            f.push(f.homeClient, "federation_targets", targets.toString());
            assertNotNull(f.tabs().button(labKey));
            assertNotNull(f.tabs().button(officeKey));
            assertNotNull(f.tabs().button(f.office.id));

            f.responses(200, targets);
            f.checkDirectRoute();
            f.connections.jobs.remove(0).run(); // 在线撤销不能被已经读到旧名单的会话检查覆盖。
            f.listener(f.homeClient).onEvent("federation_targets", "[]");
            idle();
            assertNull(f.tabs().button(labKey));
            assertNotNull(f.tabs().button(f.office.id));
            assertFalse(f.tabs().button(f.office.id).getContentDescription().toString().contains("Trusted server"));
            assertSame(account, f.h.get("user"));

            f.select(f.office.id); f.drain();
            f.push(f.homeClient, "federation_targets", labOnly.toString());
            assertNotNull(f.tabs().button(labKey));
            f.select(f.home);
            assertNotNull(f.tabs().button(labKey));
            assertNotNull(f.tabs().button(f.office.id));
        }
    }

    @Test public void recheckingTheSameSessionAddsAndRemovesTrustedTabsWithoutChangingTheAccount() throws Exception {
        String added = "b".repeat(52), replacement = "c".repeat(52);
        try (Fixture f = new Fixture(true)) {
            String addedKey = NativeFederation.tabKey(f.home, added), replacementKey = NativeFederation.tabKey(f.home, replacement);
            JSONObject account = f.h.get("user");
            EventStream stream = f.stream(f.homeClient);
            assertNull(f.tabs().button(addedKey));

            f.responses(200, target(added, "Lab", "https://lab.example.com"), "alice", "other-csrf");
            f.checkDirectRoute(); f.drainConnections();
            assertEquals("Spool → Lab, Trusted server", f.tabs().button(addedKey).getContentDescription());
            assertSame(account, f.h.get("user"));
            assertEquals("shared-csrf", f.h.<String>get("csrf"));

            f.responses(503, target(replacement, "Studio", "https://studio.example.com"));
            f.checkDirectRoute(); f.drainConnections();
            assertNotNull(f.tabs().button(addedKey));
            assertNull(f.tabs().button(replacementKey));

            f.responses(200, target(replacement, "Studio", "https://studio.example.com"));
            f.checkDirectRoute(); f.drainConnections();
            assertNull(f.tabs().button(addedKey));
            assertNotNull(f.tabs().button(replacementKey));

            f.responses(200, new JSONArray());
            f.checkDirectRoute(); f.drainConnections();
            assertNull(f.tabs().button(replacementKey));

            f.responses(200, target(added, "Lab", "https://lab.example.com"));
            f.checkDirectRoute(); f.drainConnections();
            assertNotNull(f.tabs().button(addedKey));
            f.responses(200);
            f.checkDirectRoute(); f.drainConnections();
            assertNull(f.tabs().button(addedKey));
            assertSame(account, f.h.get("user"));
            assertSame(f.homeClient, f.h.get("client"));
            assertSame(stream, f.h.get("workspaceStream"));
        }
    }

    @Test public void anInactiveProbeRefreshesTrustedTabsIfItsServerIsSelectedBeforeDelivery() throws Exception {
        String id = "b".repeat(52);
        try (Fixture f = new Fixture(true)) {
            String key = NativeFederation.tabKey(f.office.id, id);
            f.responses(200, target(id, "Lab", "https://lab.example.com"));
            f.h.call("refreshServerConnections");
            f.connections.jobs.remove(0).run(); // Office /me was sampled while Home was selected.
            f.select(f.office.id);
            assertNull(f.tabs().button(key));
            idle();
            assertEquals("Office → Lab, Trusted server", f.tabs().button(key).getContentDescription());
            assertSame(f.officeClient, f.h.get("client"));
        }
    }

    @Test public void aSessionCheckAlreadyInFlightCannotBringBackTrustedTabsAfterSessionExpiry() throws Exception {
        String original = "b".repeat(52), late = "c".repeat(52);
        try (Fixture f = new Fixture(true, target(original, "Lab", "https://lab.example.com"))) {
            String oldKey = NativeFederation.tabKey(f.home, original), lateKey = NativeFederation.tabKey(f.home, late);
            assertNotNull(f.tabs().button(oldKey));
            f.responses(200, target(late, "Studio", "https://studio.example.com"));
            f.checkDirectRoute();
            f.connections.jobs.remove(0).run(); // /me has completed, but its result has not been delivered.
            f.h.call("expire"); idle();
            assertNull(f.h.get("user"));
            assertNull(f.tabs().button(oldKey));
            assertNull(f.tabs().button(lateKey));
            f.select(f.office.id); f.select(f.home);
            assertNull(f.h.get("user"));
            assertNull(f.tabs().button(lateKey));
        }
    }

    @Test public void healthyTransportWaitsForSocketReadinessAndSignedOutTabsTurnBlue() throws Exception {
        try (Fixture f = new Fixture(false)) {
            assertEquals("connected", f.status(f.home));
            assertEquals("connecting", f.status(f.office.id));
            f.drain(); f.push(f.officeClient, "ready", "true");
            assertEquals("connected", f.status(f.office.id));
            f.push(f.officeClient, "auth_expired", "{}");
            assertEquals("login", f.status(f.office.id));
            NativeServerTabs tabs = ReflectionHelpers.getField(f.h.<NativeSidebar>get("sidebar"), "serverTabs");
            assertEquals("Office, Connected · Sign-in required", tabs.button(f.office.id).getContentDescription());
            assertEquals("connected", f.status(f.home));
            assertEquals(f.home, f.servers.selected);
        }
    }

    @Test public void socketInterruptionsTurnRedImmediatelyAndOnlyTheirOwnReadyRestoresGreen() throws Exception {
        try (Fixture f = new Fixture(true)) {
            for (String id : new String[] {f.office.id, f.home}) {
                Client client = id.equals(f.home) ? f.homeClient : f.officeClient;
                f.push(client, "transport_error", "{}");
                assertEquals("error", f.status(id));
                assertEquals("alice", f.servers.entries.stream().filter(server -> server.id.equals(id)).findFirst().orElseThrow().account.optString("id"));
                f.drainConnections(); f.drain();
                assertEquals("A successful health/session check cannot establish the realtime connection", "error", f.status(id));
                Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(650)); f.drain();
                f.push(client, "ready", "true");
                assertEquals("connected", f.status(id));
                assertEquals("connected", f.status(id.equals(f.home) ? f.office.id : f.home));
            }
        }
    }

    @Test public void theFirstFailedHealthSampleTurnsRedWithoutWaitingForTransportResetThresholds() throws Exception {
        try (Fixture f = new Fixture(true)) {
            Client client = f.homeClient;
            ReflectionHelpers.callInstanceMethod(f.h.activity, "acceptConnectionHealth",
                    ReflectionHelpers.ClassParameter.from(Client.class, client),
                    ReflectionHelpers.ClassParameter.from(int.class, f.h.<Integer>get("generation")),
                    ReflectionHelpers.ClassParameter.from(ConnectionRecovery.Snapshot.class,
                            new ConnectionRecovery.Snapshot("Running", "Failed", "network", "", "offline")),
                    ReflectionHelpers.ClassParameter.from(boolean.class, true));
            assertEquals("error", f.status(f.home));
            assertEquals("connected", f.status(f.office.id));
            MainActivityGoalTest.ShadowClient shadow = org.robolectric.shadow.api.Shadow.extract(f.officeClient);
            shadow.healthResponse = "{state:'NeedsLogin',https:'Not checked',problem:'login'}";
            f.h.call("refreshServerConnections"); f.drainConnections();
            assertEquals("Node enrollment is pending transport setup, not Spool sign-in", "connecting", f.status(f.office.id));
        }
    }

    @Test public void serverNamesFollowTheirOwningConnectionsAcrossTabSwitches() throws Exception {
        try (Fixture f = new Fixture(true)) {
            assertEquals("Server from sign-in", footerServerName(f));
            f.push(f.homeClient, "server", "{server_name:'家里的服务器'}");
            f.push(f.officeClient, "server", "{server_name:'Office server'}");
            assertEquals("家里的服务器", footerServerName(f));
            assertEquals("Office", f.office.name);
            f.select(f.office.id);
            assertEquals("Office server", footerServerName(f));
            f.drain();
            f.push(f.homeClient, "server", "{server_name:'Home renamed in background'}");
            assertEquals("Office server", footerServerName(f));
            f.select(f.home);
            assertEquals("Home renamed in background", footerServerName(f));
            f.drain();
            f.push(f.homeClient, "server", "{server_name:''}");
            assertEquals("Home", footerServerName(f));
        }
    }

    @Test public void sessionNotificationsStayWithTheirServerAcrossLiveStreamHandoffs() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.push(f.officeClient, "navigation", navigation("Office"));
            f.push(f.officeClient, "session_notification", "{chat:'chat-one',user:'alice',kind:'approval'}");
            assertEquals("", f.h.<NativeSessionNotifications>get("sessionNotifications").kind("chat-one"));
            f.select(f.office.id);
            assertEquals("approval", f.h.<NativeSessionNotifications>get("sessionNotifications").kind("chat-one"));
            assertTrue(f.h.<NativeSidebar>get("sidebar").chatRows.stream().allMatch(row -> row.notification != null));
            f.drain();
            f.push(f.homeClient, "session_notification", "{chat:'chat-one',user:'alice',kind:'failed'}");
            assertEquals("approval", f.h.<NativeSessionNotifications>get("sessionNotifications").kind("chat-one"));
            f.select(f.home);
            assertEquals("failed", f.h.<NativeSessionNotifications>get("sessionNotifications").kind("chat-one"));
            f.drain();
            f.push(f.officeClient, "session_notification", "{chat:'chat-one',user:'bob',kind:'completed'}");
            f.select(f.office.id);
            assertEquals("approval", f.h.<NativeSessionNotifications>get("sessionNotifications").kind("chat-one"));
        }
    }

    @Test public void closingTheServerSidebarReadsOnlyTheRestoredChatWhenFocused() throws Exception {
        for (boolean focused : new boolean[] {true, false}) try (Fixture f = new Fixture(true)) {
            JSONObject officeNavigation = new JSONObject(navigation("Office"));
            officeNavigation.getJSONArray("chats").put(new JSONObject().put("id", "chat-two").put("title", "Other chat")
                    .put("project_id", "project-one").put("backend", "codex"));
            f.push(f.officeClient, "navigation", officeNavigation.toString());
            f.push(f.officeClient, "session_notification", "{chat:'chat-one',user:'alice',kind:'approval'}");
            f.push(f.officeClient, "session_notification", "{chat:'chat-two',user:'alice',kind:'failed'}");
            f.select(f.office.id); f.drain();
            f.push(f.officeClient, "navigation", officeNavigation.toString());
            f.h.controller.windowFocusChanged(focused);
            NativeSessionNotifications notifications = f.h.get("sessionNotifications");
            assertTrue(f.h.<Boolean>get("sidebarPageDeferred"));
            assertEquals("approval", notifications.kind("chat-one"));
            assertEquals("failed", notifications.kind("chat-two"));
            f.h.call("closeDrawer");
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(250)); f.drain();
            assertFalse(f.h.<Boolean>get("sidebarPageDeferred"));
            assertEquals("chat-one", f.h.<String>get("chatID"));
            assertEquals(focused ? "" : "approval", notifications.kind("chat-one"));
            assertEquals("failed", notifications.kind("chat-two"));
            f.h.controller.windowFocusChanged(true);
            assertEquals("", notifications.kind("chat-one"));
            assertEquals("failed", notifications.kind("chat-two"));
            f.h.call("openSidebar");
            for (NativeChatLink row : f.h.<NativeSidebar>get("sidebar").chatRows) {
                if (row.chatID.equals("chat-one")) assertNull(row.notification);
                if (row.chatID.equals("chat-two")) assertNotNull(row.notification);
            }
        }
    }

    private String footerServerName(Fixture f) {
        NativeSidebar sidebar = f.h.get("sidebar");
        android.widget.LinearLayout copy = (android.widget.LinearLayout) sidebar.footer.getChildAt(1);
        return ((android.widget.TextView) copy.getChildAt(1)).getText().toString();
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
                assertNull(f.h.get("history")); assertNull(f.h.get("markwonHistory")); assertNull(f.h.get("transcriptViews")); assertNull(f.h.get("composerView"));
                assertSame(homeEvents, f.listener(f.homeClient)); assertSame(officeEvents, f.listener(f.officeClient));
                assertSame(home, f.stream(f.homeClient)); assertSame(office, f.stream(f.officeClient));
                assertFalse(f.socket(f.homeClient).closed); assertFalse(f.socket(f.officeClient).closed);
                assertTrue("Switching servers must not queue workspace closes", closeWork.jobs.isEmpty());
                assertEquals(subscriptions, MainActivityGoalTest.ShadowClient.workspaceSubscriptions.size());
            }
            f.h.<JSONArray>get("chats").getJSONObject(0).remove("transcript");
            f.h.<JSONObject>get("chat").remove("transcript");
            sidebar.chatRows.get(0).open.performClick();
            assertEquals("", f.h.<String>get("panel")); assertNotNull(f.h.get("markwonHistory")); assertNotNull(f.h.get("composerView"));
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

    @Test public void anInconclusiveBackgroundProbePreservesTheSameClientsVerifiedSessionUntil401() throws Exception {
        String id = "b".repeat(52);
        JSONArray allowed = target(id, "Lab", "https://lab.example.com");
        try (Fixture f = new Fixture(true)) {
            String key = NativeFederation.tabKey(f.office.id, id);
            f.push(f.officeClient, "federation_targets", allowed.toString());
            assertNotNull(f.tabs().button(key));
            MainActivityWorkspaceEventsTest.ShadowStream socket = f.socket(f.officeClient);
            clientShadow(f.officeClient).responses = call -> reply(503, "{}");
            f.h.call("refreshServerConnections"); f.drainConnections();
            assertFalse(socket.closed);

            f.select(f.office.id);
            assertSame(f.officeClient, f.h.get("client"));
            assertEquals("alice", f.h.<JSONObject>get("user").getString("id"));
            assertNotNull(f.tabs().button(key));
            assertSame(socket, f.socket(f.officeClient));

            clientShadow(f.officeClient).responses = call -> reply(401, "{}");
            f.checkDirectRoute(); f.drainConnections();
            assertNull(f.h.get("user"));
            assertNull(f.tabs().button(key));
            assertEquals(0, f.office.account.length());
            assertSingleNormalLogin(f);
        }
    }

    private void assertSingleNormalLogin(Fixture f) {
        f.h.call("closeDrawer"); idle();
        assertNull(f.h.get("connectingView"));
        assertNotNull(f.h.get("username"));
    }

    @Test public void healthSuccessBeforeTheFirstSocketReadyKeepsTheSessionCacheAndRetryOwner() throws Exception {
        try (Fixture f = new Fixture(false)) {
            f.drain();
            f.push(f.officeClient, "navigation", navigation("Office before first ready"));
            Object runtime = f.h.<Map<String, ?>>get("serverRuntimes").get(f.office.id);
            Object feed = ReflectionHelpers.getField(runtime, "feed");
            NativeWorkspaceEvents<?> events = ReflectionHelpers.getField(feed, "events");
            Object cache = f.h.<Map<String, ?>>get("serverWorkspaces").get(f.office.id);
            assertFalse(events.ready());
            f.push(f.officeClient, "transport_error", "{}");
            f.drainConnections(); f.drain();
            f.h.call("refreshServerConnections"); f.drainConnections(); f.drain();
            assertSame("Healthy HTTP must retain the first socket's retry policy", feed, ReflectionHelpers.getField(runtime, "feed"));
            assertSame(events, ReflectionHelpers.getField(feed, "events"));
            assertSame(cache, f.h.<Map<String, ?>>get("serverWorkspaces").get(f.office.id));
            assertNotNull(((ConnectionRouter.Candidate<?>) ReflectionHelpers.getField(runtime, "candidate")).session);
            assertEquals("Office before first ready project", f.cachedProject(f.office.id).getString("name"));
            assertFalse(events.ready());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(650)); f.drain();
            f.push(f.officeClient, "ready", "true");
            assertTrue(events.ready());
            assertSame(feed, ReflectionHelpers.getField(runtime, "feed"));
        }
    }

    @Test public void backgroundNetworkFallbackPreservesTheSelectedDisabledSidebar() throws Exception {
        try (Fixture f = new Fixture(true)) {
            f.enable(f.home, false); f.drainConnections();
            f.h.call("closeSettings"); f.h.call("openSidebar");
            NativeSidebar sidebar = f.h.get("sidebar");
            assertNotNull(sidebar);
            Object cache = f.h.<Map<String, ?>>get("serverWorkspaces").get(f.home);
            f.office.plan = new ConnectionPlan(f.office.plan.direct, "https://office.example.ts.net", ConnectionMode.DIRECT, true);
            MainActivityGoalTest.ShadowClient shadow = org.robolectric.shadow.api.Shadow.extract(f.officeClient);
            shadow.healthResponse = "{state:'Running',https:'Failed',problem:'network'}";
            f.h.call("refreshServerConnections"); f.drainConnections(); f.drain();
            Object runtime = f.h.<Map<String, ?>>get("serverRuntimes").get(f.office.id);
            ConnectionRouter.Candidate<Client> candidate = ReflectionHelpers.getField(runtime, "candidate");
            assertEquals(ConnectionMode.TAILSCALE, candidate.endpoint.mode);
            assertEquals(f.home, f.servers.selected);
            assertFalse(f.servers.active().enabled);
            assertNull(f.h.get("client"));
            assertSame(sidebar, f.h.get("sidebar"));
            assertSame(cache, f.h.<Map<String, ?>>get("serverWorkspaces").get(f.home));
            assertEquals("sidebar", f.h.<String>get("panel"));
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
