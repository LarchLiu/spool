package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.nio.charset.StandardCharsets;
import org.junit.Test;

public class NativeGoalCommandTest {
    @Test public void commandsAndSuggestionsFollowTheActualWebParserIncludingUnicode() throws Exception {
        JsonArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            cases = JsonParser.parseString(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getAsJsonObject().getAsJsonArray("slashCommands");
        }
        for (int i = 0; i < cases.size(); i++) {
            JsonObject sample = cases.get(i).getAsJsonObject();
            String text = sample.get("text").getAsString();
            assertEquals("suggestion case " + i, sample.get("suggestion").getAsBoolean(), NativeGoalCommand.suggestion(text));
            NativeGoalCommand command = NativeGoalCommand.parse(text);
            if (sample.get("command").isJsonNull()) assertNull(command);
            else {
                assertNotNull(command); JsonObject expected = sample.getAsJsonObject("command");
                assertEquals(expected.get("action").getAsString(), command.action);
                assertEquals(expected.has("objective") ? expected.get("objective").getAsString() : "", command.objective);
                assertEquals("Goal commands do not accept attachments", command.validationError(true));
                assertEquals(command.objective.codePointCount(0, command.objective.length()) > 4000
                        ? "Goal conditions must be at most 4000 characters" : "", command.validationError(false));
            }
        }
    }

    @Test public void lateGoalRepliesCannotReplaceADifferentSessionSelectionOrEditedDraft() {
        NativeGoalRequest request = new NativeGoalRequest(4, 7, "chat-one", "/goal clear");
        assertTrue(request.current(4, 7, "chat-one"));
        assertFalse(request.current(5, 7, "chat-one")); assertFalse(request.current(4, 8, "chat-one"));
        assertFalse(request.current(4, 7, "chat-two"));
        assertTrue(request.clears("/goal clear")); assertFalse(request.clears("A newer draft"));
    }
}
