package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.text.Spanned;
import android.widget.Button;
import java.util.List;
import java.util.Map;
import org.json.JSONArray;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

/** Servers offered by the signed-in source appear in the server tabs, marked apart from ordinary ones. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
public class NativeFederationTest {
    private static final String B = "b".repeat(52), C = "c".repeat(52), D = "d".repeat(52);

    private static ServerProfiles profiles() {
        ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("https://home.example.com", "", ConnectionMode.DIRECT));
        ServerProfiles.Server office = profiles.add("Office");
        office.plan = new ConnectionPlan("https://office.example.com:8443", "", ConnectionMode.DIRECT);
        return profiles;
    }

    @Test public void targetsKeepOnlyValidIdentitiesAndPlainHttpsOrigins() throws Exception {
        List<NativeFederation.Target> targets = NativeFederation.parse(new JSONArray("[{id:'" + B + "',name:' Office ',client_url:'https://Office.example.com:8443/'},"
                + "{id:'" + C + "',name:'Relay web',client_url:'https://relay.example.com/s/abc/'},{id:'" + B + "',name:'Duplicate'},"
                + "{id:'short',name:'Invalid'},{id:'" + D + "',name:''}]"));
        assertEquals(3, targets.size());
        assertEquals("Office", targets.get(0).name); assertEquals("https://office.example.com:8443", targets.get(0).origin);
        assertEquals("A URL with a path is not a native HTTPS origin", "", targets.get(1).origin);
        assertEquals(D.substring(0, 8), targets.get(2).name);
        assertTrue(NativeFederation.parse(null).isEmpty());
    }

    @Test public void modeSetsOnlyTheNewProfilesTransport() throws Exception {
        String address = "https://office.example.com:8443";
        List<NativeFederation.Target> targets = NativeFederation.parse(new JSONArray("[{id:'" + B + "',client_url:'" + address
                + "',client_mode:'tailscale'},{id:'" + C + "',client_url:'" + address + "',client_mode:''},"
                + "{id:'" + D + "',client_url:'" + address + "'}]"));
        ConnectionPlan tailnet = targets.get(0).newPlan();
        assertEquals(ConnectionMode.TAILSCALE, tailnet.selected);
        assertEquals(address, tailnet.tailscale);
        assertEquals("", tailnet.direct);
        for (NativeFederation.Target target : targets.subList(1, 3)) {
            assertEquals(ConnectionMode.DIRECT, target.newPlan().selected);
            assertEquals(address, target.newPlan().direct);
        }
        assertEquals(ConnectionMode.DIRECT, NativeFederation.route(profiles().entries.get(1), targets.get(0), ConnectionMode.DIRECT));
        assertFalse(NativeFederation.allows(targets.get(0), ConnectionMode.TAILSCALE, "https://office.example.com"));
        JSONArray changed = new JSONArray("[{id:'" + B + "',client_url:'" + address + "'}]");
        assertFalse(NativeFederation.authorizedRoute(changed, B, ConnectionMode.TAILSCALE, address, true));
        assertTrue(NativeFederation.authorizedRoute(changed, B, ConnectionMode.TAILSCALE, address, false));
        assertTrue(NativeFederation.authorizedRoute(changed, B, ConnectionMode.DIRECT, address, true));
    }

    @Test public void savedConnectionsMatchOnlyTheAuthorizedOriginOrPinnedRelayIdentity() throws Exception {
        ServerProfiles profiles = profiles();
        ServerProfiles.Server office = profiles.entries.get(1);
        NativeFederation.Target listed = new NativeFederation.Target(B, "Office", "https://office.example.com:8443");
        assertEquals(ConnectionMode.DIRECT, NativeFederation.route(office, listed, null));
        assertNull("The default port is a different origin", NativeFederation.route(office, new NativeFederation.Target(B, "Office", "https://office.example.com"), null));
        assertNull("Without an authorized address an HTTPS profile is never matched", NativeFederation.route(office, new NativeFederation.Target(B, "Office", ""), null));
        String code = RelayPairingTest.code("https://relay.example.com");
        ServerProfiles.Server paired = profiles.add("Paired");
        paired.plan = new ConnectionPlan("", "", code, ConnectionMode.RELAY, true);
        assertEquals(ConnectionMode.RELAY, NativeFederation.route(paired, new NativeFederation.Target(new RelayPairing(code).id, "Paired", ""), null));
        assertNull(NativeFederation.route(paired, new NativeFederation.Target(B, "Other", ""), null));
    }

    @Test public void everyAuthorizedTargetIsListedEvenWithoutARoute() {
        ServerProfiles profiles = profiles();
        List<NativeFederation.Tab> tabs = NativeFederation.tabs(profiles, profiles.entries.get(0), List.of(
                new NativeFederation.Target(B, "Office", "https://office.example.com:8443"),
                new NativeFederation.Target(C, "Lab", "https://lab.example.com"),
                new NativeFederation.Target(D, "Relay only", "")));
        assertEquals(3, tabs.size());
        assertNull(tabs.get(0).server);
        assertSame(profiles.entries.get(1), tabs.get(0).route);
        assertEquals(NativeFederation.tabKey("legacy", B), tabs.get(0).key());
        assertNull(tabs.get(1).server); assertTrue(tabs.get(1).reachable());
        assertEquals(NativeFederation.tabKey("legacy", C), tabs.get(1).key());
        assertFalse(tabs.get(2).reachable());
        assertSame(tabs.get(2), NativeFederation.find(tabs, NativeFederation.tabKey("legacy", D)));
    }

    @Test public void trustedTabsCarryALinkMarkAndDescription() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server session = profiles.trustedProfile(profiles.entries.get(1), profiles.entries.get(0), B, "a".repeat(43));
            session.enabled = true;
            String savedKey = NativeFederation.tabKey("legacy", B), pendingKey = NativeFederation.tabKey("legacy", C);
            List<NativeFederation.Tab> trusted = NativeFederation.tabs(profiles, profiles.entries.get(0), List.of(
                    new NativeFederation.Target(B, "Office", "https://office.example.com:8443"), new NativeFederation.Target(C, "Lab", "")));
            java.util.List<String> chosen = new java.util.ArrayList<>();
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), profiles, Map.of(), chosen::add, true, () -> {}, trusted);
            Button home = tabs.button("legacy"), saved = tabs.button(savedKey), pending = tabs.button(pendingKey);
            assertEquals(0, links(home)); assertEquals(1, links(saved)); assertEquals(1, links(pending));
            assertEquals(0, ((Spanned) saved.getText()).getSpans(0, 1, NativeServerTabs.Dot.class).length);
            assertEquals("An unsaved target has no connection state to show", 0, ((Spanned) pending.getText()).getSpans(0, pending.length(), NativeServerTabs.Dot.class).length);
            assertEquals("Spool → Office, Disconnected, Trusted server", saved.getContentDescription().toString());
            assertEquals("Spool → Lab, Trusted server", pending.getContentDescription().toString());
            assertEquals("Spool, Disconnected", home.getContentDescription().toString());
            pending.performClick(); saved.performClick();
            assertEquals(List.of(pendingKey, savedKey), chosen);
        }
    }

    @Test public void trustedAliasesKeepBothSourceGroupsAndSelectOnlyTheClickedAlias() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server home = profiles.entries.get(0), office = profiles.entries.get(1);
            ServerProfiles.Server ordinary = profiles.add("Ordinary"), source = profiles.add("Source");
            NativeFederation.Target work = new NativeFederation.Target(B, "Office", "https://office.example.com:8443");
            List<NativeFederation.Tab> groups = NativeFederation.tabs(profiles, home, List.of(work));
            groups.addAll(NativeFederation.tabs(profiles, source, List.of(new NativeFederation.Target(C, "Lab", ""), work)));
            profiles.selected = ordinary.id;
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeServerTabs tabs = new NativeServerTabs(ui, profiles, Map.of(), id -> {}, true, () -> {}, groups);
            String first = NativeFederation.tabKey(home.id, B), second = NativeFederation.tabKey(source.id, B);
            List<String> order = List.of("legacy", first, office.id, ordinary.id, source.id, NativeFederation.tabKey(source.id, C), second);
            assertEquals(order, tabOrder(tabs));
            assertTrue(tabs.button(ordinary.id).isSelected());
            profiles.selected = office.id;
            tabs.status(Map.of(), second);
            assertEquals(order, tabOrder(tabs));
            assertFalse(tabs.button(first).isSelected());
            assertFalse(tabs.button(second).isSelected());
            assertTrue(tabs.button(office.id).isSelected());
        }
    }

    private static List<String> tabOrder(NativeServerTabs tabs) {
        java.util.ArrayList<String> order = new java.util.ArrayList<>();
        for (int i = 0; i < tabs.tabs.getChildCount(); i++) {
            android.view.View child = tabs.tabs.getChildAt(i);
            if (child instanceof Button) order.add((String) child.getTag());
        }
        return order;
    }

    @Test public void cachedAccountOriginDoesNotMarkAnOrdinaryProfileAsATrustedDestination() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server office = profiles.entries.get(1);
            office.account = ServerProfiles.displayAccount(new org.json.JSONObject("{id:'remote',nickname:'Alice',source_server_id:'" + B + "'}"));
            ServerProfiles restored = ServerProfiles.restore(profiles.encode(), office.id, profiles.entries.get(0).plan);
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), restored, Map.of(), id -> {}, true, () -> {});
            Button ordinary = tabs.button(office.id);
            assertEquals(0, links(ordinary)); assertEquals(0, links(tabs.button("legacy")));
            assertEquals(1, ((Spanned) ordinary.getText()).getSpans(0, 1, NativeServerTabs.Dot.class).length);
            assertFalse(ordinary.getContentDescription().toString().contains("Trusted server"));
        }
    }

    private static int links(Button tab) {
        return ((Spanned) tab.getText()).getSpans(0, tab.length(), NativeServerTabs.Link.class).length;
    }

    @Test public void ordinaryAndBothSourceLoginsRemainVisibleInLightAndDarkThemes() throws Exception {
        org.robolectric.RuntimeEnvironment.setQualifiers("w960dp-h120dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server home = profiles.entries.get(0), office = profiles.entries.get(1), studio = profiles.add("Studio");
            home.name = "Home";
            NativeFederation.Target target = new NativeFederation.Target(B, "Office", office.plan.direct);
            ServerProfiles.Server a = profiles.trustedProfile(office, home, B, "a".repeat(43));
            ServerProfiles.Server d = profiles.trustedProfile(office, studio, B, "d".repeat(43));
            a.enabled = d.enabled = true; profiles.selected = d.id;
            List<NativeFederation.Tab> groups = NativeFederation.tabs(profiles, home, List.of(target));
            groups.addAll(NativeFederation.tabs(profiles, studio, List.of(target)));
            for (boolean dark : new boolean[] {false, true}) {
                Map<String, String> states = Map.of(home.id, "connected", office.id, "connected", studio.id, "connected", a.id, "connected", d.id, "connected");
                NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), dark, "zh-CN"), profiles,
                        states,
                        id -> {}, true, () -> {}, groups);
                tabs.status(states, NativeFederation.tabKey(studio.id, B));
                controller.get().setContentView(tabs);
                tabs.measure(android.view.View.MeasureSpec.makeMeasureSpec(960, android.view.View.MeasureSpec.EXACTLY),
                        android.view.View.MeasureSpec.makeMeasureSpec(120, android.view.View.MeasureSpec.AT_MOST));
                tabs.layout(0, 0, 960, tabs.getMeasuredHeight());
                assertEquals(List.of(home.id, NativeFederation.tabKey(home.id, B), office.id, studio.id, NativeFederation.tabKey(studio.id, B)), tabOrder(tabs));
                assertTrue(tabs.button(NativeFederation.tabKey(studio.id, B)).isSelected()); assertFalse(tabs.button(office.id).isSelected());
                if (android.os.Build.VERSION.SDK_INT == 35) {
                    java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"), "server-trust");
                    assertTrue(directory.isDirectory() || directory.mkdirs());
                    android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(960, tabs.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                    tabs.draw(new android.graphics.Canvas(image));
                    try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, "independent-logins-" + (dark ? "dark" : "light") + ".png"))) {
                        image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output);
                    }
                    image.recycle();
                }
            }
        }
    }
}
