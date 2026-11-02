package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeChatStateTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }
    private Set<String> keys(JSONObject value) {
        Set<String> result = new HashSet<>(); value.keys().forEachRemaining(result::add); return result;
    }
    private void same(Object expected, Object actual, String label) throws Exception {
        if (expected instanceof JSONObject) {
            assertTrue(label, actual instanceof JSONObject);
            JSONObject a = (JSONObject) expected, b = (JSONObject) actual;
            assertEquals(label + " keys", keys(a), keys(b));
            for (String key : keys(a)) same(a.get(key), b.get(key), label + "." + key);
        } else if (expected instanceof JSONArray) {
            assertTrue(label, actual instanceof JSONArray); JSONArray a = (JSONArray) expected, b = (JSONArray) actual;
            assertEquals(label + " length", a.length(), b.length());
            for (int i = 0; i < a.length(); i++) same(a.get(i), b.get(i), label + "[" + i + "]");
        } else if (expected instanceof Number) assertEquals(label, ((Number) expected).doubleValue(), ((Number) actual).doubleValue(), 0);
        else assertEquals(label, expected == JSONObject.NULL ? null : expected, actual == JSONObject.NULL ? null : actual);
    }

    @Test public void optimisticAdoptionReconciliationAndRunningTurnsMatchActualWebStateFunctions() throws Exception {
        JSONArray cases = fixtures().getJSONArray("chatStates"); assertTrue(cases.length() >= 20);
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i), previous = item.optJSONObject("previous"), next = item.optJSONObject("next");
            String before = previous == null ? "" : previous.toString(), after = next == null ? "" : next.toString();
            JSONObject result;
            if (item.optString("operation").equals("optimistic") || item.optString("operation").equals("adopt")) {
                result = NativeChatState.optimistic(previous, item.getString("project"), item.getJSONObject("turn"), item.getJSONArray("input"));
                if (item.optString("operation").equals("adopt")) result = NativeChatState.adoptCreated(result, next);
            } else result = NativeChatState.reconcile(previous, next);
            same(item.get("expected"), result, item.getString("id"));
            same(item.get("running"), NativeChatState.runningTurn(result), item.getString("id") + ".running");
            assertEquals(item.getString("id") + ".turnRunning", item.getBoolean("turnRunning"), NativeChatState.hasRunningTurn(result));
            assertEquals(before, previous == null ? "" : previous.toString()); assertEquals(after, next == null ? "" : next.toString());
        }
    }

    @Test public void equivalentSnapshotsIgnoreObjectOrderButRetainExactArrayAndValueSemantics() throws Exception {
        JSONObject a = new JSONObject("{\"id\":\"one\",\"nested\":{\"a\":null,\"b\":[1,true,\"x\"]}}");
        JSONObject b = new JSONObject("{\"nested\":{\"b\":[1.0,true,\"x\"],\"a\":null},\"id\":\"one\"}");
        assertTrue(NativeChatState.equivalent(a, b)); assertTrue(NativeChatState.equivalent(null, null));
        assertFalse(NativeChatState.equivalent(a, null)); assertFalse(NativeChatState.equivalent(a, a.toString()));
        b.getJSONObject("nested").getJSONArray("b").put(1, 1);
        assertFalse(NativeChatState.equivalent(a, b));
        b = NativeChatState.copy(a); b.getJSONObject("nested").remove("a");
        assertFalse(NativeChatState.equivalent(a, b));
        assertFalse(NativeChatState.equivalent(new JSONArray("[1,2]"), new JSONArray("[2,1]")));
        assertFalse(NativeChatState.equivalent(new JSONArray("[1]"), new JSONArray("[1,2]")));
        assertFalse(NativeChatState.equivalent(JSONObject.NULL, null));
    }

    @Test public void listRefreshReconcilesOnlyExistingMatchingChatsAndRejectsOlderStatus() throws Exception {
        JSONObject old = new JSONObject("{\"id\":\"one\",\"updated_at\":20,\"status\":\"completed\",\"turns\":[]}");
        JSONObject other = new JSONObject("{\"id\":\"two\",\"updated_at\":1}");
        JSONArray chats = new JSONArray().put(old).put(other);
        JSONObject stale = new JSONObject("{\"id\":\"one\",\"updated_at\":10,\"status\":\"running\",\"turns\":[]}");
        JSONArray result = NativeChatState.reconcileList(chats, stale);
        assertSame(old, result.getJSONObject(0)); assertSame(other, result.getJSONObject(1));
        JSONObject fresh = new JSONObject("{\"id\":\"one\",\"updated_at\":30,\"title\":\"New title\",\"status\":\"completed\",\"turns\":[]}");
        result = NativeChatState.reconcileList(chats, fresh);
        assertEquals("New title", result.getJSONObject(0).getString("title")); assertSame(other, result.getJSONObject(1));
        assertFalse(old.has("title")); assertEquals(2, NativeChatState.reconcileList(chats, new JSONObject("{\"id\":\"missing\"}")).length());
    }

    @Test public void optimisticInputUsesTheActualWebHelperWhitespaceAndAttachmentSemantics() throws Exception {
        JSONArray cases = fixtures().getJSONArray("inputTranscripts");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i);
            same(item.getJSONArray("expected"), NativeChatState.input(item.getString("message"), item.getJSONArray("attachments")), "input " + i);
        }
    }

    private static final class NoSerialization extends JSONObject {
        @Override public String toString() { throw new AssertionError("A chat update must not serialize the transcript"); }
    }

    private JSONObject largeChat(String id, String body, int count) throws Exception {
        JSONArray parts = new JSONArray(), turns = new JSONArray();
        for (int i = 0; i < count; i++) {
            parts.put(new NoSerialization().put("role", "tool").put("kind", "tool_result").put("text", body));
            turns.put(new NoSerialization().put("id", id + i).put("status", "completed").put("part_start", i).put("input_end", i).put("part_end", i + 1));
        }
        return new NoSerialization().put("id", "large").put("updated_at", 10).put("status", "completed")
                .put("transcript", new NoSerialization().put("parts", parts)).put("turns", turns);
    }

    @Test public void largeSnapshotsCopyOnlyContainersAndReuseExactImmutableStrings() throws Exception {
        String body = "line with escaped characters \"\\\n".repeat(16384);
        JSONObject before = largeChat("turn", body, 80), copied = NativeChatState.copy(before);
        assertNotSame(before, copied); assertNotSame(before.getJSONObject("transcript"), copied.getJSONObject("transcript"));
        JSONArray original = before.getJSONObject("transcript").getJSONArray("parts"), parts = copied.getJSONObject("transcript").getJSONArray("parts");
        assertNotSame(original, parts);
        for (int i = 0; i < parts.length(); i++) {
            assertNotSame(original.getJSONObject(i), parts.getJSONObject(i));
            assertSame(body, parts.getJSONObject(i).get("text"));
        }
        parts.getJSONObject(0).put("text", "changed"); assertSame(body, original.getJSONObject(0).get("text"));
    }

    @Test public void longHistoryAndStreamUpdatesNeverSerializeOrMutateTheirInputs() throws Exception {
        String body = "large tool output\n".repeat(32768);
        JSONObject before = largeChat("turn", body, 80), page = largeChat("older", body, 2);
        JSONObject prepended = NativeChatState.prepend(before, page);
        assertEquals(82, prepended.getJSONArray("turns").length());
        assertEquals(80, before.getJSONArray("turns").length()); assertEquals(2, page.getJSONArray("turns").length());
        assertSame(before.getJSONObject("transcript").getJSONArray("parts").get(0), prepended.getJSONObject("transcript").getJSONArray("parts").get(2));
        JSONObject data = new NoSerialization().put("updated_at", 11).put("status", "running")
                .put("turn", new JSONObject().put("id", "turn79").put("status", "running").put("part_start", 79).put("part_end", 80).put("input_end", 79));
        JSONObject updated = NativeChatState.event(before, "turn", data);
        assertEquals(80, updated.getJSONArray("turns").length());
        assertSame(body, updated.getJSONObject("transcript").getJSONArray("parts").getJSONObject(0).get("text"));
        JSONObject metadata = NativeChatState.metadata(before, new JSONObject().put("id", "large").put("updated_at", 11).put("title", "New title"));
        assertSame(before.getJSONObject("transcript"), metadata.getJSONObject("transcript"));
        assertFalse(before.has("title")); assertFalse(data.has("turns"));
        JSONObject pending = NativeChatState.optimistic(before, "project", new JSONObject().put("id", "pending-one"), new JSONArray().put(new JSONObject().put("role", "user").put("text", "new input")));
        assertEquals(81, pending.getJSONArray("turns").length()); assertEquals(80, before.getJSONArray("turns").length());
        assertEquals(80, before.getJSONObject("transcript").getJSONArray("parts").length());
    }
}
