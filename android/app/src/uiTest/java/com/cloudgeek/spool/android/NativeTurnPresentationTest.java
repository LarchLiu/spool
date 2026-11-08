package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeTurnPresentationTest {
    private JSONObject part(String role, String text) throws Exception {
        return new JSONObject().put("role", role).put("kind", "text").put("text", text);
    }
    private JSONObject turn(String id, int start, int end) throws Exception {
        return new JSONObject().put("id", id).put("part_start", start).put("part_end", end).put("status", "completed");
    }
    private JSONObject page(JSONArray parts, JSONArray turns) throws Exception {
        return new JSONObject().put("transcript", new JSONObject().put("parts", parts)).put("turns", turns);
    }

    @Test public void unchangedTurnsReuseTheirPresentationWithoutReadingTheirParts() throws Exception {
        class CountedParts extends JSONArray {
            int reads;
            @Override public Object opt(int index) { reads++; return super.opt(index); }
        }
        CountedParts parts = new CountedParts(); JSONArray turns = new JSONArray();
        for (int i = 0; i < 500; i++) {
            parts.put(part("user", "Question " + i)).put(part("assistant", "Answer " + i));
            turns.put(turn("turn-" + i, 2 * i, 2 * i + 2));
        }
        JSONObject page = page(parts, turns); NativeTurnPresentation.Cache cache = new NativeTurnPresentation.Cache();
        List<NativeTurnPresentation> before = new ArrayList<>(cache.update("chat", page, null).values());
        assertEquals(500, cache.builds); parts.reads = 0;
        parts.getJSONObject(999).put("text", "Updated answer"); parts.reads = 0;
        List<NativeTurnPresentation> after = new ArrayList<>(cache.update("chat", page, Set.of("turn-499")).values());
        assertEquals(501, cache.builds); assertEquals("Only the changed turn's local range is read", 2, parts.reads);
        for (int i = 0; i < 499; i++) assertSame(before.get(i), after.get(i));
        assertEquals("Updated answer", after.get(499).answer);
        cache.update("chat", page, Set.of()); assertEquals(501, cache.builds); assertEquals(2, parts.reads);
    }

    @Test public void prependedHistoryKeepsCachedInputsAndFilesInTurnLocalCoordinates() throws Exception {
        JSONArray recentParts = new JSONArray().put(part("user", "Recent question"))
                .put(new JSONObject("{role:'user',kind:'context',name:'recent.txt',file_path:'/recent.txt'}"))
                .put(part("assistant", "Recent answer"));
        NativeTurnPresentation.Cache cache = new NativeTurnPresentation.Cache();
        NativeTurnPresentation recent = cache.update("chat", page(recentParts, new JSONArray().put(turn("recent", 0, 3))), null).values().iterator().next();
        JSONArray all = new JSONArray().put(part("user", "Older")).put(part("assistant", "Old answer"));
        for (int i = 0; i < recentParts.length(); i++) all.put(recentParts.get(i));
        List<NativeTurnPresentation> values = new ArrayList<>(cache.update("chat", page(all,
                new JSONArray().put(turn("older", 0, 2)).put(turn("recent", 2, 5))), Set.of("older")).values());
        assertSame(recent, values.get(1)); assertEquals(2, recent.inputEnd);
        assertEquals("recent.txt", recent.parts.getJSONObject(1).getString("name")); assertEquals("Recent answer", recent.answer);
        assertEquals(2, cache.builds);
    }

    @Test public void steeringInputsEarlierAnswersActionsAndStatusShareOneChronology() throws Exception {
        JSONArray parts = new JSONArray().put(part("user", "Initial").put("timestamp", 1000))
                .put(part("assistant", "Earlier answer").put("phase", "final_answer"))
                .put(part("user", "Steer").put("timestamp", 2000))
                .put(new JSONObject("{role:'user',kind:'context',name:'notes.txt'}"))
                .put(new JSONObject("{role:'assistant',kind:'tool_use',name:'read_file'}"))
                .put(part("assistant", "Final ").put("phase", "final_answer").put("id", "answer"))
                .put(part("assistant", "answer.").put("phase", "final_answer").put("id", "answer"))
                .put(part("assistant", "Another paragraph.").put("phase", "final_answer").put("id", "next"));
        JSONObject turn = turn("turn", 0, parts.length()).put("started_at", 500).put("status", "running")
                .put("actions", new JSONArray("[{kind:'stop'}]")).put("attachments", new JSONArray("[{name:'initial.txt'}]"))
                .put("pending_approval", new JSONObject("{id:'approval'}")).put("error", "Failure");
        NativeTurnPresentation value = NativeTurnPresentation.turn(parts, turn);
        assertEquals("Initial", value.input); assertEquals(1000, value.inputTimestamp); assertEquals(1, value.inputEnd);
        assertEquals(4, value.process.size()); assertEquals("Earlier answer", value.process.get(0).getString("text"));
        assertEquals("Steer", value.process.get(1).getString("text")); assertEquals("notes.txt", value.process.get(2).getString("name"));
        assertEquals("Final answer.\n\nAnother paragraph.", value.answer); assertTrue(value.running); assertTrue(value.answered);
        assertSame(turn.getJSONArray("actions"), value.actions); assertSame(turn.getJSONObject("pending_approval"), value.approval);
        assertEquals("Failure", value.error); assertEquals("initial.txt", value.attachments.getJSONObject(0).getString("name"));
        turn.put("status", "stopped"); assertEquals("", NativeTurnPresentation.turn(parts, turn).error);
    }

    @Test public void unphasedOutputBecomesAnAnswerOnlyAfterActivityCompletes() throws Exception {
        JSONArray parts = new JSONArray().put(part("user", "Question")).put(part("assistant", "Progress"))
                .put(new JSONObject("{role:'assistant',kind:'tool_use'}")).put(part("assistant", "Result"));
        JSONObject turn = turn("turn", 0, parts.length()).put("status", "running");
        NativeTurnPresentation running = NativeTurnPresentation.turn(parts, turn);
        assertFalse(running.answered); assertEquals(3, running.process.size());
        turn.put("status", "completed"); NativeTurnPresentation completed = NativeTurnPresentation.turn(parts, turn);
        assertEquals("Result", completed.answer); assertEquals(2, completed.process.size());
        parts.put(part("user", "One more request")); turn.put("part_end", parts.length());
        assertFalse(NativeTurnPresentation.turn(parts, turn).answered);
    }

    @Test public void duplicateIdsRetirementAndSessionChangesDoNotAliasCachedPresentations() throws Exception {
        JSONArray parts = new JSONArray().put(part("assistant", "One")).put(part("assistant", "Two"));
        JSONObject page = page(parts, new JSONArray().put(turn("same", 0, 1)).put(turn("same", 1, 2)));
        NativeTurnPresentation.Cache cache = new NativeTurnPresentation.Cache();
        Map<String, NativeTurnPresentation> before = cache.update("session-a", page, null);
        assertEquals(2, before.size());
        assertEquals(List.of("One", "Two"), before.values().stream().map(v -> v.answer).collect(java.util.stream.Collectors.toList()));
        NativeTurnPresentation first = before.values().iterator().next();
        page.getJSONArray("turns").remove(1);
        assertEquals(1, cache.update("session-a", page, Set.of()).size());
        assertNotSame(first, cache.update("session-b", page, Set.of()).values().iterator().next());
        assertTrue(cache.update("session-b", null, Set.of()).isEmpty());
    }
}
