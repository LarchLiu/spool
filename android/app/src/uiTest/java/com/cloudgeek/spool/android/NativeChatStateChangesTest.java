package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import java.nio.charset.StandardCharsets;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

/** Merges report exactly the turns whose rows must rebind; everything else keeps its references. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeChatStateChangesTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }
    private static JSONObject stripped(JSONObject turn) throws Exception {
        JSONObject copy = new JSONObject(turn.toString());
        for (String key : new String[] {"part_start", "input_end", "part_end"}) copy.remove(key);
        return copy;
    }
    private static JSONObject turnById(JSONObject chat, String id) {
        JSONArray turns = chat.optJSONArray("turns");
        for (int i = 0; turns != null && i < turns.length(); i++) if (turns.optJSONObject(i).optString("id").equals(id)) return turns.optJSONObject(i);
        return null;
    }
    private static JSONObject part(String role, String text) throws Exception { return new JSONObject().put("role", role).put("kind", "text").put("text", text); }
    private static JSONObject chat() throws Exception {
        JSONArray turns = new JSONArray(), parts = new JSONArray();
        for (int i = 0; i < 3; i++) {
            parts.put(part("user", "Prompt " + i)); parts.put(part("assistant", "Answer " + i).put("phase", "final_answer"));
            turns.put(new JSONObject().put("id", "t" + i).put("status", "completed").put("part_start", i * 2).put("input_end", i * 2 + 1).put("part_end", i * 2 + 2));
        }
        return new JSONObject().put("id", "c").put("title", "T").put("updated_at", 10).put("status", "completed").put("can_control", true)
                .put("participants", new JSONArray()).put("automation_chat", false).put("collaborator_ids", new JSONArray()).put("artifacts", new JSONArray())
                .put("turns", turns).put("transcript", new JSONObject().put("parts", parts));
    }
    private static JSONObject copy(JSONObject value) throws Exception { return new JSONObject(value.toString()); }

    @Test public void trackedMergesMatchTheUntrackedEntryPointsAndUnchangedTurnsKeepTheirPartReferences() throws Exception {
        JSONArray cases = fixtures().getJSONArray("chatStates"); int reconciled = 0;
        for (int i = 0; i < cases.length(); i++) {
            JSONObject item = cases.getJSONObject(i);
            if (item.optString("operation").equals("optimistic") || item.optString("operation").equals("adopt")) continue;
            JSONObject previous = item.optJSONObject("previous"), next = item.optJSONObject("next");
            NativeChatState.Merge merge = NativeChatState.reconcileTracked(previous, next); reconciled++;
            assertTrue(item.getString("id"), NativeChatState.equivalent(NativeChatState.reconcile(previous, next), merge.chat));
            if (merge.unchanged) assertTrue(item.getString("id") + " unchanged merges are deep-equal", NativeChatState.equivalent(previous, merge.chat));
            if (merge.changedTurns == null || previous == null) continue;
            JSONArray turns = merge.chat.getJSONArray("turns"), parts = merge.chat.getJSONObject("transcript").getJSONArray("parts");
            JSONArray oldParts = previous.getJSONObject("transcript").getJSONArray("parts");
            for (int t = 0; t < turns.length(); t++) {
                JSONObject turn = turns.getJSONObject(t); String id = turn.getString("id");
                if (merge.changedTurns.contains(id)) continue;
                JSONObject old = turnById(previous, id);
                assertNotNull(item.getString("id") + ": an unlisted turn must have existed before: " + id, old);
                // Skipping a row's rebind is only safe when its data and its part objects are identical.
                assertTrue(item.getString("id") + "/" + id, NativeChatState.equivalent(stripped(old), stripped(turn)));
                assertEquals(old.getInt("part_end") - old.getInt("part_start"), turn.getInt("part_end") - turn.getInt("part_start"));
                for (int p = 0; p < turn.getInt("part_end") - turn.getInt("part_start"); p++)
                    assertSame(item.getString("id") + "/" + id + "[" + p + "]", oldParts.get(old.getInt("part_start") + p), parts.get(turn.getInt("part_start") + p));
            }
        }
        assertTrue(reconciled >= 10);
    }

    @Test public void turnEventsNameOnlyTheChangedTurnAndIdenticalSnapshotsAreUnchanged() throws Exception {
        JSONObject chat = chat();
        JSONObject data = copy(chat).put("updated_at", 11).put("status", "running");
        data.getJSONObject("transcript").getJSONArray("parts").getJSONObject(5).put("text", "Answer 2 streaming");
        data.getJSONArray("turns").getJSONObject(2).put("status", "running");
        data.put("turn", data.getJSONArray("turns").getJSONObject(2));
        data.remove("id"); data.remove("can_control"); data.remove("participants"); data.remove("automation_chat"); data.remove("collaborator_ids"); data.remove("artifacts");
        NativeChatState.Merge merge = NativeChatState.eventTracked(chat, "turn", data);
        assertEquals(Set.of("t2"), merge.changedTurns); assertFalse(merge.reordered); assertFalse(merge.unchanged);
        assertEquals("Answer 2 streaming", merge.chat.getJSONObject("transcript").getJSONArray("parts").getJSONObject(5).getString("text"));
        assertSame(chat.getJSONObject("transcript").getJSONArray("parts").get(0), merge.chat.getJSONObject("transcript").getJSONArray("parts").get(0));
        NativeChatState.Merge same = NativeChatState.reconcileTracked(merge.chat, copy(merge.chat));
        assertTrue(same.unchanged); assertTrue(same.changedTurns.isEmpty()); assertFalse(same.reordered);
        NativeChatState.Merge older = NativeChatState.reconcileTracked(merge.chat, copy(chat));
        assertTrue("An older snapshot changes nothing", older.unchanged); assertSame(merge.chat, older.chat);
        NativeChatState.Merge other = NativeChatState.reconcileTracked(chat, copy(chat).put("id", "elsewhere"));
        assertNull("A different chat rebinds everything", other.changedTurns); assertFalse(other.unchanged);
    }

    @Test public void prependedPagesAndMetadataReportTheirScope() throws Exception {
        JSONObject chat = chat();
        JSONArray turns = new JSONArray(), parts = new JSONArray();
        for (int i = 0; i < 2; i++) {
            parts.put(part("user", "Older prompt " + i)); parts.put(part("assistant", "Older answer " + i));
            turns.put(new JSONObject().put("id", "o" + i).put("status", "completed").put("part_start", i * 2).put("input_end", i * 2 + 1).put("part_end", i * 2 + 2));
        }
        JSONObject page = new JSONObject().put("id", "c").put("turns", turns).put("transcript", new JSONObject().put("parts", parts)).put("history", new JSONObject().put("has_more", false));
        NativeChatState.Merge merge = NativeChatState.prependTracked(chat, page);
        assertEquals(Set.of("o0", "o1"), merge.changedTurns); assertTrue(merge.reordered); assertFalse(merge.unchanged);
        assertEquals(5, merge.chat.getJSONArray("turns").length());
        assertEquals("t0", merge.chat.getJSONArray("turns").getJSONObject(2).getString("id"));
        assertSame(chat.getJSONObject("transcript").getJSONArray("parts").get(0), merge.chat.getJSONObject("transcript").getJSONArray("parts").get(4));
        NativeChatState.Merge titled = NativeChatState.eventTracked(chat, "metadata", new JSONObject().put("id", "c").put("updated_at", 10).put("title", "Renamed"));
        assertTrue(titled.changedTurns.isEmpty()); assertFalse(titled.unchanged); assertEquals("Renamed", titled.chat.getString("title"));
        NativeChatState.Merge identical = NativeChatState.eventTracked(chat, "metadata", new JSONObject().put("id", "c").put("updated_at", 10).put("title", "T"));
        assertTrue("Identical metadata is unchanged", identical.unchanged); assertTrue(identical.changedTurns.isEmpty());
        assertEquals(chat.toString(), NativeChatState.event(chat, "metadata", new JSONObject().put("id", "c").put("updated_at", 10).put("title", "T")).toString());
    }
}
