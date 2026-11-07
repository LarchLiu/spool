package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.ScrollView;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;
import spoolnet.Client;
import spoolnet.Spoolnet;

/** Actual startup/session callbacks and dialog windows, without a real node or account. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class, MainActivityConnectionTest.ShadowSpoolnet.class,
        MainActivityWorkspaceEventsTest.ShadowStream.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityConnectionTest {
    @Implements(value = Spoolnet.class, isInAndroidSdk = false)
    public static class ShadowSpoolnet {
        static String mode, directory, origin;
        static boolean failDirect;
        static String tailnetHealth;
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected static void updateInterfaces(String value) {}
        @Implementation protected static Client newDirectClient(String data, String address) throws Exception {
            mode = "direct"; directory = data; origin = address;
            if (failDirect) throw new java.io.IOException("Direct connection unavailable");
            return new Client("unused", address);
        }
        @Implementation protected static Client newClient(String data, String address) {
            mode = "tailscale"; directory = data; origin = address;
            Client client = new Client("unused", address);
            if (tailnetHealth != null) {
                MainActivityGoalTest.ShadowClient shadow = org.robolectric.shadow.api.Shadow.extract(client);
                shadow.healthResponse = tailnetHealth;
            }
            return client;
        }
    }

    static MainActivityGoalTest.Queue connectionQueue(MainActivityGoalTest.Harness h) {
        ExecutorService original = h.get("connectionWorker");
        original.shutdown();
        MainActivityGoalTest.Queue connections = new MainActivityGoalTest.Queue();
        h.set("connectionWorker", connections);
        ConnectionRecovery.Scheduler scheduler = new ConnectionRecovery.Scheduler() {
            public void post(Runnable task) {}
            public void postDelayed(Runnable task, long delay) {}
            public void remove(Runnable task) {}
            public long now() { return 0; }
        };
        h.set("recovery", new ConnectionRecovery(task -> {}, scheduler));
        h.set("monitor", new ConnectionMonitor(task -> {}, scheduler));
        return connections;
    }

    static void drain(MainActivityGoalTest.Queue connections) {
        while (!connections.jobs.isEmpty()) { connections.jobs.remove(0).run(); idle(); }
    }

    private void select(MainActivityGoalTest.Harness h, String id) {
        ReflectionHelpers.callInstanceMethod(h.activity, "selectServer", ReflectionHelpers.ClassParameter.from(String.class, id));
    }

    private void navigation(MainActivityGoalTest.Harness h, String name) throws Exception {
        ReflectionHelpers.callInstanceMethod(h.activity, "acceptNavigation", ReflectionHelpers.ClassParameter.from(JSONObject.class,
                new JSONObject("{projects:[{id:'project-one',name:'" + name + " project',can_create:true}],chats:[{id:'chat-one',title:'" + name
                        + " chat',project_id:'project-one',backend:'codex',updated_at:1,can_control:true,participants:[]}]}")));
    }

    private void directory(MainActivityGoalTest.Harness h, int revision, String name) throws Exception {
        ReflectionHelpers.callInstanceMethod(h.activity, "acceptProfiles", ReflectionHelpers.ClassParameter.from(JSONObject.class,
                new JSONObject("{revision:" + revision + ",people:[{id:'alice',nickname:'" + name + "'}]}")));
    }

    @Test public void serverTabsRestoreTheirOwnWorkspaceBeforeAnyConnectionOrRefreshCompletes() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h);
            try {
                ServerProfiles servers = h.get("serverProfiles"); String home = servers.selected;
                servers.active().enabled = false;
                navigation(h, "Home"); directory(h, 8, "Home Alice");
                h.composer().input.setText("Home draft"); h.call("openSidebar");
                NativeSidebarState state = h.get("sidebarState");
                state.projectLimit = 15; state.recentLimit = 25; state.scrollY = 90;
                state.expanded.clear(); state.chatLimits.put("project-one", 15);
                NativeHistoryScroll.State history = h.get("historyScrollState"); history.following = false; history.top = 175;
                NativeActivityList.State tool = new NativeActivityList.State(); tool.open = true;
                h.<java.util.Map<String, NativeActivityList.State>>get("activityStates").put("turn-one", tool);
                ServerProfiles.Server office = servers.add("Office"); office.enabled = false;
                select(h, office.id);
                assertEquals(0, h.<NativeSidebar>get("sidebar").projectRows.size());
                h.set("user", new JSONObject("{id:'alice'}")); h.set("csrf", "office-csrf");
                h.set("chatID", "chat-one"); h.set("projectID", "project-one"); h.set("chat", h.chat("chat-one"));
                h.call("closeDrawer"); h.call("render"); navigation(h, "Office"); directory(h, 1, "Office Alice");
                h.composer().input.setText("Office draft"); h.call("openSidebar");
                state.projectLimit = 5; state.recentLimit = 15; state.scrollY = 45;
                state.expanded.add("project-one"); state.chatLimits.put("project-one", 25);
                history.following = false; history.top = 360;

                select(h, home);
                NativeSidebar sidebar = h.get("sidebar");
                assertEquals("Home project", sidebar.projectRows.get(0).title.getText().toString());
                assertEquals("Home chat", sidebar.chatRows.get(0).title.getText().toString());
                assertEquals(15, state.projectLimit); assertEquals(25, state.recentLimit); assertEquals(90, state.scrollY);
                assertFalse(state.expanded.contains("project-one")); assertEquals(15, state.limit("project-one"));
                assertEquals(175, history.top); assertFalse(history.following);
                assertSame(tool, h.<java.util.Map<String, NativeActivityList.State>>get("activityStates").get("turn-one"));
                assertTrue(footerText(sidebar).contains("Home Alice")); assertFalse(footerText(sidebar).contains("Office Alice"));
                assertNull(h.get("user")); assertNull(h.get("client")); assertEquals("", h.<String>get("csrf"));
                assertFalse(sidebar.projectRows.get(0).create.isEnabled()); assertFalse(sidebar.chatRows.get(0).open.isEnabled());
                assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
                assertFalse(h.activity.getPreferences(Activity.MODE_PRIVATE).getAll().toString().contains("Home project"));
                assertFalse(h.activity.getPreferences(Activity.MODE_PRIVATE).getAll().toString().contains("Home draft"));
                directory(h, 7, "Stale Alice");
                assertEquals("Home Alice", h.<JSONArray>get("profiles").getJSONObject(0).getString("nickname"));
                state.expanded.add("project-one"); state.scrollY = 120;

                select(h, office.id);
                sidebar = h.get("sidebar");
                assertEquals("Office project", sidebar.projectRows.get(0).title.getText().toString());
                assertEquals("Office chat", sidebar.chatRows.get(0).title.getText().toString());
                assertEquals(5, state.projectLimit); assertEquals(15, state.recentLimit); assertEquals(45, state.scrollY);
                assertTrue(state.expanded.contains("project-one")); assertEquals(25, state.limit("project-one"));
                assertEquals(360, history.top); assertFalse(history.following);
                assertTrue(footerText(sidebar).contains("Office Alice"));
                assertTrue(h.<java.util.Map<?, ?>>get("activityStates").isEmpty());
                select(h, home);
                assertTrue(state.expanded.contains("project-one")); assertEquals(120, state.scrollY);
                h.set("user", new JSONObject("{id:'alice'}")); h.call("restoreServerWorkspace"); h.call("closeDrawer"); h.call("render");
                assertEquals("Home draft", h.composer().input.getText().toString());
                h.call("openSidebar");
                navigation(h, "Updated home");
                assertEquals("Updated home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                assertEquals("sidebar", h.<String>get("panel"));
                select(h, office.id);
                assertEquals("Office project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                h.set("user", new JSONObject("{id:'alice'}")); h.call("restoreServerWorkspace"); h.call("closeDrawer"); h.call("render");
                assertEquals("Office draft", h.composer().input.getText().toString());
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void reconnectAndNetworkHandoffKeepProjectsUntilNewNavigationOrADefinitiveFailure() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h);
            try {
                navigation(h, "Home"); h.composer().input.setText("Unsent during reconnect"); h.call("openSidebar");
                h.call("beginNetworkHandoff");
                assertEquals("Home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                Client next = new Client("unused", "https://home.example.ts.net");
                ConnectionPlan plan = new ConnectionPlan("", "https://home.example.ts.net", ConnectionMode.TAILSCALE, false);
                h.<ServerProfiles>get("serverProfiles").active().plan = plan; h.set("connectionPlan", plan);
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(new ConnectionPlan.Endpoint(ConnectionMode.TAILSCALE, plan.tailscale), next,
                        new ConnectionRecovery.Snapshot("Starting", "Not checked", "starting", "", ""), null, false);
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                for (String problem : new String[] {"starting", "network", "offline", "status"}) {
                    health(h, problem, false);
                    assertEquals("Home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                    assertEquals("Home chat", h.<NativeSidebar>get("sidebar").chatRows.get(0).title.getText().toString());
                }
                response(503, "{error:'Temporarily unavailable'}"); health(h, "", true); h.io.run();
                assertEquals("Home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                response(200, "{user:{id:'alice'},csrf:'restored-csrf'}"); health(h, "", true); h.io.run();
                assertEquals("Home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                assertEquals("Unsent during reconnect", h.composer().input.getText().toString());
                navigation(h, "Fresh home");
                assertEquals("Fresh home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                health(h, "endpoint", false);
                assertEquals(0, h.<NativeSidebar>get("sidebar").projectRows.size()); assertEquals(0, h.<JSONArray>get("chats").length());
                assertEquals("alice", h.<ServerProfiles>get("serverProfiles").active().account.getString("id"));
                assertEquals("Unsent during reconnect", h.composer().input.getText().toString());
                assertEquals("sidebar", h.<String>get("panel"));
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void oldServerResponsesAndExpiredSessionsCannotReviveOrReplaceAnotherTabsCache() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h);
            try {
                ServerProfiles servers = h.get("serverProfiles"); String home = servers.selected; servers.active().enabled = false;
                navigation(h, "Home"); h.call("openSidebar");
                ServerProfiles.Server office = servers.add("Office"); office.enabled = false;
                select(h, office.id);
                h.set("user", new JSONObject("{id:'alice'}")); h.set("chatID", "chat-one"); h.set("chat", h.chat("chat-one")); h.set("projectID", "project-one");
                h.call("render"); navigation(h, "Office");
                select(h, home);
                h.set("user", new JSONObject("{id:'alice'}")); h.set("client", new Client("unused", "https://home.example.com"));
                workspaceResponses(); h.call("refreshWorkspace");
                while (!h.io.jobs.isEmpty()) h.io.jobs.remove(0).run(); // Replies are in flight on the main queue.
                select(h, office.id); idle();
                assertEquals("Office project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                select(h, home);
                assertEquals("Home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                h.call("expire");
                assertEquals(0, h.<NativeSidebar>get("sidebar").projectRows.size());
                assertFalse(h.<java.util.Map<?, ?>>get("serverWorkspaces").containsKey(home));
                select(h, office.id);
                assertEquals("Office project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                select(h, home);
                assertEquals(0, h.<NativeSidebar>get("sidebar").projectRows.size());
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void workspaceCachesAreMemoryOnlyAndADifferentAccountCannotReuseThem() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h);
            try {
                ServerProfiles servers = h.get("serverProfiles"); String home = servers.selected; servers.active().enabled = false;
                navigation(h, "Home"); h.call("openSidebar");
                ServerProfiles.Server office = servers.add("Office"); office.enabled = false;
                select(h, office.id); select(h, home);
                assertEquals("Home project", h.<NativeSidebar>get("sidebar").projectRows.get(0).title.getText().toString());
                try (ActivityController<MainActivity> recreated = Robolectric.buildActivity(MainActivity.class).setup()) {
                    assertTrue(((java.util.Map<?, ?>) ReflectionHelpers.getField(recreated.get(), "serverWorkspaces")).isEmpty());
                    assertEquals(0, ((JSONArray) ReflectionHelpers.getField(recreated.get(), "projects")).length());
                    assertEquals(0, ((NativeSidebar) ReflectionHelpers.getField(recreated.get(), "sidebar")).projectRows.size());
                }
                Client next = new Client("unused", "https://home.example.com");
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(new ConnectionPlan.Endpoint(ConnectionMode.DIRECT, "https://home.example.com"), next,
                        new ConnectionRecovery.Snapshot("Running", "Verified", "", "", ""), new ConnectionRouter.Session("bob", "{user:{id:'bob'},csrf:'bob-csrf'}"));
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                assertEquals("bob", h.<JSONObject>get("user").getString("id")); assertEquals("bob-csrf", h.<String>get("csrf"));
                assertEquals(0, h.<NativeSidebar>get("sidebar").projectRows.size()); assertEquals("", h.<String>get("chatID"));
                assertFalse(h.<java.util.Map<?, ?>>get("serverWorkspaces").containsKey(home));
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void applicationStartsInTheSidebar() {
        try (org.robolectric.android.controller.ActivityController<MainActivity> controller = org.robolectric.Robolectric.buildActivity(MainActivity.class).setup()) {
            assertEquals("sidebar", ReflectionHelpers.getField(controller.get(), "panel"));
            assertNotNull(ReflectionHelpers.getField(controller.get(), "sidebar"));
        }
    }

    private String footerText(NativeSidebar sidebar) {
        StringBuilder result = new StringBuilder();
        collectText(sidebar.footer, result); return result.toString();
    }

    private void collectText(View view, StringBuilder result) {
        if (view instanceof android.widget.TextView) result.append(((android.widget.TextView) view).getText()).append('\n');
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) collectText(((ViewGroup) view).getChildAt(i), result);
    }

    @Test public void disconnectedConnectingAndRetryingKeepTheAccountAndConnectWithoutShowingLogin() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.Queue connections = connectionQueue(h);
            ShadowSpoolnet.failDirect = false;
            try {
                h.<JSONObject>get("user").put("nickname", "Alice");
                ServerProfiles profiles = h.get("serverProfiles");
                ConnectionPlan plan = new ConnectionPlan("https://saved.example.com", "", ConnectionMode.DIRECT, false);
                profiles.active().plan = plan; h.set("connectionPlan", plan); h.set("connectionDraft", plan);
                h.call("openSidebar"); h.call("disconnect"); drain(connections); h.call("closeSettings");
                NativeSidebar sidebar = h.get("sidebar");
                assertTrue(footerText(sidebar).contains("Alice")); assertFalse(footerText(sidebar).contains("Sign in"));
                assertEquals("Connect", sidebar.connection.getContentDescription());
                assertNull(h.get("user")); assertEquals("", h.<String>get("csrf"));
                ShadowSpoolnet.failDirect = true;
                sidebar.connection.performClick();
                sidebar = h.get("sidebar");
                assertTrue(footerText(sidebar).contains("Alice")); assertEquals("Connecting…", sidebar.connection.getContentDescription());
                assertFalse(sidebar.connection.isEnabled()); assertEquals("sidebar", h.<String>get("panel"));
                drain(connections);
                sidebar = h.get("sidebar");
                assertTrue(footerText(sidebar).contains("Alice")); assertEquals("Retry", sidebar.connection.getContentDescription());
                assertNull(h.get("user")); assertFalse(footerText(sidebar).contains("Sign in"));
                ShadowSpoolnet.failDirect = false;
                response(200, "{user:{id:'alice',nickname:'Alice'},csrf:'restored-csrf'}");
                sidebar.connection.performClick(); drain(connections); h.io.jobs.clear();
                sidebar = h.get("sidebar");
                assertTrue(footerText(sidebar).contains("Alice")); assertNull(sidebar.connection);
                assertEquals("alice", h.<JSONObject>get("user").getString("id"));
                response(200, "{ok:true}"); h.call("logout"); h.io.run();
                sidebar = h.get("sidebar");
                assertTrue(footerText(sidebar).contains("Sign in")); assertFalse(footerText(sidebar).contains("Alice"));
                assertEquals(0, profiles.active().account.length());
            } finally {
                ShadowSpoolnet.failDirect = false;
                h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
            }
        }
    }

    @Test public void cachedAccountsStayWithTheirServerAndNeverAuthenticateTheWorkspace() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            connectionQueue(h);
            try {
                h.<JSONObject>get("user").put("nickname", "Alice"); h.call("openSidebar"); h.call("disconnect");
                ServerProfiles profiles = h.get("serverProfiles"); String first = profiles.selected;
                ServerProfiles.Server second = profiles.add("Office"); second.enabled = false;
                second.account = new JSONObject("{id:'bob',nickname:'Bob'}");
                select(h, second.id);
                assertTrue(footerText(h.get("sidebar")).contains("Bob")); assertFalse(footerText(h.get("sidebar")).contains("Alice"));
                assertNull(h.get("user")); assertNull(h.get("client")); assertEquals("", h.<String>get("csrf"));
                select(h, first);
                assertTrue(footerText(h.get("sidebar")).contains("Alice")); assertFalse(footerText(h.get("sidebar")).contains("Bob"));
                assertNull(h.get("user")); assertNull(h.get("client"));
                assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void tailnetStartupHandoffRestoresTheSessionWhenHTTPSBecomesReadyAndLoadsWorkspaceData() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h); MainActivityWorkspaceEventsTest.install(h); workspaceResponses();
            try {
                h.<JSONObject>get("user").put("nickname", "Alice");
                h.composer().input.setText("Draft during tailnet startup");
                ConnectionPlan plan = new ConnectionPlan("", "https://private.example.ts.net", ConnectionMode.TAILSCALE);
                h.<ServerProfiles>get("serverProfiles").active().plan = plan; h.set("connectionPlan", plan); h.set("connectionDraft", plan);
                h.call("openSidebar");
                Client next = new Client("unused", "https://private.example.ts.net");
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(
                        new ConnectionPlan.Endpoint(ConnectionMode.TAILSCALE, "https://private.example.ts.net"), next,
                        new ConnectionRecovery.Snapshot("Starting", "Not checked", "starting", "", ""), null);
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                assertTrue(footerText(h.get("sidebar")).contains("Alice"));
                assertEquals("Connecting…", h.<NativeSidebar>get("sidebar").connection.getContentDescription());
                assertTrue(h.io.jobs.isEmpty());
                health(h, "", true);
                assertEquals("Verified HTTPS must continue to the Spool session check", 1, h.io.jobs.size());
                h.io.run();
                assertFalse(h.<Boolean>get("routeLoginRequired")); assertFalse(h.<Boolean>get("restoringSession"));
                assertEquals("Draft during tailnet startup", h.composer().input.getText().toString());
                drainWorkspace(h, false);
                assertEquals(1, h.<org.json.JSONArray>get("projects").length());
                assertEquals(1, h.<org.json.JSONArray>get("chats").length());
                assertEquals("Alice", h.<JSONObject>get("user").getString("nickname"));
                java.util.Map<String, String> states = ReflectionHelpers.callInstanceMethod(h.activity, "serverConnectionStates");
                assertEquals("connected", states.get(h.<ServerProfiles>get("serverProfiles").selected));
                assertNull(h.<NativeSidebar>get("sidebar").connection);
                assertEquals("sidebar", h.<String>get("panel"));
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void failedSessionChecksKeepTheAccountUntilTheServerExplicitlyRejectsTheLogin() throws Exception {
        for (int transientStatus : new int[] {503, 200}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h);
            try {
                h.<JSONObject>get("user").put("nickname", "Alice"); h.call("openSidebar");
                Client next = new Client("unused", "https://private.example.ts.net");
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(
                        new ConnectionPlan.Endpoint(ConnectionMode.TAILSCALE, "https://private.example.ts.net"), next,
                        new ConnectionRecovery.Snapshot("Starting", "Not checked", "starting", "", ""), null);
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                response(transientStatus, "{}"); health(h, "", true); h.io.run();
                assertTrue(h.<Boolean>get("restoringSession"));
                assertTrue(footerText(h.get("sidebar")).contains("Alice"));
                assertFalse(footerText(h.get("sidebar")).contains("Sign in"));
                assertEquals("alice", h.<ServerProfiles>get("serverProfiles").active().account.getString("id"));
                response(401, "{error:'Session expired'}");
                ReflectionHelpers.callInstanceMethod(h.activity, "restoreSession", ReflectionHelpers.ClassParameter.from(Client.class, next),
                        ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("generation")));
                h.io.run();
                assertFalse(h.<Boolean>get("restoringSession")); assertNull(h.get("user"));
                assertEquals(0, h.<ServerProfiles>get("serverProfiles").active().account.length());
                assertTrue(footerText(h.get("sidebar")).contains("Sign in"));
                assertFalse(footerText(h.get("sidebar")).contains("Alice"));
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void connectionPollingAndSessionRecoveryKeepTheOpenInformationPopupAndSettingsViews() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h); MainActivityWorkspaceEventsTest.install(h); workspaceResponses();
            try {
                ConnectionPlan plan = new ConnectionPlan("", "https://private.example.ts.net", ConnectionMode.TAILSCALE);
                h.<ServerProfiles>get("serverProfiles").active().plan = plan; h.set("connectionPlan", plan); h.set("connectionDraft", plan);
                h.call("openSidebar");
                Client next = new Client("unused", plan.tailscale);
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(
                        new ConnectionPlan.Endpoint(ConnectionMode.TAILSCALE, plan.tailscale), next,
                        new ConnectionRecovery.Snapshot("Starting", "Not checked", "starting", "", ""), null);
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                h.call("openSettings"); idle();
                NativeSettings dialog = h.get("settingsDialog"); NativeConnectionSettings settings = h.get("connectionSettings");
                layout(dialog.modal, 360, 800); idle();
                NativeInfo info = settings.rows.get(ConnectionMode.TAILSCALE).information;
                info.button.performClick(); idle();
                NativePopover popup = ReflectionHelpers.getField(info, "popup"); assertTrue(popup.isShowing());
                View root = h.get("root"), content = dialog.content;
                for (int i = 0; i < 3; i++) {
                    health(h, "starting", false);
                    assertSame(root, h.get("root")); assertSame(content, dialog.content);
                    assertSame(settings, h.get("connectionSettings")); assertTrue(popup.isShowing());
                }
                MainActivityGoalTest.ShadowClient.responder = null;
                response(503, "{error:'Temporary service failure'}"); health(h, "", true); h.io.run();
                assertSame(root, h.get("root")); assertSame(settings, h.get("connectionSettings")); assertTrue(popup.isShowing());
                assertFalse(((android.widget.TextView) popup.content.getChildAt(2)).getText().toString().contains("Temporary service failure"));
                assertTrue(h.<NativeConnecting>get("connectingView").message.getText().toString().contains("Temporary service failure"));
                workspaceResponses(); health(h, "", true); h.io.run();
                assertSame(content, dialog.content); assertSame(settings, h.get("connectionSettings"));
                assertTrue(popup.isShowing());
                String details = ((android.widget.TextView) popup.content.getChildAt(2)).getText().toString();
                assertTrue(details.contains("HTTPS: Verified")); assertFalse(details.contains("Spool session"));
                android.widget.Button disconnect = settings.rows.get(ConnectionMode.TAILSCALE).use;
                NativeServerTabs.Dot dot = ((android.text.Spanned) disconnect.getText()).getSpans(0, 1, NativeServerTabs.Dot.class)[0];
                assertEquals(NativeServerTabs.statusColor(h.get("ui"), "connected"), dot.color);
                h.io.jobs.clear(); h.call("closeSettings"); assertFalse(popup.isShowing());
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void networkDotsAndTransportDetailsRemainIndependentOfSpoolAuthentication() throws Exception {
        for (ConnectionMode mode : ConnectionMode.values()) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            connectionQueue(h);
            try {
                h.call("clearWorkspace");
                String address = mode == ConnectionMode.DIRECT ? "https://public.example.com" : "https://private.example.ts.net";
                ConnectionPlan plan = new ConnectionPlan(mode == ConnectionMode.DIRECT ? address : "", mode == ConnectionMode.TAILSCALE ? address : "", mode);
                ServerProfiles profiles = h.get("serverProfiles"); profiles.active().plan = plan;
                h.set("connectionPlan", plan); h.set("connectionDraft", plan); h.call("openSidebar");
                Client next = new Client("unused", address);
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(new ConnectionPlan.Endpoint(mode, address), next,
                        new ConnectionRecovery.Snapshot("Running", "Verified", "", "", ""), null);
                response(401, "{error:'Sign in required'}");
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                h.io.run();
                java.util.Map<String, String> states = ReflectionHelpers.callInstanceMethod(h.activity, "serverConnectionStates");
                assertEquals("connected", states.get(profiles.selected));
                assertTrue(footerText(h.get("sidebar")).contains("Sign in"));
                h.call("openSettings");
                NativeConnectionSettings settings = h.get("connectionSettings");
                String details = settings.details.get(mode);
                assertTrue(details.contains("HTTPS: Verified")); assertFalse(details.contains("Spool"));
                NativeServerTabs.Dot dot = ((android.text.Spanned) settings.rows.get(mode).use.getText()).getSpans(0, 1, NativeServerTabs.Dot.class)[0];
                assertEquals(NativeServerTabs.statusColor(h.get("ui"), "connected"), dot.color);
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    private String workspaceNavigation() {
        return "{projects:[{id:'project-one',name:'Project',can_create:true}],chats:[{id:'chat-one',title:'Existing chat',project_id:'project-one',backend:'codex',updated_at:1,can_control:true,participants:[]}]}";
    }

    private void workspaceResponses() {
        MainActivityWorkspaceEventsTest.ShadowStream.streams.clear();
        MainActivityGoalTest.ShadowClient.workspaceFactory = spoolnet.EventStream::new;
        MainActivityGoalTest.ShadowClient.responder = call -> {
            String path = call.get(1);
            String body = path.equals("/api/me") ? "{user:{id:'alice'},csrf:'restored-csrf'}"
                    : path.equals("/api/navigation") ? workspaceNavigation()
                    : path.equals("/api/profiles") ? "{revision:1,people:[{id:'alice',nickname:'Alice'}]}"
                    : path.equals("/api/agents") ? "[]" : "{}";
            try { return new JSONObject().put("status", 200).put("body", body).toString(); }
            catch (Exception error) { throw new AssertionError(error); }
        };
    }

    private void drainWorkspace(MainActivityGoalTest.Harness h, boolean pushFirst) {
        if (pushFirst) {
            assertFalse(h.io.jobs.isEmpty());
            h.io.jobs.remove(0).run(); idle();
            assertNotNull(MainActivityGoalTest.ShadowClient.workspaceListener);
            MainActivityGoalTest.ShadowClient.workspaceListener.onEvent("ready", "true"); idle();
            MainActivityGoalTest.ShadowClient.workspaceListener.onEvent("navigation", workspaceNavigation()); idle();
        }
        int runs = 0;
        while (!h.io.jobs.isEmpty()) {
            assertTrue("Workspace initialization must settle", ++runs < 30);
            h.io.jobs.remove(0).run(); idle();
        }
    }

    private void assertSidebarThenChooseChat(MainActivityGoalTest.Harness h) {
        assertEquals("An automatic chat selection must not close the sidebar", "sidebar", h.<String>get("panel"));
        NativeSidebar sidebar = h.get("sidebar"); assertNotNull(sidebar);
        NativeChatLink link = sidebar.chatRows.stream().filter(row -> row.open.getTag().toString().endsWith(":chat-one")).findFirst().orElseThrow();
        assertTrue(link.open.performClick());
        assertEquals("Only choosing a conversation reveals the workspace", "", h.<String>get("panel"));
        assertNull(h.get("sidebar")); assertEquals("chat-one", h.<String>get("chatID"));
    }

    @Test public void startupConnectionKeepsTheSidebarAfterRestAndWebsocketNavigationArrive() throws Exception {
        for (boolean pushFirst : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            connectionQueue(h); workspaceResponses();
            try {
                h.call("clearWorkspace"); h.call("openSidebar");
                Client next = new Client("unused", "https://restored.example.com");
                ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(
                        new ConnectionPlan.Endpoint(ConnectionMode.DIRECT, "https://restored.example.com"), next,
                        new ConnectionRecovery.Snapshot("Running", "Verified", "", "", ""),
                        new ConnectionRouter.Session("alice", "{user:{id:'alice'},csrf:'restored-csrf'}"));
                ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection", ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
                assertTrue(h.<Boolean>get("connected")); assertEquals("sidebar", h.<String>get("panel"));
                drainWorkspace(h, pushFirst);
                assertSidebarThenChooseChat(h);
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void serverSwitchKeepsTheSidebarAfterTheNewServersFirstConversationListArrives() throws Exception {
        for (boolean pushFirst : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.Queue connections = connectionQueue(h); workspaceResponses();
            try {
                ServerProfiles profiles = h.get("serverProfiles");
                ServerProfiles.Server second = profiles.add("Office");
                second.plan = new ConnectionPlan("https://office.example.com", "", ConnectionMode.DIRECT);
                select(h, second.id); drain(connections);
                assertEquals(second.id, profiles.selected); assertTrue(h.<Boolean>get("connected"));
                drainWorkspace(h, pushFirst);
                assertSidebarThenChooseChat(h);
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void deletingAServerStopsOnlyItsConnectionAndLastDeletionKeepsTheAddEntry() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.Queue connections = connectionQueue(h);
            h.call("disconnect"); drain(connections);
            try {
                response(200, "{user:{id:'alice'},csrf:'csrf'}");
                NativeConnectionSettings settings = h.get("connectionSettings");
                settings.input(ConnectionMode.DIRECT).setText("https://first.example.com");
                settings.rows.get(ConnectionMode.DIRECT).use.performClick(); drain(connections); h.io.jobs.clear();
                ServerProfiles profiles = h.get("serverProfiles");
                String firstID = profiles.selected;
                ServerProfiles.Server second = profiles.add("Office");
                second.plan = new ConnectionPlan("https://second.example.com", "", ConnectionMode.DIRECT);
                h.call("refreshServerConnections"); drain(connections);
                java.util.Map<String, ?> runtimes = h.get("serverRuntimes");
                Object secondRuntime = runtimes.get(second.id);
                Client secondClient = ((ConnectionRouter.Candidate<Client>) ReflectionHelpers.getField(secondRuntime, "candidate")).client;
                java.io.File firstState = new java.io.File(profiles.active().directory(h.activity.getNoBackupFilesDir(), ConnectionMode.DIRECT));
                java.io.File secondState = new java.io.File(second.directory(h.activity.getNoBackupFilesDir(), ConnectionMode.DIRECT));
                assertTrue(firstState.mkdirs() || firstState.isDirectory());
                assertTrue(secondState.mkdirs() || secondState.isDirectory());
                java.nio.file.Files.writeString(new java.io.File(firstState, "owned-session").toPath(), "fixture");
                java.nio.file.Files.writeString(new java.io.File(secondState, "owned-session").toPath(), "fixture");
                h.call("openSidebar"); h.call("openSettings");
                NativeSettings dialog = h.get("settingsDialog");
                View last = dialog.content.additions.getChildAt(dialog.content.additions.getChildCount() - 1);
                assertTrue(last instanceof Button); assertEquals("Delete server", ((Button) last).getText().toString());
                last.performClick(); drain(connections); h.io.jobs.clear();
                assertEquals(second.id, profiles.selected); assertEquals(1, profiles.entries.size());
                assertSame(secondClient, h.get("client")); assertTrue(second.enabled);
                assertFalse(runtimes.containsKey(firstID)); assertFalse(firstState.exists()); assertTrue(secondState.exists());
                assertEquals("sidebar", h.<String>get("panel")); assertNotNull(h.get("sidebar"));
                h.call("deleteServer"); drain(connections);
                assertTrue(profiles.entries.isEmpty()); assertNull(profiles.active()); assertNull(h.get("client"));
                assertFalse(secondState.exists()); assertNull(h.get("settingsDialog"));
                assertEquals("[]", h.activity.getPreferences(Activity.MODE_PRIVATE).getString("servers", ""));
                NativeSidebar sidebar = h.get("sidebar"); assertNotNull(sidebar);
                NativeServerTabs tabs = (NativeServerTabs) sidebar.getChildAt(1);
                assertEquals(1, tabs.tabs.getChildCount()); assertSame(tabs.add, tabs.tabs.getChildAt(0));
                tabs.add.performClick();
                NativeServerDialog add = h.get("serverDialog");
                add.name.setText("New server"); add.save.performClick();
                assertEquals(1, profiles.entries.size()); assertEquals("New server", profiles.active().name);
                assertTrue(profiles.active().plan.direct.isEmpty()); assertTrue(h.<NativeSettings>get("settingsDialog").isShowing());
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void bothNetworksShareTheServersSessionWhileOnlyTheSelectedNetworkIsLive() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.Queue connections = connectionQueue(h);
            ShadowSpoolnet.failDirect = false;
            try {
                h.call("disconnect"); drain(connections);
                response(200, "{user:{id:'alice'},csrf:'direct-csrf'}");
                NativeConnectionSettings settings = h.get("connectionSettings");
                settings.input(ConnectionMode.DIRECT).setText("https://chat.example.com:8443/");
                settings.input(ConnectionMode.TAILSCALE).setText("https://spool.example.ts.net");
                settings.rows.get(ConnectionMode.DIRECT).use.performClick(); drain(connections);
                assertEquals("direct", ShadowSpoolnet.mode);
                assertEquals("https://chat.example.com:8443", h.<String>get("origin"));
                assertEquals("direct", h.activity.getPreferences(Activity.MODE_PRIVATE).getString("connectionMode", ""));
                ServerProfiles profiles = h.get("serverProfiles");
                assertEquals("https://spool.example.ts.net", profiles.active().plan.tailscale);
                assertTrue(profiles.active().enabled);
                assertTrue(h.<Boolean>get("connected"));
                MainActivityGoalTest.ShadowClient publicClient = org.robolectric.shadow.api.Shadow.extract(h.<Client>get("client"));
                assertEquals(profiles.active().sessionDirectory(h.activity.getNoBackupFilesDir()), publicClient.sessionDirectory);
                assertEquals("https://chat.example.com:8443", publicClient.directOrigin);
                assertEquals("https://spool.example.ts.net", publicClient.tailscaleOrigin);
                h.io.jobs.clear();
                settings = h.get("connectionSettings"); settings.rows.get(ConnectionMode.TAILSCALE).use.performClick();
                drain(connections);
                assertEquals("tailscale", ShadowSpoolnet.mode);
                assertSame(ConnectionMode.TAILSCALE, profiles.active().plan.selected);
                assertTrue(ShadowSpoolnet.directory.endsWith("/tsnet"));
                MainActivityGoalTest.ShadowClient privateClient = org.robolectric.shadow.api.Shadow.extract(h.<Client>get("client"));
                assertEquals(publicClient.sessionDirectory, privateClient.sessionDirectory);
                assertEquals("alice", h.<JSONObject>get("user").getString("id"));
                assertEquals("direct-csrf", h.<String>get("csrf"));
                assertFalse(h.<Boolean>get("routeLoginRequired"));
                assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().allMatch(call -> call.get(0).equals("GET")));
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void unavailableDirectStartupDoesNotTryAnotherNetworkWhenAutomaticSwitchingIsOff() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.Queue connections = connectionQueue(h);
            h.call("disconnect"); drain(connections); ShadowSpoolnet.failDirect = true;
            try {
                NativeConnectionSettings settings = h.get("connectionSettings");
                settings.input(ConnectionMode.DIRECT).setText("https://chat.example.com");
                settings.input(ConnectionMode.TAILSCALE).setText("https://spool.example.ts.net");
                if (settings.automatic.isChecked()) settings.automatic.performClick();
                settings.rows.get(ConnectionMode.DIRECT).use.performClick(); drain(connections);
                assertEquals("direct", ShadowSpoolnet.mode); assertNull(h.get("client"));
                assertFalse(h.<Boolean>get("routeSelecting"));
            } finally {
                ShadowSpoolnet.failDirect = false;
                h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
            }
        }
    }

    @Test public void selectedTailscaleKeepsItsLoginFlowInsteadOfReturningToPublicHTTPS() throws Exception {
        for (String problem : new String[] {"login", "approval", "starting"})
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
                MainActivityGoalTest.Queue connections = connectionQueue(h);
                h.call("disconnect"); drain(connections);
                ShadowSpoolnet.failDirect = false;
                try {
                    response(200, "{user:{id:'alice'},csrf:'direct-csrf'}");
                    NativeConnectionSettings settings = h.get("connectionSettings");
                    settings.input(ConnectionMode.DIRECT).setText("https://chat.example.com");
                    settings.input(ConnectionMode.TAILSCALE).setText("https://spool.example.ts.net");
                    settings.automatic.setChecked(true);
                    settings.rows.get(ConnectionMode.DIRECT).use.performClick(); drain(connections); h.io.jobs.clear();
                    assertSame(ConnectionMode.DIRECT, h.get("connectionMode"));
                    ShadowSpoolnet.tailnetHealth = new JSONObject().put("state", problem.equals("login") ? "NeedsLogin" : problem.equals("approval") ? "NeedsMachineAuth" : "Starting")
                            .put("https", "Not checked").put("problem", problem).put("authURL", "https://login.tailscale.com/a/test").toString();
                    settings = h.get("connectionSettings");
                    settings.rows.get(ConnectionMode.TAILSCALE).use.performClick(); drain(connections);
                    assertSame("The selected network stays active while its node is not ready", ConnectionMode.TAILSCALE, h.get("connectionMode"));
                    assertNotNull(h.get("client"));
                    assertEquals("https://spool.example.ts.net", h.<String>get("origin"));
                    assertSame("The clicked entry becomes this server's saved choice", ConnectionMode.TAILSCALE, h.<ServerProfiles>get("serverProfiles").active().plan.selected);
                    ServerProfiles restored = ServerProfiles.restore(h.activity.getPreferences(Activity.MODE_PRIVATE).getString("servers", null),
                            h.<ServerProfiles>get("serverProfiles").selected, new ConnectionPlan("", "", ConnectionMode.DIRECT));
                    assertSame(ConnectionMode.TAILSCALE, restored.active().plan.selected);
                    assertEquals("tailscale", ShadowSpoolnet.mode);
                    for (int i = 0; i < 3; i++) {
                        health(h, problem, false); drain(connections);
                        assertSame(ConnectionMode.TAILSCALE, h.get("connectionMode"));
                        assertEquals("tailscale", ShadowSpoolnet.mode);
                    }
                } finally {
                    ShadowSpoolnet.tailnetHealth = null;
                    h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
                }
            }
    }

    @Test public void enabledServersStayConnectedAcrossTabSelectionAndKeepWorkspaceDraftsIsolated() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.Queue connections = connectionQueue(h);
            h.call("disconnect"); drain(connections);
            ShadowSpoolnet.failDirect = false;
            try {
                response(200, "{user:{id:'alice'},csrf:'csrf'}");
                NativeConnectionSettings settings = h.get("connectionSettings");
                settings.input(ConnectionMode.DIRECT).setText("https://first.example.com"); settings.rows.get(ConnectionMode.DIRECT).use.performClick(); drain(connections);
                h.io.jobs.clear();
                h.composer().input.setText("First server draft");
                Client first = h.get("client");
                ServerProfiles profiles = h.get("serverProfiles"); String firstID = profiles.selected;
                ServerProfiles.Server second = profiles.add("Second"); second.plan = new ConnectionPlan("https://second.example.com", "", ConnectionMode.DIRECT, true);
                h.call("refreshServerConnections"); drain(connections);
                java.util.Map<?, ?> runtimes = h.get("serverRuntimes");
                assertEquals(2, runtimes.size());
                Object firstRuntime = runtimes.get(firstID);
                assertNotNull(ReflectionHelpers.getField(firstRuntime, "candidate"));
                h.call("openDesktop");
                NativeDesktopDialog desktop = h.get("desktopDialog");
                assertNotNull(desktop); assertTrue(desktop.isShowing());
                select(h, second.id); drain(connections); h.io.jobs.clear();
                assertEquals("sidebar", h.<String>get("panel")); assertNotNull(h.get("sidebar"));
                assertFalse(desktop.isShowing()); assertNull(h.get("desktopDialog"));
                assertEquals(second.id, profiles.selected); assertNotSame(first, h.get("client"));
                MainActivityGoalTest.ShadowClient firstClient = org.robolectric.shadow.api.Shadow.extract(first);
                MainActivityGoalTest.ShadowClient secondClient = org.robolectric.shadow.api.Shadow.extract(h.<Client>get("client"));
                assertNotEquals(firstClient.sessionDirectory, secondClient.sessionDirectory);
                assertFalse(h.composer().input.getText().toString().contains("First server draft"));
                assertTrue(profiles.entries.stream().allMatch(server -> server.enabled));
                h.composer().input.setText("Second server draft");
                select(h, firstID); drain(connections); h.io.jobs.clear();
                assertSame(first, h.get("client"));
                h.call("closeDrawer");
                assertEquals("First server draft", h.composer().input.getText().toString());
                assertEquals(firstID, profiles.selected);
                ReflectionHelpers.callInstanceMethod(h.activity, "enableServer", ReflectionHelpers.ClassParameter.from(String.class, second.id), ReflectionHelpers.ClassParameter.from(boolean.class, false));
                drain(connections);
                assertFalse(second.enabled); assertSame(first, h.get("client")); assertTrue(h.<Boolean>get("connected"));
            } finally { h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit(); }
        }
    }

    @Test public void networkHandoffPreservesTheSelectedChatAndDraftButClosesTheOldDesktop() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            connectionQueue(h);
            h.set("connected", true); h.composer().input.setText("Draft preserved across networks");
            h.call("openDesktop");
            NativeDesktopDialog desktop = h.get("desktopDialog");
            Client next = new Client("unused", "https://tail.example.ts.net");
            ConnectionRouter.Candidate<Client> candidate = new ConnectionRouter.Candidate<>(
                    new ConnectionPlan.Endpoint(ConnectionMode.TAILSCALE, "https://tail.example.ts.net"), next,
                    new ConnectionRecovery.Snapshot("Running", "Verified", "", "", ""),
                    new ConnectionRouter.Session("alice", "{user:{id:'alice'},csrf:'tail-csrf'}"));
            ReflectionHelpers.callInstanceMethod(h.activity, "adoptConnection",
                    ReflectionHelpers.ClassParameter.from(ConnectionRouter.Candidate.class, candidate));
            assertSame(next, h.get("client")); assertEquals("chat-one", h.<String>get("chatID"));
            assertEquals("Draft preserved across networks", h.composer().input.getText().toString());
            assertEquals("tail-csrf", h.<String>get("csrf")); assertFalse(desktop.isShowing()); assertNull(h.get("desktopDialog"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(0).equals("POST")));
        }
    }

    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    private void response(int status, String body) throws Exception {
        MainActivityGoalTest.ShadowClient.response = new JSONObject().put("status", status).put("body", body).toString();
    }

    private void restore(MainActivityGoalTest.Harness h) {
        ReflectionHelpers.callInstanceMethod(h.activity, "restoreSession",
                ReflectionHelpers.ClassParameter.from(Client.class, h.get("client")),
                ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("generation")));
        h.io.jobs.remove(0).run();
        idle();
    }

    private void waiting(MainActivityGoalTest.Harness h) {
        h.call("clearWorkspace");
        h.set("origin", "https://fixture.invalid");
        h.set("connected", true);
        h.set("restoringSession", true);
        h.call("render");
        assertTrue(h.<ViewGroup>get("root").getChildAt(0) instanceof NativeConnecting);
        assertNull(h.get("username"));
        assertNull(h.get("password"));
    }

    private void health(MainActivityGoalTest.Harness h, String problem, boolean available) {
        ConnectionRecovery.Snapshot value = new ConnectionRecovery.Snapshot("Running",
                problem.isEmpty() ? "Verified" : "Failed", problem, "", problem.isEmpty() ? "" : "Fixture failure");
        ReflectionHelpers.callInstanceMethod(h.activity, "acceptConnectionHealth",
                ReflectionHelpers.ClassParameter.from(Client.class, h.get("client")),
                ReflectionHelpers.ClassParameter.from(int.class, h.<Integer>get("generation")),
                ReflectionHelpers.ClassParameter.from(ConnectionRecovery.Snapshot.class, value),
                ReflectionHelpers.ClassParameter.from(boolean.class, available));
    }

    @Test public void healthLossAndRecoveryRetainTheActualWorkspaceDraftAndSessionWithoutReplayingWrites() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("connected", true);
            h.composer().input.setText("Draft waiting for network");
            NativeUi.Composer composer = h.composer();
            View header = h.get("workspaceHeader");
            JSONObject user = h.get("user"), chat = h.get("chat");
            Client client = h.get("client");
            int generation = h.get("generation"), session = h.get("authEpoch");
            health(h, "network", true); assertTrue(h.<Boolean>get("connected"));
            health(h, "network", false); assertFalse(h.<Boolean>get("connected"));
            assertTrue(h.<Boolean>get("connecting")); assertEquals("Failed", h.<String>get("httpsState"));
            assertSame(user, h.get("user")); assertSame(chat, h.get("chat"));
            assertSame(header, h.get("workspaceHeader")); assertSame(composer, h.composer());
            assertEquals("Draft waiting for network", h.composer().input.getText().toString());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertTrue(h.io.jobs.isEmpty());
            health(h, "", true);
            assertTrue(h.<Boolean>get("connected")); assertFalse(h.<Boolean>get("connecting"));
            assertEquals("Verified", h.<String>get("httpsState")); assertEquals("", h.<String>get("connectionMessage"));
            assertSame(client, h.get("client")); assertSame(user, h.get("user")); assertSame(chat, h.get("chat"));
            assertEquals(generation, h.<Integer>get("generation").intValue());
            assertEquals(session, h.<Integer>get("authEpoch").intValue());
            assertEquals("chat-one", h.<String>get("chatID")); assertEquals("test-csrf", h.<String>get("csrf"));
            assertSame(composer, h.composer()); assertEquals("Draft waiting for network", composer.input.getText().toString());
            assertEquals(1, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(0).equals("POST")));
            int reads = h.io.jobs.size(), streams = MainActivityGoalTest.ShadowClient.chatSubscriptions.size();
            health(h, "", true); // Healthy sampling must not repeatedly reopen streams or reload data.
            assertEquals(reads, h.io.jobs.size()); assertEquals(streams, MainActivityGoalTest.ShadowClient.chatSubscriptions.size());
        }
    }

    @Test public void repeatedHealthySamplesSerializeSessionRestoreAndRetryOnlyTheRead() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            waiting(h);
            health(h, "", true); health(h, "", true);
            assertEquals(1, h.io.jobs.size());
            response(503, "{error:'Server unavailable'}"); h.io.run();
            assertTrue(h.<Boolean>get("restoringSession")); assertFalse(h.<Boolean>get("sessionCheckBusy"));
            health(h, "", true); health(h, "", true);
            assertEquals(1, h.io.jobs.size());
            response(401, "{error:'Sign in required'}"); h.io.run();
            assertFalse(h.<Boolean>get("restoringSession")); assertNotNull(h.get("username"));
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
            for (java.util.List<String> call : MainActivityGoalTest.ShadowClient.calls) {
                assertEquals("GET", call.get(0)); assertEquals("/api/me", call.get(1));
            }
        }
    }

    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    @Test public void savedServerStartsWithConnectingBeforeAnyTransportWorkAndFreshInstallShowsLogin() {
        for (String[] selection : new String[][] {{"https://fixture.invalid", ""}, {"https://chat.example.com", "direct"}, {"", ""}}) {
            String origin = selection[0];
            ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class);
            MainActivity activity = controller.get();
            activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().putString("serverOrigin", origin)
                    .putString("connectionMode", selection[1]).commit();
            MainActivityGoalTest.Queue connections = new MainActivityGoalTest.Queue();
            ExecutorService original = ReflectionHelpers.getField(activity, "connectionWorker");
            original.shutdown();
            ReflectionHelpers.setField(activity, "connectionWorker", connections);
            try {
                controller.create().start().resume().visible();
                assertSame(selection[1].equals("direct") || origin.isEmpty() ? ConnectionMode.DIRECT : ConnectionMode.TAILSCALE,
                        ReflectionHelpers.getField(activity, "connectionMode"));
                ViewGroup root = ReflectionHelpers.getField(activity, "root");
                if (origin.isEmpty()) assertTrue(root.getChildAt(0) instanceof NativeLogin);
                else {
                    assertTrue(root.getChildAt(0) instanceof NativeConnecting);
                    assertNull(ReflectionHelpers.getField(activity, "username"));
                    assertFalse(connections.jobs.isEmpty());
                }
            } finally {
                activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
                controller.pause().stop().destroy();
                connections.jobs.clear();
            }
        }
    }

    @Test public void restartingUsesTheServersSavedNetworkEvenWhenTheLastActiveNetworkWasDifferent() throws Exception {
        for (ConnectionMode chosen : ConnectionMode.values()) {
            ActivityController<MainActivity> controller = Robolectric.buildActivity(MainActivity.class);
            MainActivity activity = controller.get();
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("https://chat.example.com", "https://spool.example.ts.net", chosen, true));
            profiles.active().account = new JSONObject("{id:'alice',nickname:'Alice'}");
            ConnectionMode other = chosen == ConnectionMode.DIRECT ? ConnectionMode.TAILSCALE : ConnectionMode.DIRECT;
            activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().putString("servers", profiles.encode())
                    .putString("selectedServer", profiles.selected).putString("serverOrigin", profiles.active().plan.origin(other))
                    .putString("connectionMode", other.value).commit();
            MainActivityGoalTest.Queue connections = new MainActivityGoalTest.Queue();
            ExecutorService original = ReflectionHelpers.getField(activity, "connectionWorker"); original.shutdown();
            ReflectionHelpers.setField(activity, "connectionWorker", connections);
            ReflectionHelpers.setField(activity, "avatarImages", new NativeAvatars(task -> {}, (style, seed) -> { throw new AssertionError("No native avatar generation in startup request tests"); }));
            try {
                controller.create().start().resume().visible();
                assertSame(chosen, ReflectionHelpers.getField(activity, "connectionMode"));
                assertEquals(profiles.active().plan.origin(chosen), ReflectionHelpers.getField(activity, "origin"));
                assertFalse(connections.jobs.isEmpty());
                NativeSidebar sidebar = ReflectionHelpers.getField(activity, "sidebar");
                assertTrue(footerText(sidebar).contains("Alice")); assertFalse(footerText(sidebar).contains("Sign in"));
                assertEquals("Connecting…", sidebar.connection.getContentDescription());
                assertNull(ReflectionHelpers.getField(activity, "user"));
            } finally {
                activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
                controller.pause().stop().destroy(); connections.jobs.clear();
            }
        }
    }

    @Test public void restoredSessionKeepsLocalPreferencesAndTheSettingsWindow() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            org.robolectric.RuntimeEnvironment.setQualifiers("w360dp-h400dp-mdpi");
            h.set("theme", "light"); h.set("language", "en");
            waiting(h);
            h.call("openSettings");
            idle();
            NativeSettings dialog = h.get("settingsDialog");
            View decor = dialog.getWindow().getDecorView();
            AtomicInteger dismissed = new AtomicInteger(), detached = new AtomicInteger();
            dialog.setOnDismissListener(value -> dismissed.incrementAndGet());
            decor.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
                public void onViewAttachedToWindow(View view) {}
                public void onViewDetachedFromWindow(View view) { detached.incrementAndGet(); }
            });
            layout(dialog.modal, 360, 400);
            ScrollView scroll = ReflectionHelpers.getField(dialog, "scroll");
            scroll.scrollTo(0, 40);
            assertEquals(40, scroll.getScrollY());
            response(200, "{user:{id:'alice'},csrf:'restored-csrf',preferences:{theme:'dark',language:'ja'}}");
            restore(h);
            layout(dialog.modal, 360, 400);
            dialog.modal.getViewTreeObserver().dispatchOnPreDraw();
            assertSame(dialog, h.get("settingsDialog"));
            assertSame(decor, dialog.getWindow().getDecorView());
            assertTrue(dialog.isShowing());
            assertEquals(0, dismissed.get());
            assertEquals(0, detached.get());
            ScrollView restored = ReflectionHelpers.getField(dialog, "scroll");
            int maximum = restored.getChildAt(0).getHeight() - restored.getHeight();
            assertTrue(maximum > 0);
            assertEquals(Math.min(40, maximum), restored.getScrollY());
            assertFalse(h.<Boolean>get("restoringSession"));
            assertNotNull(h.get("workspaceHeader"));
            assertNull(h.get("username"));
            assertEquals("alice", h.<JSONObject>get("user").getString("id"));
            assertEquals("restored-csrf", h.<String>get("csrf"));
            assertFalse(h.<NativeUi>get("ui").dark);
            assertEquals("en", h.<NativeUi>get("ui").locale);
            dialog.content.close.performClick();
            idle();
            assertFalse(dialog.isShowing());
            assertNull(h.get("settingsDialog"));
            assertFalse(h.<Boolean>get("settingsOpen"));
        }
    }

    @Test public void onlyAnUnauthorizedSessionRevealsLoginAndKeepsAnOpenSettingsWindow() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            waiting(h);
            h.call("openSettings");
            NativeSettings dialog = h.get("settingsDialog");
            response(401, "{error:'Session expired'}");
            restore(h);
            assertFalse(h.<Boolean>get("restoringSession"));
            assertTrue(h.<ViewGroup>get("root").getChildAt(0) instanceof NativeLogin);
            assertNotNull(h.get("username"));
            assertSame(dialog, h.get("settingsDialog"));
            assertTrue(dialog.isShowing());
        }
    }

    @Test public void failedSessionCheckKeepsLoginHiddenAndRetryCanRestoreTheSession() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            waiting(h);
            response(503, "{error:'Server unavailable'}");
            restore(h);
            NativeConnecting page = (NativeConnecting) h.<ViewGroup>get("root").getChildAt(0);
            assertNull(h.get("username"));
            Button retry = findButton(page, "Retry");
            assertNotNull(retry);
            response(200, "{user:{id:'alice'},csrf:'restored-csrf'}");
            retry.performClick();
            assertTrue(h.<ViewGroup>get("root").getChildAt(0) instanceof NativeConnecting);
            h.io.jobs.remove(0).run();
            idle();
            assertNotNull(h.get("workspaceHeader"));
            assertEquals(2, MainActivityGoalTest.ShadowClient.calls.size());
            for (java.util.List<String> call : MainActivityGoalTest.ShadowClient.calls) {
                assertEquals("GET", call.get(0));
                assertEquals("/api/me", call.get(1));
            }
        }
    }

    private Button findButton(View view, String text) {
        if (view instanceof Button && text.contentEquals(((Button) view).getText())) return (Button) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            Button found = findButton(((ViewGroup) view).getChildAt(i), text);
            if (found != null) return found;
        }
        return null;
    }
}
