package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeHistoryStateTest {
    private JSONObject oracle() throws Exception {
        try (InputStream input = getClass().getResourceAsStream("/web-history.json")) {
            assertNotNull(input); return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }
    @Test public void historyMergeAndSubsequentLiveReconciliationMatchActualWeb() throws Exception {
        JSONArray cases = oracle().getJSONArray("merges"); assertEquals(9, cases.length());
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i), previous = item.getJSONObject("previous"), page = item.getJSONObject("page");
            String original = previous.toString(), incoming = page.toString();
            JSONObject result = item.optString("mode").equals("reconcile") ? NativeChatState.reconcile(previous, page) : NativeChatState.prepend(previous, page);
            assertTrue(item.getString("id") + "\n" + result, NativeChatState.equivalent(item.getJSONObject("expected"), result));
            assertEquals(original, previous.toString()); assertEquals(incoming, page.toString());
        }
    }

    @Test public void historyScrollTargetsMatchActualWebBeforeNativeViewportClamping() throws Exception {
        JSONArray cases = oracle().getJSONArray("scrolls"); assertEquals(18, cases.length());
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i), anchor = item.optJSONObject("anchor");
            assertEquals(item.toString(), item.getInt("expectedTop"), NativeHistoryScroll.restore(item.getInt("height"), item.getInt("initialTop"),
                    item.getBoolean("following"), anchor == null ? null : new NativeHistoryScroll.Anchor(anchor.getInt("height"), anchor.getInt("top"))));
        }
    }
}
