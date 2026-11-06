package com.cloudgeek.spool.android;

import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;

/** Same chronological input/response boundaries as Web turnExchanges. */
final class NativeTurnExchange {
    final int start;
    int inputEnd, end;
    private NativeTurnExchange(int start) { this.start = start; inputEnd = end = start; }

    static List<NativeTurnExchange> split(JSONArray parts, int start, int end) {
        start = Math.max(0, start); end = Math.max(start, Math.min(parts.length(), end));
        List<NativeTurnExchange> exchanges = new ArrayList<>();
        NativeTurnExchange current = new NativeTurnExchange(start);
        exchanges.add(current);
        boolean response = false; String inputID = "";
        for (int i = start; i < end; i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part == null) continue;
            if (part.optString("role").equals("user")) {
                String id = part.optString("id");
                if (response || !inputID.isEmpty() && !id.isEmpty() && !inputID.equals(id)) {
                    current = new NativeTurnExchange(i); exchanges.add(current);
                    response = false; inputID = "";
                }
                current.inputEnd = i + 1;
                if (!id.isEmpty()) inputID = id;
            } else response = true;
            current.end = i + 1;
        }
        return exchanges;
    }

    static JSONObject completedSegment(JSONObject turn) {
        JSONObject segment = new JSONObject();
        try {
            java.util.Iterator<String> keys = turn.keys();
            while (keys.hasNext()) { String key = keys.next(); segment.put(key, turn.opt(key)); }
            segment.put("status", "completed").put("compacting", false);
            segment.remove("finished_at");
        } catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        return segment;
    }
}
