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
        List<NativeFederation.Tab> tabs = NativeFederation.tabs(profiles, List.of(
                new NativeFederation.Target(B, "Office", "https://office.example.com:8443"),
                new NativeFederation.Target(C, "Lab", "https://lab.example.com"),
                new NativeFederation.Target(D, "Relay only", "")));
        assertEquals(3, tabs.size());
        assertSame(profiles.entries.get(1), tabs.get(0).server); assertEquals(profiles.entries.get(1).id, tabs.get(0).key());
        assertNull(tabs.get(1).server); assertTrue(tabs.get(1).reachable()); assertEquals(NativeFederation.PREFIX + C, tabs.get(1).key());
        assertFalse(tabs.get(2).reachable());
        assertSame(tabs.get(2), NativeFederation.find(tabs, NativeFederation.PREFIX + D));
    }

    @Test public void trustedTabsCarryALinkMarkAndDescription() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server office = profiles.entries.get(1);
            List<NativeFederation.Tab> trusted = NativeFederation.tabs(profiles, List.of(
                    new NativeFederation.Target(B, "Office", "https://office.example.com:8443"), new NativeFederation.Target(C, "Lab", "")));
            java.util.List<String> chosen = new java.util.ArrayList<>();
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), profiles, Map.of(), chosen::add, true, () -> {}, trusted);
            Button home = tabs.button("legacy"), saved = tabs.button(office.id), pending = tabs.button(NativeFederation.PREFIX + C);
            assertEquals(0, links(home)); assertEquals(1, links(saved)); assertEquals(1, links(pending));
            assertEquals(1, ((Spanned) saved.getText()).getSpans(0, 1, NativeServerTabs.Dot.class).length);
            assertEquals("An unsaved target has no connection state to show", 0, ((Spanned) pending.getText()).getSpans(0, pending.length(), NativeServerTabs.Dot.class).length);
            assertEquals("Office, Disconnected, Trusted server", saved.getContentDescription().toString());
            assertEquals("Lab, Trusted server", pending.getContentDescription().toString());
            assertEquals("Spool, Disconnected", home.getContentDescription().toString());
            pending.performClick(); saved.performClick();
            assertEquals(List.of(NativeFederation.PREFIX + C, office.id), chosen);
        }
    }

    @Test public void trustedTabsFollowTheSelectedSourceWithoutRepeatingSavedServers() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server office = profiles.entries.get(1), ordinary = profiles.add("Ordinary"), source = profiles.add("Source");
            profiles.selected = source.id;
            List<NativeFederation.Target> targets = List.of(new NativeFederation.Target(C, "Lab", ""),
                    new NativeFederation.Target(B, "Office", "https://office.example.com:8443"));
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeServerTabs tabs = new NativeServerTabs(ui, profiles, Map.of(), id -> {}, true, () -> {}, NativeFederation.tabs(profiles, targets));
            assertEquals(List.of("legacy", ordinary.id, source.id, NativeFederation.PREFIX + C, office.id), tabOrder(tabs));
            profiles.selected = "legacy";
            NativeServerTabs home = new NativeServerTabs(ui, profiles, Map.of(), id -> {}, true, () -> {}, NativeFederation.tabs(profiles, targets));
            assertEquals(List.of("legacy", NativeFederation.PREFIX + C, office.id, ordinary.id, source.id), tabOrder(home));
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

    @Test public void aServerSignedInThroughTrustKeepsItsMarkWithoutTheSourceList() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = profiles();
            ServerProfiles.Server office = profiles.entries.get(1);
            office.account = ServerProfiles.displayAccount(new org.json.JSONObject("{id:'remote',nickname:'Alice',source_server_id:'" + B + "'}"));
            ServerProfiles restored = ServerProfiles.restore(profiles.encode(), office.id, profiles.entries.get(0).plan);
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), restored, Map.of(), id -> {}, true, () -> {});
            assertEquals(1, links(tabs.button(office.id))); assertEquals(0, links(tabs.button("legacy")));
        }
    }

    private static int links(Button tab) {
        return ((Spanned) tab.getText()).getSpans(0, tab.length(), NativeServerTabs.Link.class).length;
    }
}
