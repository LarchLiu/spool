package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** Production composer/catalog/dispatch contract, replacing only the Go transport. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityCommandsTest {
    private String response(int status, JSONObject body) {
        try { return new JSONObject().put("status", status).put("body", body.toString()).toString(); }
        catch (Exception error) { throw new AssertionError(error); }
    }
    private void commands(MainActivityGoalTest.Harness h, String json) throws Exception {
        h.<JSONObject>get("permissionCatalog").put("commands", new JSONArray(json)); h.call("updateComposer");
    }
    private JSONObject modes() throws Exception {
        return new JSONObject("{current:'ask',modes:[{id:'ask',available:true}],commands:[{name:'plan',kind:'control'}]}");
    }
    private void complete(MainActivityGoalTest.Harness h) {
        while (!h.io.jobs.isEmpty()) { h.io.jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    }

    private String emptyMessage(MainActivityGoalTest.Harness h) {
        assertTrue(h.composer().commands.menu.isShowing());
        assertTrue(h.composer().commands.options.isEmpty());
        return ((android.widget.TextView) h.composer().commands.menu.content.getChildAt(0)).getText().toString();
    }

    @Test public void missingServerCatalogShowsUpgradeMessageAndRecoversWhenDevServerProvidesCommands() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.<JSONArray>get("agents").getJSONObject(0).getJSONObject("catalog").remove("commands");
            h.composer().input.requestFocus(); h.composer().input.setText("/");
            JSONObject legacy = modes(); legacy.remove("commands");
            MainActivityGoalTest.ShadowClient.response = response(200, legacy);
            h.call("loadPermissions"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("Loading agent commands…", emptyMessage(h));
            complete(h);
            String upgrade = "This server does not provide an agent command catalog. Update Spool on the server.";
            assertEquals(upgrade, emptyMessage(h));
            MainActivityGoalTest.ShadowClient.calls.clear(); h.send("/plan");
            assertEquals(upgrade, h.<String>get("goalError"));
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());

            MainActivityGoalTest.ShadowClient.response = response(200, modes());
            h.call("loadPermissions"); complete(h); h.composer().input.setText("/");
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, h.composer().commands.options.size());
            assertEquals("Plan mode", h.composer().commands.options.get(0).title.getText().toString());
            commands(h, "[]"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals("No matching agent commands", emptyMessage(h));
        }
    }

    @Test public void discoveryFailureHasItsOwnMessageAndRetainsSavedCommandFallback() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject catalog = h.<JSONArray>get("agents").getJSONObject(0).getJSONObject("catalog");
            JSONArray saved = catalog.getJSONArray("commands"); catalog.remove("commands");
            h.composer().input.requestFocus(); h.composer().input.setText("/");
            MainActivityGoalTest.ShadowClient.response = response(503, new JSONObject().put("error", "Discovery unavailable"));
            h.call("loadPermissions"); complete(h);
            assertEquals("Could not load agent commands. Check project permissions and retry.", emptyMessage(h));
            catalog.put("commands", saved); h.call("updateComposer");
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(3, h.composer().commands.options.size());
        }
    }

    @Test public void projectCatalogOverridesSavedCommandsAndSwitchesWithTheAgent() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.composer().input.requestFocus(); h.composer().input.setText("/");
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(3, h.composer().commands.options.size());
            MainActivityGoalTest.ShadowClient.response = response(200, modes().put("commands", new JSONArray("[{name:'repo:check',displayName:'Project check',kind:'skill',description:'Verify tests',scope:'repo'}]")));
            h.call("loadPermissions"); complete(h);
            assertTrue(MainActivityGoalTest.ShadowClient.calls.get(0).get(1).contains("project_id=project-one&backend=codex&chat_id=chat-one"));
            MainActivityGoalTest.ShadowClient.calls.clear();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, h.composer().commands.options.size());
            assertEquals("Project check", h.composer().commands.options.get(0).title.getText().toString());
            assertEquals("Project", h.composer().commands.options.get(0).source.getText().toString());
            h.composer().commands.options.get(0).performClick();
            assertEquals("/repo:check ", h.composer().input.getText().toString());
            commands(h, "[]"); h.send("/repo:check");
            assertEquals("No matching agent commands", h.<String>get("goalError")); assertTrue(h.io.jobs.isEmpty());
            h.set("backend", "claude"); h.set("permissionCatalog", modes().put("commands", new JSONArray("[{name:'code-review',kind:'prompt',aliases:['review']}]")));
            h.set("agents", new JSONArray("[{backend:'claude',enabled:true,installed:true}]") );
            h.composer().input.setText("/rev"); h.call("updateComposer"); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, h.composer().commands.options.size());
            assertEquals("Code review", h.composer().commands.options.get(0).title.getText().toString());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void controlsUseCommandsEndpointAndPreserveNewerDraftsWithoutTurns() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            commands(h, "[{name:'plan',kind:'control',aliases:['p']}]");
            JSONObject original = h.get("chat");
            JSONObject result = new JSONObject().put("message", "Plan enabled").put("model", "new-model").put("reasoning_effort", "high").put("permission_mode", "ask");
            MainActivityGoalTest.ShadowClient.responder = call -> response(200, call.get(1).endsWith("/commands") ? result : uncheckedModes());
            h.send("/p"); assertTrue(h.<Boolean>get("busy")); assertNull(h.get("pendingSend"));
            h.composer().input.setText("A newer draft"); complete(h);
            List<String> call = MainActivityGoalTest.ShadowClient.calls.get(0);
            assertEquals("/api/chats/chat-one/commands", call.get(1)); assertEquals("/plan", new JSONObject(call.get(2)).getString("command"));
            assertEquals("test-csrf", call.get(3)); assertEquals("en", call.get(4));
            assertEquals("Plan enabled", h.<String>get("goalNotice")); assertEquals("new-model", h.<String>get("model"));
            assertEquals("high", h.<String>get("effort")); assertEquals("A newer draft", h.composer().input.getText().toString());
            assertSame(original, h.get("chat")); assertEquals(0, original.getJSONArray("turns").length()); assertFalse(h.<Boolean>get("busy"));
        }
    }

    private JSONObject uncheckedModes() {
        try { return modes(); } catch (Exception error) { throw new AssertionError(error); }
    }

    @Test public void aLocalControlCanCreateANewChatWithoutAnOptimisticTurn() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.ShadowClient.responder = call -> {
                try {
                    return response(200, call.get(1).equals("/api/chats") ? h.chat("created").put("project_id", "project-one")
                            : call.get(1).endsWith("/commands") ? new JSONObject("{message:'Plan enabled',model:'',reasoning_effort:'',permission_mode:'ask'}")
                            : call.get(1).contains("/permissions") ? modes() : h.chat("created").put("project_id", "project-one"));
                } catch (Exception error) { throw new AssertionError(error); }
            };
            h.send("/plan"); assertNull(h.get("pendingSend")); complete(h);
            assertEquals("created", h.<String>get("chatID")); assertEquals("Plan enabled", h.<String>get("goalNotice"));
            assertEquals("", h.composer().input.getText().toString()); assertEquals(0, h.<JSONObject>get("chat").getJSONArray("turns").length());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(1).endsWith("/messages")));
        }
    }

    @Test public void changingSelectionDuringCreationNeverDispatchesTheCommandInTheNextChat() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(false)) {
            MainActivityGoalTest.ShadowClient.response = response(200, h.chat("created").put("project_id", "project-one"));
            h.send("/plan"); h.io.jobs.remove(0).run();
            h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1); h.set("chatID", "chat-two"); h.set("chat", h.chat("chat-two"));
            h.composer().input.setText("Other draft"); Shadows.shadowOf(Looper.getMainLooper()).idle(); complete(h);
            assertEquals(1, MainActivityGoalTest.ShadowClient.calls.size()); assertEquals("/api/chats", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            assertEquals("chat-two", h.<String>get("chatID")); assertEquals("Other draft", h.composer().input.getText().toString());
            assertEquals("", h.<String>get("goalNotice")); assertFalse(h.<Boolean>get("busy"));
        }
    }

    @Test public void promptsSkillsAndPlanTasksRetainTheStreamingMessageRoute() throws Exception {
        for (String text : new String[] {"/review main", "/plan fix tests", "/repo:check verify"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            commands(h, "[{name:'review',kind:'prompt'},{name:'plan',kind:'control'},{name:'repo:check',kind:'skill'}]");
            MainActivityGoalTest.ShadowClient.response = response(409, new JSONObject().put("error", "Fixture rejection"));
            h.send(text); assertNotNull(h.get("pendingSend")); complete(h);
            List<List<String>> messages = MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).endsWith("/messages")).toList();
            assertEquals(1, messages.size());
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(1).endsWith("/commands")));
            List<String> call = messages.get(0);
            assertEquals("/api/chats/chat-one/messages", call.get(1)); assertEquals(text, new JSONObject(call.get(2)).getString("message"));
            assertEquals(text, h.composer().input.getText().toString());
        }
    }

    @Test public void staleResponsesRevokedSessionsAndErrorsCannotMutateTheNextSelection() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.send("/plan"); h.set("selectionEpoch", h.<Integer>get("selectionEpoch") + 1);
            h.set("chatID", "chat-two"); h.set("chat", h.chat("chat-two")); h.composer().input.setText("Other draft"); complete(h);
            assertEquals("", h.<String>get("goalNotice")); assertEquals("Other draft", h.composer().input.getText().toString());
            MainActivityGoalTest.ShadowClient.response = response(409, new JSONObject().put("error", "Plan unavailable"));
            h.send("/plan"); complete(h); assertEquals("Plan unavailable", h.<String>get("goalError"));
            assertEquals("/plan", h.composer().input.getText().toString()); assertNotNull(h.composer().commands.error);
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.send("/plan"); h.call("clearWorkspace"); complete(h);
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty()); assertNull(h.get("user")); assertFalse(h.<Boolean>get("busy"));
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            MainActivityGoalTest.ShadowClient.response = response(401, new JSONObject().put("error", "Expired"));
            h.send("/plan"); complete(h); assertNull(h.get("user")); assertFalse(h.<Boolean>get("busy"));
            assertEquals("", h.<String>get("goalNotice"));
        }
    }

    @Test public void slashCommandsCannotBeQueuedSteeredOrSentWithFilesAndPathsStayOrdinary() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.<JSONObject>get("chat").put("status", "running").put("turns", new JSONArray("[{id:'live',status:'running',part_start:0,part_end:0}]"));
            h.send("/review");
            assertEquals("Wait for the running response before using agent commands", h.<String>get("goalError")); assertTrue(h.io.jobs.isEmpty());
            h.call("updateComposer"); assertFalse(h.composer().commands.menu.isShowing());
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.send("/unknown"); assertEquals("No matching agent commands", h.<String>get("goalError")); assertTrue(h.io.jobs.isEmpty());
            MainActivityGoalTest.ShadowClient.response = response(409, new JSONObject().put("error", "Fixture rejection"));
            h.send("/tmp/file"); complete(h);
            assertEquals("/tmp/file", new JSONObject(MainActivityGoalTest.ShadowClient.calls.get(0).get(2)).getString("message"));
        }
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
            Object file = org.robolectric.util.ReflectionHelpers.callConstructor(type,
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(String.class, "note.txt"),
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(String.class, "text/plain"),
                    org.robolectric.util.ReflectionHelpers.ClassParameter.from(byte[].class, new byte[] {65}));
            Object draft = org.robolectric.util.ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            List<Object> files = org.robolectric.util.ReflectionHelpers.getField(draft, "files"); files.add(file);
            h.send("/review"); assertEquals("Agent commands do not accept attachments", h.<String>get("goalError"));
            assertTrue(h.io.jobs.isEmpty()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void clientCommandsUseConversationActionsAndValidateArguments() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            commands(h, "[{name:'rename',kind:'client'},{name:'clear',kind:'client'},{name:'resume',kind:'client'}]");
            h.send("/rename"); assertEquals("Enter a conversation name", h.<String>get("goalError")); assertTrue(h.io.jobs.isEmpty());
            h.send("/resume missing"); assertEquals("Conversation not found", h.<String>get("goalError"));
            MainActivityGoalTest.ShadowClient.response = response(200, new JSONObject("{id:'chat-one',title:'Updated title'}"));
            h.send("/rename Updated title"); complete(h);
            assertEquals("/api/chats/chat-one/rename", MainActivityGoalTest.ShadowClient.calls.get(0).get(1));
            assertEquals("Updated title", h.<JSONObject>get("chat").getString("title")); assertEquals("", h.composer().input.getText().toString());
            h.send("/clear unexpected"); assertEquals("Usage: /clear", h.<String>get("goalError"));
        }
    }
}
