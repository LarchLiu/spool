package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import static org.robolectric.Shadows.shadowOf;
import android.app.Activity;
import android.os.Looper;
import android.view.View;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Base64;
import java.util.concurrent.atomic.AtomicReference;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeDevicePairingTest {
    private static RelayPairing invitation() throws Exception {
        JSONObject value = new JSONObject(new String(Base64.getUrlDecoder().decode(RelayPairingTest.code("https://relay.example.com").substring(11)), StandardCharsets.UTF_8));
        value.put("invite", Base64.getUrlEncoder().withoutPadding().encodeToString(new byte[32]))
                .put("expires_at", System.currentTimeMillis() / 1000 + 300);
        return new RelayPairing("spool-pair:" + Base64.getUrlEncoder().withoutPadding().encodeToString(value.toString().getBytes(StandardCharsets.UTF_8)));
    }
    static final class FakePairing implements NativeDevicePairingDialog.Transport {
        int calls, closes;
        boolean approved, fail;
        public String fingerprint() { return "abcd1234".repeat(8); }
        public String poll() throws Exception { calls++; if (fail) throw new Exception("rejected"); return approved ? "approved" : "pending"; }
        public String code() { if (!approved) throw new AssertionError("approval missing"); return "saved-device-code"; }
        public void close() { closes++; }
    }
    @Test public void onlyDesktopApprovalConnectsAndClosesPairingTransport() throws Exception {
        try (var activity = Robolectric.buildActivity(Activity.class).setup()) {
            FakePairing transport = new FakePairing();
            AtomicReference<String> connected = new AtomicReference<>();
            NativeDevicePairingDialog dialog = new NativeDevicePairingDialog(new NativeUi(activity.get(), false), "/private", invitation(), Runnable::run, connected::set, (d,c,n) -> transport);
            dialog.show(); shadowOf(Looper.getMainLooper()).idle();
            assertTrue(dialog.isShowing()); assertEquals(1, transport.calls); assertNull(connected.get());
            transport.approved = true;
            shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1200));
            assertEquals("saved-device-code", connected.get()); assertFalse(dialog.isShowing()); assertEquals(1, transport.closes);
        }
    }
    @Test public void dismissStopsPollingAndFailuresNeverConnect() throws Exception {
        try (var activity = Robolectric.buildActivity(Activity.class).setup()) {
            for (boolean fail : new boolean[]{false, true}) {
                FakePairing transport = new FakePairing(); transport.fail = fail;
                AtomicReference<String> connected = new AtomicReference<>();
                NativeDevicePairingDialog dialog = new NativeDevicePairingDialog(new NativeUi(activity.get(), true, "zh-CN"), "/private", invitation(), Runnable::run, connected::set, (d,c,n) -> transport);
                dialog.show(); shadowOf(Looper.getMainLooper()).idle();
                if (fail) assertEquals(View.VISIBLE, dialog.error.getVisibility());
                dialog.dismiss(); shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(10));
                assertNull(connected.get()); assertEquals(1, transport.calls); assertEquals(1, transport.closes);
            }
        }
    }
}
