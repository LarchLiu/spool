package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.widget.Button;
import com.google.zxing.*;
import com.google.zxing.common.*;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class RelayPairingTest {
    static String code(String address) throws Exception {
        String value = new JSONObject().put("v", 1).put("relay", address)
                .put("key", Base64.getEncoder().encodeToString(new byte[32]))
                .put("token", Base64.getUrlEncoder().withoutPadding().encodeToString(new byte[32])).toString();
        return "spool-pair:" + Base64.getUrlEncoder().withoutPadding().encodeToString(value.getBytes(StandardCharsets.UTF_8));
    }
    @Test public void scannerDecodesPairingAndConfirmationIsRequired() throws Exception {
        String encoded = code("https://relay.example.com");
        BitMatrix matrix = new MultiFormatWriter().encode(encoded, BarcodeFormat.QR_CODE, 512, 512);
        int[] pixels = new int[512 * 512];
        for (int y = 0; y < 512; y++) for (int x = 0; x < 512; x++) pixels[y * 512 + x] = matrix.get(x, y) ? 0xff000000 : 0xffffffff;
        BinaryBitmap bitmap = new BinaryBitmap(new HybridBinarizer(new RGBLuminanceSource(512, 512, pixels)));
        assertEquals("QR raster conversion", matrix, bitmap.getBlackMatrix());
        Result scanned = new MultiFormatReader().decode(bitmap, java.util.Map.of(DecodeHintType.TRY_HARDER, true, DecodeHintType.PURE_BARCODE, true));
        RelayPairing pairing = new RelayPairing(scanned.getText());
        assertEquals(encoded, pairing.code);
        assertEquals("66687aad f862bd77 6c8fc18b 8e9f8e20 08971485 6ee233b3 902a591d 0d5f2925", pairing.fingerprint);
        assertEquals(52, pairing.id.length());
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            AtomicBoolean accepted = new AtomicBoolean();
            NativePairingDialog dialog = new NativePairingDialog(new NativeUi(controller.get(), false), pairing, () -> accepted.set(true));
            dialog.show(); assertFalse(accepted.get());
            ((Button) dialog.actions.getChildAt(1)).performClick();
            assertTrue(accepted.get()); assertFalse(dialog.isShowing());
        }
    }
    @Test public void pairedIdentityAndNetworkChoiceSurviveProfileStorage() throws Exception {
        String encoded = code("https://relay.example.com");
        ConnectionPlan plan = new ConnectionPlan("", "", encoded, ConnectionMode.RELAY, true).validated();
        ServerProfiles profiles = new ServerProfiles(plan);
        ServerProfiles saved = ServerProfiles.restore(profiles.encode(), "legacy", new ConnectionPlan("", "", ConnectionMode.DIRECT));
        assertEquals(encoded, saved.active().plan.relay);
        assertEquals(ConnectionMode.RELAY, saved.active().plan.candidates(null).get(0).mode);
        assertEquals(encoded, plan.select(ConnectionMode.DIRECT).automatic(false).relay);
    }
    @Test public void malformedCodesAndUnsafeRelayAddressesAreRejected() throws Exception {
        for (String invalid : new String[]{"https://relay.example.com", "spool-pair:broken", code("http://relay.example.com"), code("https://user:password@relay.example.com"), code("https://relay.example.com/path")}) {
            try { new RelayPairing(invalid); fail("accepted invalid pairing"); } catch (IllegalArgumentException expected) {}
        }
    }
}
