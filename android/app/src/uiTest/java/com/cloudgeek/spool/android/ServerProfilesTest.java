package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.view.View;
import java.io.File;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class ServerProfilesTest {
    @Test public void profilesRetainIndependentAddressesNetworkChoicesEnabledFlagsAndSelection() {
        ConnectionPlan legacy = new ConnectionPlan("https://first.example.com", "https://first.example.ts.net", ConnectionMode.TAILSCALE, true);
        ServerProfiles profiles = new ServerProfiles(legacy);
        ServerProfiles.Server second = profiles.add("Second");
        second.plan = new ConnectionPlan("https://second.example.com", "https://second.example.ts.net", ConnectionMode.DIRECT, true);
        profiles.active().enabled = false; profiles.selected = second.id;
        ServerProfiles restored = ServerProfiles.restore(profiles.encode(), second.id, new ConnectionPlan("", "", ConnectionMode.DIRECT));
        assertEquals(2, restored.entries.size()); assertEquals(second.id, restored.active().id);
        assertTrue(restored.active().enabled); assertFalse(restored.entries.get(0).enabled);
        assertEquals("https://second.example.com", restored.active().plan.direct);
        assertEquals(ConnectionMode.TAILSCALE, restored.entries.get(0).plan.selected);
        File root = new File("/private-app-data");
        assertEquals("/private-app-data/tsnet", restored.entries.get(0).directory(root, ConnectionMode.TAILSCALE));
        assertTrue(restored.active().directory(root, ConnectionMode.TAILSCALE).contains("/servers/" + second.id + "/tsnet"));
        assertNotEquals(restored.active().directory(root, ConnectionMode.DIRECT), restored.active().directory(root, ConnectionMode.TAILSCALE));
        assertNotEquals(restored.entries.get(0).sessionDirectory(root), restored.active().sessionDirectory(root));
        assertTrue(restored.active().sessionDirectory(root).endsWith("/servers/" + second.id + "/session"));
    }

    @Test public void displayAccountsSurviveRestartWithoutSavingCredentialsOrPermissions() throws Exception {
        ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
        profiles.active().account = new org.json.JSONObject("{id:'alice',nickname:'Alice',username:'alice',csrf:'secret',password:'secret',remote_desktop:true,avatar:{style:'clay',seed:'Alice',token:'secret'}}");
        String encoded = profiles.encode();
        assertFalse(encoded.contains("secret")); assertFalse(encoded.contains("remote_desktop"));
        ServerProfiles restored = ServerProfiles.restore(encoded, "legacy", new ConnectionPlan("", "", ConnectionMode.DIRECT));
        assertEquals("Alice", restored.active().account.getString("nickname"));
        assertEquals("clay", restored.active().account.getJSONObject("avatar").getString("style"));
        assertFalse(restored.active().account.has("csrf")); assertFalse(restored.active().account.getJSONObject("avatar").has("token"));
    }

    @Test public void malformedStoragePreservesTheOldServerAndRejectsUnsafeStateDirectories() {
        ConnectionPlan legacy = new ConnectionPlan("https://old.example.com", "", ConnectionMode.DIRECT);
        assertEquals(legacy.direct, ServerProfiles.restore("broken", "invalid", legacy).active().plan.direct);
        assertEquals("legacy", ServerProfiles.restore("[{id:'../../escape',name:'Other'}]", "../../escape", legacy).active().id);
    }

    @Test public void networkChoiceIsRememberedPerServerWithoutStoringAPriority() throws Exception {
        ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("https://first.example.com", "https://first.example.ts.net", ConnectionMode.DIRECT, true));
        ServerProfiles.Server second = profiles.add("Office");
        second.plan = new ConnectionPlan("https://office.example.com", "https://office.example.ts.net", ConnectionMode.TAILSCALE, true);
        profiles.selected = second.id;
        String encoded = profiles.encode();
        org.json.JSONArray stored = new org.json.JSONArray(encoded);
        assertFalse(stored.getJSONObject(0).has("priority"));
        assertEquals("tailscale", stored.getJSONObject(1).getString("selectedNetwork"));
        ServerProfiles restored = ServerProfiles.restore(encoded, second.id, new ConnectionPlan("", "", ConnectionMode.DIRECT));
        assertSame(ConnectionMode.DIRECT, restored.entries.get(0).plan.selected);
        assertSame(ConnectionMode.TAILSCALE, restored.active().plan.selected);
        assertSame(ConnectionMode.TAILSCALE, restored.active().plan.candidates(null).get(0).mode);
    }

    @Test public void deletingTheLastServerPersistsAnEmptyListAndNeverRestoresItsLegacyAddress() {
        ConnectionPlan legacy = new ConnectionPlan("https://removed.example.com", "", ConnectionMode.DIRECT);
        ServerProfiles profiles = new ServerProfiles(legacy);
        ServerProfiles.Server second = profiles.add("Office");
        profiles.remove("legacy");
        assertEquals(second.id, profiles.selected); assertSame(second, profiles.active());
        profiles.remove(second.id);
        assertNull(profiles.active()); assertEquals("", profiles.selected);
        ServerProfiles restored = ServerProfiles.restore(profiles.encode(), profiles.selected, legacy);
        assertTrue(restored.entries.isEmpty()); assertNull(restored.active());
        ServerProfiles.Server replacement = restored.add("New server");
        assertNotEquals("legacy", replacement.id); assertTrue(replacement.plan.direct.isEmpty());
    }

    @Test public void upgradingTheOldPrioritySettingsKeepsTheLastManuallySelectedNetwork() {
        String old = "[{id:'legacy',name:'Home',direct:'https://home.example.com',tailscale:'https://home.example.ts.net',priority:'direct'}]";
        ConnectionPlan lastUsed = new ConnectionPlan("https://home.example.com", "https://home.example.ts.net", ConnectionMode.TAILSCALE, true);
        ServerProfiles restored = ServerProfiles.restore(old, "legacy", lastUsed);
        assertSame(ConnectionMode.TAILSCALE, restored.active().plan.selected);
    }

    @Test public void tabsShowOnlyEnabledServersAndSelectionDoesNotChangeTheirEnabledState() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
            ServerProfiles.Server second = profiles.add("Second"), disabled = profiles.add("Disabled"); disabled.enabled = false;
            AtomicReference<String> selected = new AtomicReference<>();
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), profiles, Map.of(second.id, "connected"), selected::set);
            assertEquals(2, tabs.tabs.getChildCount());
            assertTrue(tabs.tabs.getChildAt(0).isSelected());
            tabs.tabs.getChildAt(1).performClick(); assertEquals(second.id, selected.get());
            assertTrue(profiles.entries.get(0).enabled); assertTrue(second.enabled); assertFalse(disabled.enabled);
            assertEquals("legacy", profiles.selected);
        }
    }

    @Test public void addServerDialogRequiresANameAndDoesNotAcceptAnEmptyServer() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            AtomicReference<String> name = new AtomicReference<>();
            NativeServerDialog dialog = new NativeServerDialog(new NativeUi(controller.get(), false), "", true, name::set);
            dialog.show(); dialog.save.performClick(); assertNull(name.get()); assertNotNull(dialog.name.getError());
            dialog.name.setText("  Home server  "); dialog.save.performClick();
            assertEquals("Home server", name.get()); assertFalse(dialog.isShowing());
        }
    }

    @Test public void settingsOnlyEditTheCurrentServerWhileSidebarTabsOwnThePlusButton() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
            ServerProfiles.Server second = profiles.add("Office"); second.enabled = false;
            AtomicReference<Boolean> checked = new AtomicReference<>(); int[] renamed = {0}, added = {0}, appearance = {0}, login = {0}, settingsOpened = {0};
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeServerSettings settings = new NativeServerSettings(ui, profiles.active(), checked::set, () -> renamed[0]++);
            assertEquals("Spool", settings.name.getText().toString());
            settings.enabled.performClick(); assertEquals(Boolean.FALSE, checked.get());
            settings.rename.performClick(); assertEquals(1, renamed[0]);
            NativeSidebar sidebar = new NativeSidebar(ui, new NativeSidebarState(), id -> new org.json.JSONObject(), () -> {}, id -> {}, id -> {},
                    () -> {}, () -> settingsOpened[0]++, () -> {}, () -> login[0]++, () -> appearance[0]++);
            sidebar.servers(profiles, Map.of(), id -> {}, () -> added[0]++);
            sidebar.bind(new org.json.JSONObject(), new org.json.JSONArray(), new org.json.JSONArray(), "", "", "", false);
            assertSame(sidebar.brand, sidebar.getChildAt(0));
            NativeServerTabs tabs = (NativeServerTabs) sidebar.getChildAt(1);
            assertEquals(3, tabs.tabs.getChildCount()); assertSame(tabs.add, tabs.tabs.getChildAt(2));
            tabs.add.performClick(); assertEquals(1, added[0]);
            sidebar.footer.getChildAt(1).performClick(); assertEquals(1, settingsOpened[0]);
            sidebar.footer.getChildAt(2).performClick(); assertEquals(1, login[0]);
            sidebar.brand.getChildAt(sidebar.brand.getChildCount() - 2).performClick(); assertEquals(1, appearance[0]);
        }
    }

    @Test public void tabStatusDotsAreBeforeTheNameAndUpdateWithoutRecreatingTheTab() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("https://server.example.com", "", ConnectionMode.DIRECT));
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), profiles, Map.of("legacy", "connected"), id -> {});
            android.widget.Button tab = (android.widget.Button) tabs.tabs.getChildAt(0);
            NativeServerTabs.Dot dot = ((android.text.Spanned) tab.getText()).getSpans(0, 1, NativeServerTabs.Dot.class)[0];
            int green = dot.color;
            assertNull(tab.getCompoundDrawablesRelative()[0]);
            assertTrue(tab.getText().toString().endsWith("Spool"));
            assertTrue(tab.getContentDescription().toString().contains("Connected"));
            tabs.status(Map.of("legacy", "connecting"));
            int yellow = dot.color;
            assertNotEquals(green, yellow);
            tabs.status(Map.of("legacy", "error"));
            int red = dot.color;
            assertNotEquals(yellow, red);
            profiles.active().enabled = false; tabs.status(Map.of("legacy", "connected"));
            assertNotEquals(green, dot.color);
            assertSame(tab, tabs.tabs.getChildAt(0));
        }
    }

    @Test public void overflowingTabsScrollHorizontallyKeepTheSelectedServerVisibleAndReachTheAddIcon() {
        org.robolectric.RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
            for (int i = 1; i <= 12; i++) profiles.add("Server " + i);
            profiles.selected = profiles.entries.get(10).id;
            int[] added = {0};
            NativeServerTabs tabs = new NativeServerTabs(new NativeUi(controller.get(), false), profiles, Map.of(), id -> {}, true, () -> added[0]++);
            controller.get().setContentView(tabs);
            tabs.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(100, View.MeasureSpec.AT_MOST));
            tabs.layout(0, 0, 360, tabs.getMeasuredHeight());
            int viewport = tabs.scroll.getWidth();
            assertTrue(tabs.tabs.getWidth() > viewport);
            assertTrue(tabs.scroll.getScrollX() > 0);
            View selected = tabs.tabs.getChildAt(10);
            assertTrue(selected.getLeft() >= tabs.scroll.getScrollX());
            assertTrue(selected.getRight() <= tabs.scroll.getScrollX() + viewport);
            tabs.scroll.scrollTo(0, 0);
            tabs.status(Map.of(profiles.selected, "connected"));
            tabs.layout(0, 0, 360, tabs.getMeasuredHeight());
            assertEquals("Status updates must not undo the user's scroll position", 0, tabs.scroll.getScrollX());
            tabs.scroll.scrollTo(tabs.tabs.getWidth(), 0);
            assertTrue(tabs.add.getRight() <= tabs.scroll.getScrollX() + viewport);
            assertTrue(tabs.add.getLeft() >= tabs.scroll.getScrollX());
            tabs.add.performClick(); assertEquals(1, added[0]);
        }
    }
}
