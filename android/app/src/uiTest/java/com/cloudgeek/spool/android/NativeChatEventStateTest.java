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
public class NativeChatEventStateTest {
    @Test public void primaryAndNavigationStateMatchActualWebSnapshotMetadataAndTurnCallbacks() throws Exception {
        JSONArray cases;
        try (InputStream input = getClass().getResourceAsStream("/web-chat-events.json")) {
            assertNotNull(input); cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("applicationCases");
        }
        assertEquals(12, cases.length());
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i), previous = item.optJSONObject("previous"), payload = item.getJSONObject("value");
            JSONObject expected = item.getJSONObject("expected"); String kind = item.getString("kind");
            JSONObject actual = NativeChatState.event(previous, kind, payload);
            JSONArray list = NativeChatState.eventList(item.getJSONArray("chats"), "chat-one", kind, payload);
            assertTrue(item.getString("id") + " primary state", NativeChatState.equivalent(expected.optJSONObject("chat"), actual));
            assertTrue(item.getString("id") + " sidebar state: " + list, NativeChatState.equivalent(expected.getJSONArray("chats"), list));
        }
    }
}
