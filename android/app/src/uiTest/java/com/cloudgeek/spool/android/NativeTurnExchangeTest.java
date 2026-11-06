package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.nio.charset.StandardCharsets;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeTurnExchangeTest {
    @Test public void viewportRestoresEachInputExpansionIndependently() throws Exception {
        try (org.robolectric.android.controller.ActivityController<android.app.Activity> controller = org.robolectric.Robolectric.buildActivity(android.app.Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            NativeTranscriptViewport.Row row = new NativeTranscriptViewport.Row(ui);
            row.bind(children -> {
                children.use("one", "user", () -> new NativeUserRow(ui, null, "First prompt\n".repeat(20), 0, null, ignored -> {}));
                children.use("two", "user", () -> new NativeUserRow(ui, null, "Follow up\n".repeat(20), 0, null, ignored -> {}));
            });
            row.mount();
            ((NativeUserRow) row.getChildAt(0)).message.restoreExpanded(true);
            assertFalse(((NativeUserRow) row.getChildAt(1)).message.content.expanded);
            row.release(false); row.mount();
            assertTrue(((NativeUserRow) row.getChildAt(0)).message.content.expanded);
            assertFalse(((NativeUserRow) row.getChildAt(1)).message.content.expanded);
        }
    }

    @Test public void chronologicalBoundariesMatchWebIncludingAttachmentsAndAdjacentMessages() throws Exception {
        JSONArray fixtures;
        try (java.io.InputStream input = getClass().getResourceAsStream("/turn-exchanges.json")) {
            assertNotNull(input); fixtures = new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        for (int i = 0; i < fixtures.length(); i++) {
            JSONObject fixture = fixtures.getJSONObject(i); JSONArray parts = fixture.getJSONArray("parts"), expected = fixture.getJSONArray("ranges");
            String before = parts.toString();
            List<NativeTurnExchange> exchanges = NativeTurnExchange.split(parts, 0, parts.length());
            JSONArray actual = new JSONArray();
            for (NativeTurnExchange exchange : exchanges) actual.put(new JSONArray().put(exchange.start).put(exchange.inputEnd).put(exchange.end));
            assertTrue(fixture.getString("id"), NativeChatState.equivalent(expected, actual));
            assertEquals(before, parts.toString());
            JSONArray padded = new JSONArray().put(new JSONObject().put("role", "assistant"));
            for (int j = 0; j < parts.length(); j++) padded.put(parts.get(j));
            List<NativeTurnExchange> offset = NativeTurnExchange.split(padded, 1, padded.length());
            for (int j = 0; j < exchanges.size(); j++) {
                assertEquals(exchanges.get(j).start + 1, offset.get(j).start);
                assertEquals(exchanges.get(j).inputEnd + 1, offset.get(j).inputEnd);
                assertEquals(exchanges.get(j).end + 1, offset.get(j).end);
            }
        }
    }
}
