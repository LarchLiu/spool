package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.view.inputmethod.EditorInfo;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Add and edit share a settings window; identity edits never require a second dialog. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class,
        MainActivityConnectionTest.ShadowSpoolnet.class, MainActivityWorkspaceEventsTest.ShadowStream.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityServerSettingsTest {
    private static final class Fixture implements AutoCloseable {
        final MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false);
        final MainActivityGoalTest.Queue connections = MainActivityConnectionTest.connectionQueue(h);
        final ServerProfiles profiles = h.get("serverProfiles");
        Fixture() throws Exception {
            MainActivityConnectionTest.ShadowSpoolnet.failDirect = false;
            MainActivityConnectionTest.ShadowSpoolnet.tailnetHealth = null;
            h.call("closeSettings");
        }
        NativeSettings dialog() { return h.get("settingsDialog"); }
        NativeServerSettings identity() { return h.get("serverSettings"); }
        NativeConnectionSettings routes() { return h.get("connectionSettings"); }
        void metadata(String name) throws Exception {
            ReflectionHelpers.callInstanceMethod(h.activity, "rememberServerName",
                    ReflectionHelpers.ClassParameter.from(ServerProfiles.Server.class, profiles.active()),
                    ReflectionHelpers.ClassParameter.from(JSONObject.class, new JSONObject().put("server_name", name)));
        }
        ServerProfiles restored() {
            return ServerProfiles.restore(h.activity.getPreferences(Activity.MODE_PRIVATE).getString("servers", null),
                    profiles.selected, new ConnectionPlan("", "", ConnectionMode.DIRECT));
        }
        void snapshot(String name) throws Exception {
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            NativeModalFrame modal = dialog().modal;
            modal.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.AT_MOST));
            modal.layout(0, 0, modal.getMeasuredWidth(), modal.getMeasuredHeight());
            File directory = new File(System.getProperty("spool.ui.artifacts"), "server-settings");
            assertTrue(directory.isDirectory() || directory.mkdirs());
            Bitmap bitmap = Bitmap.createBitmap(modal.surface.getWidth(), modal.surface.getHeight(), Bitmap.Config.ARGB_8888);
            try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) {
                modal.surface.draw(new Canvas(bitmap)); assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output));
            } finally { bitmap.recycle(); }
        }
        @Override public void close() {
            h.activity.getPreferences(Activity.MODE_PRIVATE).edit().clear().commit();
            h.close();
        }
    }

    @Test public void sidebarPlusOpensTheWholeFormAndClosingAnUntouchedFormKeepsTheCurrentServer() throws Exception {
        try (Fixture f = new Fixture()) {
            String selected = f.profiles.selected;
            Object client = f.h.get("client");
            f.h.call("openSidebar");
            NativeServerTabs tabs = ReflectionHelpers.getField(f.h.<NativeSidebar>get("sidebar"), "serverTabs");
            tabs.add.performClick();
            NativeSettings dialog = f.dialog();
            assertTrue(dialog.isShowing()); assertEquals("Add server", dialog.content.title.getText().toString());
            assertEquals("", f.identity().name.getText().toString());
            assertEquals("Home", f.identity().name.getHint().toString());
            assertEquals(3, f.routes().rows.size());
            assertEquals(View.GONE, f.h.<View>get("serverDelete").getVisibility());
            assertEquals(1, f.profiles.entries.size()); assertEquals(selected, f.profiles.selected);
            assertSame(client, f.h.get("client"));
            f.snapshot("add-light-en");
            dialog.cancel();
            assertFalse(dialog.isShowing()); assertNull(f.dialog());
            assertFalse(f.h.<Boolean>get("settingsOpen"));
            assertEquals(1, f.profiles.entries.size()); assertEquals(selected, f.profiles.selected);
        }
    }

    @Test public void connectWithoutANameCreatesTheSelectedRouteEvenWhenNoServersRemain() throws Exception {
        for (ConnectionMode mode : new ConnectionMode[] {ConnectionMode.DIRECT, ConnectionMode.TAILSCALE}) try (Fixture f = new Fixture()) {
            f.h.set("client", null); f.h.set("user", null);
            f.profiles.entries.clear(); f.profiles.selected = "";
            f.h.call("openSettings");
            NativeSettings dialog = f.dialog();
            String address = mode == ConnectionMode.DIRECT ? "https://new.example.com" : "https://new.example.ts.net";
            f.routes().tabs.button(mode.value).performClick();
            f.routes().input(mode).setText(address);
            f.routes().rows.get(mode).use.performClick();
            assertSame(dialog, f.dialog()); assertTrue(dialog.isShowing());
            assertEquals(1, f.profiles.entries.size()); assertNull(f.h.get("newServer"));
            ServerProfiles.Server server = f.profiles.active();
            assertEquals("", server.name); assertEquals("Home", server.displayName()); assertTrue(server.enabled);
            assertEquals(address, server.plan.origin(mode)); assertSame(mode, server.plan.selected);
            assertEquals(address, f.restored().active().plan.origin(mode));
            assertEquals("Home · Settings", dialog.content.title.getText().toString());
            assertEquals(View.VISIBLE, f.h.<View>get("serverDelete").getVisibility());
        }
    }

    @Test public void inlineNameSavesOnEnterBlurAndCloseAndBlankNamesFollowServerMetadata() throws Exception {
        try (Fixture f = new Fixture()) {
            f.h.call("addServer");
            NativeSettings dialog = f.dialog(); NativeServerSettings identity = f.identity();
            identity.name.requestFocus(); identity.name.setText("  My office  ");
            identity.name.onEditorAction(EditorInfo.IME_ACTION_DONE);
            assertEquals(2, f.profiles.entries.size()); assertEquals("My office", f.restored().active().name);
            assertSame(dialog, f.dialog()); assertSame(identity, f.identity());
            f.metadata("Office Mac");
            assertEquals("My office · Settings", dialog.content.title.getText().toString());
            assertEquals("Office Mac", identity.name.getHint().toString());
            identity.name.setText(""); identity.name.clearFocus();
            assertEquals("", f.restored().active().name);
            assertEquals("Office Mac · Settings", dialog.content.title.getText().toString());
            f.h.call("openSidebar");
            NativeServerTabs tabs = ReflectionHelpers.getField(f.h.<NativeSidebar>get("sidebar"), "serverTabs");
            assertTrue(tabs.button(f.profiles.selected).getText().toString().endsWith("Office Mac"));
            identity.name.requestFocus(); identity.name.setText("Unfinished local edit");
            f.metadata("Renamed by administrator");
            assertEquals("Unfinished local edit", identity.name.getText().toString());
            dialog.content.close.performClick();
            assertEquals("Unfinished local edit", f.restored().active().name); assertNull(f.dialog());
            f.h.call("openSettings"); f.identity().name.setText(""); f.dialog().cancel();
            assertEquals("Renamed by administrator", f.restored().active().displayName());
        }
    }

    @Test public void closeSavesAValidAddressWithAnOptionalNameWithoutStartingAConnection() throws Exception {
        for (boolean nameFirst : new boolean[] {false, true}) try (Fixture f = new Fixture()) {
            f.h.call("addServer");
            f.identity().name.setText("Office");
            if (nameFirst) f.identity().name.onEditorAction(EditorInfo.IME_ACTION_DONE);
            f.routes().input(ConnectionMode.DIRECT).setText("https://NEW.example.com/");
            f.dialog().content.close.performClick();
            assertNull(f.dialog()); assertEquals(2, f.profiles.entries.size());
            assertEquals("Office", f.restored().active().name);
            assertEquals("https://new.example.com", f.restored().active().plan.direct);
            assertFalse(f.restored().active().enabled); assertTrue(f.connections.jobs.isEmpty());
        }
    }

    @Test public void aSavedNameDoesNotPreventLaterAddressEditsOrAnEnableAction() throws Exception {
        try (Fixture f = new Fixture()) {
            f.h.call("addServer"); NativeSettings dialog = f.dialog();
            f.identity().name.setText("Office"); f.identity().name.onEditorAction(EditorInfo.IME_ACTION_DONE);
            f.routes().input(ConnectionMode.DIRECT).requestFocus();
            f.routes().input(ConnectionMode.DIRECT).setText("https://first.example.com");
            f.routes().input(ConnectionMode.DIRECT).clearFocus();
            assertEquals("https://first.example.com", f.restored().active().plan.direct);
            f.routes().input(ConnectionMode.DIRECT).setText("https://second.example.com");
            f.identity().enabled.performClick();
            assertTrue(f.profiles.active().enabled); assertNull(f.h.get("newServer"));
            assertEquals("https://second.example.com", f.h.<ConnectionPlan>get("connectionPlan").direct);
            assertEquals("https://second.example.com", f.restored().active().plan.direct);
            assertSame(dialog, f.dialog());
        }
    }

    @Test public void invalidAddressKeepsTheDraftOpenAndTheExistingServerSelected() throws Exception {
        try (Fixture f = new Fixture()) {
            String selected = f.profiles.selected;
            f.h.call("addServer"); NativeSettings dialog = f.dialog();
            f.routes().input(ConnectionMode.DIRECT).setText("http://invalid.example.com");
            f.routes().rows.get(ConnectionMode.DIRECT).use.performClick();
            assertEquals(1, f.profiles.entries.size()); assertEquals(selected, f.profiles.selected);
            assertNotNull(f.routes().input(ConnectionMode.DIRECT).getError());
            f.identity().enabled.performClick(); assertFalse(f.identity().enabled.isChecked());
            dialog.cancel();
            assertSame(dialog, f.dialog()); assertTrue(dialog.isShowing()); assertTrue(f.h.<Boolean>get("settingsOpen"));
            f.routes().input(ConnectionMode.DIRECT).setText(""); dialog.cancel();
            assertNull(f.dialog()); assertEquals(1, f.profiles.entries.size());
        }
    }

    @Test public void changingAppearancePreservesTheOpenAddFormNameAddressAndVisibleTab() throws Exception {
        try (Fixture f = new Fixture()) {
            f.h.call("addServer"); NativeSettings dialog = f.dialog();
            f.identity().name.requestFocus();
            f.identity().name.setText("Name in progress");
            f.routes().input(ConnectionMode.TAILSCALE).setText("https://office.example.ts.net");
            f.routes().tabs.button(ConnectionMode.TAILSCALE.value).performClick();
            f.h.set("theme", "dark"); f.h.set("language", "zh-CN"); f.h.call("render");
            assertSame(dialog, f.dialog()); assertTrue(dialog.isShowing());
            assertEquals("Name in progress", f.identity().name.getText().toString());
            assertEquals("https://office.example.ts.net", f.routes().plan().tailscale);
            assertTrue(f.routes().tabs.button(ConnectionMode.TAILSCALE.value).isSelected());
            assertEquals("显示名称（可选）", f.identity().name.getContentDescription());
            f.snapshot("add-dark-zh-CN");
        }
    }

    @Test public void tailscaleEnrollmentCanStartDirectlyFromAnUnnamedNewServer() throws Exception {
        for (boolean nameFirst : new boolean[] {false, true}) try (Fixture f = new Fixture()) {
            f.h.call("addServer");
            if (nameFirst) { f.identity().name.setText("Office"); f.identity().name.onEditorAction(EditorInfo.IME_ACTION_DONE); }
            f.routes().tabs.button(ConnectionMode.TAILSCALE.value).performClick();
            f.routes().input(ConnectionMode.TAILSCALE).setText("https://office.example.ts.net");
            assertTrue(f.routes().login.isEnabled());
            f.routes().login.performClick();
            assertEquals(2, f.profiles.entries.size()); assertEquals(nameFirst ? "Office" : "", f.profiles.active().name);
            assertEquals("https://office.example.ts.net", f.restored().active().plan.tailscale);
            assertSame(ConnectionMode.TAILSCALE, f.restored().active().plan.selected);
            assertTrue(f.profiles.active().enabled); assertFalse(f.connections.jobs.isEmpty());
        }
    }

    @Test public void scannedPairingBelongsToTheNewServerAndIsIgnoredAfterItsFormCloses() throws Exception {
        JSONObject payload = new JSONObject(new String(Base64.getUrlDecoder().decode(RelayPairingTest.code("https://relay.example.com").substring(11)), StandardCharsets.UTF_8));
        payload.put("invite", Base64.getUrlEncoder().withoutPadding().encodeToString(new byte[32])).put("expires_at", 2000000000L);
        String code = "spool-pair:" + Base64.getUrlEncoder().withoutPadding().encodeToString(payload.toString().getBytes(StandardCharsets.UTF_8));
        for (boolean cancel : new boolean[] {false, true}) try (Fixture f = new Fixture()) {
            String previous = f.profiles.selected;
            f.h.call("addServer");
            f.routes().tabs.button(ConnectionMode.RELAY.value).performClick(); f.routes().scan.performClick();
            org.robolectric.shadows.ShadowActivity.IntentForResult launched = org.robolectric.Shadows.shadowOf(f.h.activity).getNextStartedActivityForResult();
            assertEquals(49371, launched.requestCode); assertEquals(PairingScanActivity.class.getName(), launched.intent.getComponent().getClassName());
            assertEquals("QR_CODE", launched.intent.getStringExtra(com.google.zxing.client.android.Intents.Scan.FORMATS));
            assertEquals(f.h.<ServerProfiles.Server>get("newServer").id, f.h.get("pairingOwner"));
            if (cancel) f.dialog().cancel();
            ReflectionHelpers.callInstanceMethod(f.h.activity, "onActivityResult",
                    ReflectionHelpers.ClassParameter.from(int.class, 49371),
                    ReflectionHelpers.ClassParameter.from(int.class, Activity.RESULT_OK),
                    ReflectionHelpers.ClassParameter.from(Intent.class, new Intent().putExtra("SCAN_RESULT", code).putExtra("SCAN_RESULT_FORMAT", "QR_CODE")));
            if (cancel) {
                assertEquals(1, f.profiles.entries.size()); assertEquals(previous, f.profiles.selected);
            } else {
                assertEquals(2, f.profiles.entries.size()); assertNotEquals(previous, f.profiles.selected);
                assertEquals(code, f.profiles.active().plan.relay); assertSame(ConnectionMode.RELAY, f.profiles.active().plan.selected);
                assertEquals(code, f.routes().plan().relay);
                assertEquals("", f.profiles.active().name);
                android.app.Dialog confirmation = org.robolectric.shadows.ShadowDialog.getLatestDialog();
                assertTrue(confirmation instanceof NativePairingDialog); assertTrue(confirmation.isShowing());
                confirmation.dismiss();
            }
        }
    }
}
