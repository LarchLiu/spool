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
        boolean response = false; String inputID = ""; long inputTime = 0;
        for (int i = start; i < end; i++) {
            JSONObject part = parts.optJSONObject(i);
            if (part == null) continue;
            if (part.optString("role").equals("user")) {
                String id = part.optString("id");
                if (response || !inputID.isEmpty() && !id.isEmpty() && !inputID.equals(id)
                        || inputTime > 0 && part.optLong("timestamp") > 0 && inputTime != part.optLong("timestamp")) {
                    current = new NativeTurnExchange(i); exchanges.add(current);
                    response = false; inputID = ""; inputTime = 0;
                }
                current.inputEnd = i + 1;
                if (!id.isEmpty()) inputID = id;
                if (part.optLong("timestamp") > 0) inputTime = part.optLong("timestamp");
            } else response = true;
            current.end = i + 1;
        }
        return exchanges;
    }
}
