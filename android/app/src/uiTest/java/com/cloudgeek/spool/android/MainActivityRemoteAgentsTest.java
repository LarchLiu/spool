package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Real native composer and API boundary; no real account, server or model is used. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityRemoteAgentsTest {
    private static JSONObject json(Object... pairs) { return NativeRemoteAgents.json(pairs); }
    private static String envelope(int status, Object value) { return json("status", status, "body", value.toString()).toString(); }
    private static JSONArray targets() { return new JSONArray().put(json("id", "server-b", "name", "Trusted B")); }
    private static final class Fixture {
        boolean trusted = true, failDelegate, failServers, projectWritable = true, bothAgents, historyOnly;
        JSONObject task;
        final List<JSONObject> requests = new ArrayList<>();
        String respond(List<String> call) {
            try {
                if (call.get(1).equals("/api/chats")) return envelope(200, chat("created-chat"));
                if (call.get(1).endsWith("/messages")) return envelope(202, chat("chat-one"));
                if (call.get(1).startsWith("/api/permissions")) return envelope(200, json("modes", new JSONArray().put(json("id", "ask", "available", true))));
                if (!call.get(1).equals("/api/federation/agents")) return envelope(200, new JSONArray());
                JSONObject input = new JSONObject(call.get(2)); requests.add(input);
                switch (input.optString("operation")) {
                    case "servers":
                        if (failServers) return envelope(503, json("error", "Server list unavailable"));
                        return envelope(200, trusted ? targets() : new JSONArray());
                    case "projects": return envelope(200, new JSONArray().put(json("id", "read-only", "name", "Read project", "can_create", false))
                            .put(json("id", "project-b", "name", "Build project", "can_create", projectWritable, "summary", json("text", "Remote purpose"))));
                    case "agents":
                        JSONArray agents = new JSONArray().put(json("backend", "codex", "enabled", true, "installed", true,
                            "summary", json("text", "Remote agent guidance"), "catalog", json("defaultModel", "remote-model", "models", new JSONArray().put(json("model", "remote-model",
                                    "displayName", "Remote Model", "summary", json("text", "Remote model guidance"), "defaultReasoningEffort", "low",
                                    "supportedReasoningEfforts", new JSONArray().put(json("reasoningEffort", "low")).put(json("reasoningEffort", "high")))))));
                        if (bothAgents) {
                            agents.getJSONObject(0).getJSONObject("catalog").getJSONArray("models").put(json("model", "other-remote-model", "displayName", "Other Remote Model"));
                            agents.put(json("backend", "claude", "enabled", true, "installed", true, "catalog", json("defaultModel", "claude-remote-model",
                                    "models", new JSONArray().put(json("model", "claude-remote-model", "displayName", "Claude Remote Model")))));
                        }
                        return envelope(200, agents);
                    case "delegate":
                        if (failDelegate) { failDelegate = false; return envelope(502, json("error", "Temporary remote failure")); }
                        task = json("task_id", "remote-task", "request_id", input.optString("request_id"), "conversation_id", "remote-conversation", "turn_id", "remote-turn",
                                "project_id", "project-b", "project_name", "Build project", "agent", input.optString("agent"), "model", input.optString("model"),
                                "reasoning_effort", input.optString("reasoning_effort"), "status", "awaiting_approval", "progress", "Working remotely",
                                "text", "Unfinished text must not enter context", "pending_approval", json("id", "remote-approval", "title", "Remote approval",
                                        "fields", new JSONArray().put(json("name", "confirm", "label", "Confirm remote action", "type", "boolean"))));
                        return envelope(200, task);
                    case "calls": return envelope(200, task == null ? new JSONArray() : new JSONArray().put(json("server", "server-b", "server_name", "Original PC", "history_only", historyOnly, "task", task)));
                    case "task": return envelope(200, task);
                    case "approve":
                        assertEquals("remote-turn", input.getJSONObject("approval").getString("turn_id"));
                        assertTrue(input.getJSONObject("approval").getJSONObject("content").getBoolean("confirm"));
                        task.put("status", "completed").put("text", "**Completed remote answer**"); task.remove("pending_approval"); task.remove("progress");
                        return envelope(200, json("ok", true));
                    case "cancel": task.put("status", "stopped"); task.remove("pending_approval"); return envelope(200, task);
                    case "history": return envelope(200, json("turns", new JSONArray().put(json("messages", new JSONArray().put(json("role", "assistant", "text",
                            input.optString("before").isEmpty() ? "Latest remote history" : "Earlier remote history")))), "next", input.optString("before").isEmpty() ? "older" : ""));
                    default: throw new AssertionError(input);
                }
            } catch (org.json.JSONException error) { throw new AssertionError(error); }
        }
        static JSONObject chat(String id) { return json("id", id, "project_id", "project-one", "backend", "codex", "can_control", true, "status", "completed", "turns", new JSONArray()); }
    }
    private void prepare(MainActivityGoalTest.Harness h, Fixture fixture) {
        MainActivityGoalTest.ShadowClient.responder = fixture::respond;
        h.set("federationTargets", fixture.trusted ? targets() : new JSONArray()); h.call("updateComposer");
        NativeMarkwonHistoryTest.layout(h.get("root"));
        h.composer().input.requestFocus(); idle();
    }
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private void drain(MainActivityGoalTest.Harness h) {
        int remaining = 12;
        while (!h.io.jobs.isEmpty()) { assertTrue("Bounded API work", --remaining > 0); h.io.jobs.remove(0).run(); idle(); }
    }
    private void open(MainActivityGoalTest.Harness h, String draft) {
        h.composer().input.requestFocus(); h.composer().input.setText(draft); idle();
        assertTrue("Native suggestion popup is visible for " + draft, h.composer().commands.menu.isShowing());
    }
    private NativeSlashCommandMenu row(MainActivityGoalTest.Harness h, String title) {
        for (NativeSlashCommandMenu row : h.composer().commands.options) if (row.title.getText().toString().equals(title)) return row;
        throw new AssertionError("Missing row " + title);
    }
    private void key(MainActivityGoalTest.Harness h, int code) {
        h.composer().input.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, code));
        h.composer().input.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_UP, code)); idle();
    }
    private void chooseProject(MainActivityGoalTest.Harness h, String project) {
        open(h, "Analyze @Trust"); h.io.run();
        row(h, "Trusted B").performClick(); h.io.run();
        row(h, project).performClick(); h.io.run();
        assertEquals("Analyze", h.composer().input.getText().toString());
    }
    private View button(View view, String name) {
        if (view instanceof TextView && ((TextView) view).getText().toString().equals(name) && view.isClickable()) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            View result = button(((ViewGroup) view).getChildAt(i), name); if (result != null) return result;
        }
        return null;
    }
    private View description(View view, String name) {
        if (name.contentEquals(view.getContentDescription() == null ? "" : view.getContentDescription())) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            View result = description(((ViewGroup) view).getChildAt(i), name); if (result != null) return result;
        }
        return null;
    }
    private NativeDialog dialog(MainActivityGoalTest.Harness h) { return ReflectionHelpers.getField(h.get("remoteAgents"), "dialog"); }

    @Test public void mcpIsDisabledWithoutTrustAndOptInTravelsOnlyInMetadata() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); fixture.trusted = false; prepare(h, fixture);
            open(h, "/"); NativeSlashCommandMenu root = row(h, "mcp");
            assertEquals("Choose MCP tools for this chat", root.detail.getText().toString());
            root.performClick(); h.io.run();
            NativeSlashCommandMenu unavailable = row(h, "Remote agents"); assertFalse(unavailable.isEnabled());
            screenshot(h, "mcp-disabled");
            assertEquals("No trusted servers available", unavailable.detail.getText().toString());
            unavailable.performClick(); key(h, KeyEvent.KEYCODE_ENTER);
            assertEquals("/mcp ", h.composer().input.getText().toString());
            assertNull(description(h.composer(), "Remove remote agent MCP"));
            fixture.trusted = true; h.set("federationTargets", targets()); h.call("updateComposer");
            key(h, KeyEvent.KEYCODE_DPAD_LEFT); row(h, "mcp").performClick(); h.io.run();
            key(h, KeyEvent.KEYCODE_DPAD_RIGHT);
            assertEquals("", h.composer().input.getText().toString());
            assertNotNull(description(h.composer(), "Remove remote agent MCP"));
            assertEquals(0, MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).endsWith("/messages")).count());
            h.send("Local task"); h.io.run();
            List<String> request = MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).endsWith("/messages")).findFirst().orElseThrow();
            JSONObject input = new JSONObject(request.get(2)); assertEquals("Local task", input.getString("message"));
            assertEquals("spool_federation", input.getJSONArray("mcp_servers").getString(0));
            h.set("chat", Fixture.chat("chat-one")); h.call("renderChat");
            description(h.composer(), "Remove remote agent MCP").performClick();
            assertEquals(0, ((NativeRemoteAgents) h.get("remoteAgents")).mcpServers().length());
            open(h, "/mcp"); key(h, KeyEvent.KEYCODE_ENTER);
            ReflectionHelpers.callInstanceMethod(h.activity, "newChat", ReflectionHelpers.ClassParameter.from(String.class, "project-one"));
            assertEquals(0, ((NativeRemoteAgents) h.get("remoteAgents")).mcpServers().length());
            assertNull(description(h.composer(), "Remove remote agent MCP"));
        }
    }

    @Test public void mentionSelectsAuthorizedProjectPreservesTextAndDelegatesWithoutLocalAgent() throws Exception {
        for (boolean existing : new boolean[] {true, false}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(existing)) {
            Fixture fixture = new Fixture(); prepare(h, fixture);
            open(h, "Analyze @Trust"); h.io.run();
            screenshot(h, "mention-servers");
            assertTrue(h.composer().input.hasFocus());
            row(h, "Trusted B").performClick(); h.io.run();
            screenshot(h, "mention-projects");
            key(h, KeyEvent.KEYCODE_DPAD_LEFT); row(h, "Trusted B").performClick(); h.io.run();
            assertTrue(row(h, "Read project").detail.getText().toString().contains("Read-only"));
            assertFalse(row(h, "Read project").isEnabled());
            row(h, "Read project").performClick(); key(h, KeyEvent.KEYCODE_ENTER); key(h, KeyEvent.KEYCODE_TAB);
            assertEquals("Analyze @", h.composer().input.getText().toString());
            assertNull(description(h.composer(), "Remove remote target"));
            assertTrue(h.composer().commands.menu.isShowing());
            assertTrue(h.io.jobs.isEmpty());
            assertTrue(fixture.requests.stream().noneMatch(input -> input.optString("operation").equals("agents")));
            row(h, "Build project").performClick(); h.io.run();
            assertEquals("Analyze", h.composer().input.getText().toString()); assertTrue(h.composer().send.isEnabled());
            assertNull(description(h.composer(), "Remove remote agent MCP"));
            h.composer().model.performClick();
            NativePopover popup = ((NativeUi) h.get("ui")).activePopover;
            assertNotNull(allText(popup.content), description(popup.content, "Remote Model"));
            popup.dismiss();
            fixture.failDelegate = true; h.send("Inspect remote project"); drain(h);
            assertEquals("Inspect remote project", h.composer().input.getText().toString());
            h.call("sendOrStop"); h.io.run();
            List<JSONObject> sent = fixture.requests.stream().filter(input -> input.optString("operation").equals("delegate")).toList();
            assertEquals(2, sent.size()); assertEquals(sent.get(0).getString("request_id"), sent.get(1).getString("request_id"));
            JSONObject input = sent.get(1);
            assertEquals(existing ? "chat-one" : "created-chat", input.getString("origin_chat"));
            assertEquals("server-b", input.getString("server")); assertEquals("project-b", input.getString("project"));
            assertEquals("Inspect remote project", input.getString("message")); assertEquals("remote-model", input.getString("model"));
            assertEquals("low", input.getString("reasoning_effort")); assertFalse(input.has("mcp_servers"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(1).endsWith("/messages")));
            assertEquals("", h.composer().input.getText().toString());
            ((NativeRemoteAgents) h.get("remoteAgents")).reset();
            assertFalse(((NativeRemoteAgents) h.get("remoteAgents")).targeted());
        }
    }

    @Test public void mcpSubmenuListsEntriesAndKeepsSelectionsIndependent() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture);
            open(h, "/mcp"); h.io.run();
            assertEquals("One built-in MCP plus Back", 2, h.composer().commands.options.size());
            NativeRemoteAgents original = h.get("remoteAgents");
            NativeRemoteAgents.Host host = ReflectionHelpers.getField(original, "host");
            original.reset();
            h.composer().box.removeView(ReflectionHelpers.getField(original, "controls"));
            List<NativeRemoteAgents.MCPEntry> catalog = new ArrayList<>(NativeRemoteAgents.BUILTIN_MCPS);
            catalog.add(new NativeRemoteAgents.MCPEntry("fixture_search", "Fixture search", "Second MCP fixture", "Unavailable",
                    "Remove fixture MCP", "fixture", false));
            NativeRemoteAgents remote = new NativeRemoteAgents(host, catalog);
            h.set("remoteAgents", remote); remote.attach(h.get("ui"), h.composer()); h.call("updateComposer");
            open(h, "/mcp"); drain(h);
            assertEquals(3, h.composer().commands.options.size());
            key(h, KeyEvent.KEYCODE_ENTER);
            assertNotNull(description(h.composer(), "Remove remote agent MCP"));
            open(h, "/mcp"); key(h, KeyEvent.KEYCODE_DPAD_DOWN); key(h, KeyEvent.KEYCODE_ENTER);
            assertNotNull(description(h.composer(), "Remove fixture MCP"));
            assertEquals("[\"spool_federation\",\"fixture_search\"]", remote.mcpServers().toString());
            h.send("Local task with two MCPs"); h.io.run();
            List<String> sent = MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).endsWith("/messages")).findFirst().orElseThrow();
            assertEquals(remote.mcpServers().toString(), new JSONObject(sent.get(2)).getJSONArray("mcp_servers").toString());
            h.set("chat", Fixture.chat("chat-one")); h.call("renderChat");
            description(h.composer(), "Remove remote agent MCP").performClick();
            assertEquals("[\"fixture_search\"]", remote.mcpServers().toString());
            open(h, "/mcp fixture"); assertEquals(2, h.composer().commands.options.size());
            key(h, KeyEvent.KEYCODE_TAB); assertEquals(0, remote.mcpServers().length());
            assertNull(description(h.composer(), "Remove fixture MCP"));
        }
    }

    @Test public void readOnlyProjectCannotResumeAnExistingRemoteConversation() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); fixture.projectWritable = false;
            fixture.task = json("task_id", "mcp-task", "project_id", "project-b", "project_name", "Build project",
                    "conversation_id", "mcp-conversation", "agent", "codex", "status", "completed", "text", "MCP final answer");
            prepare(h, fixture);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1500)); drain(h);
            h.composer().input.setText("Keep this draft");
            button(h.composer(), "Remote agents · 1 · Completed").performClick();
            button(dialog(h).body, "@Trusted B/Build project · Completed").performClick();
            button(dialog(h).body, "Continue conversation").performClick(); h.io.run();
            assertFalse(((NativeRemoteAgents) h.get("remoteAgents")).targeted());
            assertEquals("Keep this draft", h.composer().input.getText().toString());
            assertTrue(h.io.jobs.isEmpty());
            assertTrue(fixture.requests.stream().noneMatch(input -> input.optString("operation").equals("agents") || input.optString("operation").equals("delegate")));
            assertNotNull(button(dialog(h).body, "Remote history"));
            fixture.projectWritable = true;
            button(dialog(h).body, "Continue conversation").performClick(); h.io.run(); h.io.run();
            assertTrue(((NativeRemoteAgents) h.get("remoteAgents")).ready());
            assertEquals("Keep this draft", h.composer().input.getText().toString());
        }
    }

    @Test public void remoteResultsApprovalsHistoryAndContinuationUseTheNativeTaskPanel() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture); chooseProject(h, "Build project");
            h.send("Work remotely"); h.io.run();
            View calls = button(h.composer(), "Remote agents · 1 · Approval required"); assertNotNull(calls); calls.performClick();
            button(dialog(h).body, "@Trusted B/Build project · Approval required").performClick();
            assertNull(button(dialog(h).body, "Use result as context"));
            assertNotNull(button(dialog(h).body, "Remote history"));
            android.widget.CompoundButton confirm = checkbox(dialog(h).body); assertNotNull(confirm); confirm.setChecked(true);
            fixture.task.put("updated_at", 1000);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1500)); drain(h);
            assertSame("Status polling must preserve the human's approval form", confirm, checkbox(dialog(h).body));
            assertTrue(confirm.isChecked());
            View allow = button(dialog(h).body, "Allow once"); assertNotNull(allow); allow.performClick(); h.io.run(); h.io.run();
            assertNotNull(button(dialog(h).body, "Use result as context"));
            button(dialog(h).body, "Remote history").performClick(); h.io.run();
            assertTrue(allText(dialog(h).body).contains("Latest remote history"));
            button(dialog(h).body, "Load older messages").performClick(); h.io.run();
            assertTrue(allText(dialog(h).body).contains("Earlier remote history")); dialog(h).dismiss();
            button(h.composer(), "Remote agents · 1 · Completed").performClick();
            button(dialog(h).body, "@Trusted B/Build project · Completed").performClick();
            button(dialog(h).body, "Use result as context").performClick();
            JSONObject context = new JSONObject(h.composer().input.getText().toString());
            assertEquals("**Completed remote answer**", context.getString("reference_output"));
            assertFalse(context.toString().contains("Working remotely")); assertFalse(((NativeRemoteAgents) h.get("remoteAgents")).targeted());
            button(h.composer(), "Remote agents · 1 · Completed").performClick();
            button(dialog(h).body, "@Trusted B/Build project · Completed").performClick();
            button(dialog(h).body, "Continue conversation").performClick(); h.io.run(); h.io.run();
            h.send("Follow up"); h.io.run();
            assertEquals("remote-conversation", fixture.requests.stream().filter(input -> input.optString("operation").equals("delegate")).reduce((first, last) -> last).orElseThrow().getString("conversation_id"));
            button(h.composer(), "Remote agents · 1 · Approval required").performClick();
            button(dialog(h).body, "@Trusted B/Build project · Approval required").performClick();
            button(dialog(h).body, "Stop response").performClick(); h.io.run();
            assertNull(button(dialog(h).body, "Use result as context"));
        }
    }

    @Test public void continuingRemoteConversationKeepsItsAgentUntilStartingANewConversation() throws Exception {
        for (boolean resume : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); fixture.bothAgents = true;
            if (resume) fixture.task = json("task_id", "remote-task", "project_id", "project-b", "project_name", "Build project",
                    "conversation_id", "remote-conversation", "agent", "codex", "model", "remote-model", "status", "completed", "text", "Prior result");
            prepare(h, fixture);
            if (resume) {
                Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1500)); drain(h);
                button(h.composer(), "Remote agents · 1 · Completed").performClick();
                button(dialog(h).body, "@Trusted B/Build project · Completed").performClick();
                button(dialog(h).body, "Continue conversation").performClick(); h.io.run(); h.io.run();
            } else {
                chooseProject(h, "Build project");
                h.composer().model.performClick();
                NativePopover allAgents = ((NativeUi) h.get("ui")).activePopover;
                assertNotNull(description(allAgents.content, "Claude Remote Model")); allAgents.dismiss();
                h.send("Initial remote task"); h.io.run();
            }
            h.composer().model.performClick();
            NativePopover menu = ((NativeUi) h.get("ui")).activePopover;
            assertNull("A continued Codex conversation cannot select Claude", description(menu.content, "Claude Remote Model"));
            View model = description(menu.content, "Other Remote Model");
            assertNotNull("Models within the conversation's Agent stay available", model); model.performClick(); menu.dismiss();
            h.send("Continue with another model"); h.io.run();
            JSONObject continued = fixture.requests.stream().filter(input -> input.optString("operation").equals("delegate")).reduce((first, last) -> last).orElseThrow();
            assertEquals("remote-conversation", continued.getString("conversation_id"));
            assertEquals("codex", continued.getString("agent")); assertEquals("other-remote-model", continued.getString("model"));
            View fresh = button(h.composer(), "New remote conversation"); assertNotNull(fresh); fresh.performClick();
            h.composer().model.performClick(); menu = ((NativeUi) h.get("ui")).activePopover;
            View claude = description(menu.content, "Claude Remote Model"); assertNotNull(claude); claude.performClick(); menu.dismiss();
            h.send("New task for Claude"); h.io.run();
            JSONObject created = fixture.requests.stream().filter(input -> input.optString("operation").equals("delegate")).reduce((first, last) -> last).orElseThrow();
            assertEquals("", created.getString("conversation_id")); assertEquals("claude", created.getString("agent"));
        }
    }

    @Test public void lateProjectRepliesCannotRestoreTargetsAfterNavigation() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture); open(h, "@Trust"); h.io.run();
            row(h, "Trusted B").performClick(); assertEquals(1, h.io.jobs.size());
            Runnable delayed = h.io.jobs.remove(0);
            ReflectionHelpers.callInstanceMethod(h.activity, "newChat", ReflectionHelpers.ClassParameter.from(String.class, "project-one"));
            delayed.run(); idle();
            assertFalse(((NativeRemoteAgents) h.get("remoteAgents")).targeted());
            assertEquals("", h.composer().input.getText().toString()); assertFalse(h.composer().commands.menu.isShowing());
        }
    }

    @Test public void sendSelectsTheOpenMentionAndRoutesSlashCommandsToTheRemoteAgent() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture); open(h, "@Trust"); h.io.run();
            h.call("sendOrStop"); h.io.run();
            key(h, KeyEvent.KEYCODE_DPAD_DOWN); h.call("sendOrStop"); h.io.run();
            assertTrue(((NativeRemoteAgents) h.get("remoteAgents")).targeted());
            h.composer().input.setText("/review remote changes"); idle();
            assertFalse(h.composer().commands.menu.isShowing()); h.call("sendOrStop"); h.io.run();
            assertEquals("/review remote changes", fixture.requests.stream().filter(input -> input.optString("operation").equals("delegate")).findFirst().orElseThrow().getString("message"));
            assertTrue(MainActivityGoalTest.ShadowClient.calls.stream().noneMatch(call -> call.get(1).endsWith("/messages") || call.get(1).endsWith("/commands")));
        }
    }

    @Test public void mentionMatchingNoServerIsSentAsTextWithoutAnMcpField() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture);
            h.composer().input.setText("Ask @nobody"); idle(); h.io.run();
            assertFalse("A mention that matches no server opens no list", h.composer().commands.menu.isShowing());
            h.call("sendOrStop"); h.io.run();
            List<String> request = MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(1).endsWith("/messages")).findFirst().orElseThrow();
            JSONObject input = new JSONObject(request.get(2));
            assertEquals("Ask @nobody", input.getString("message"));
            // Servers that predate MCP selection reject the unknown field.
            assertFalse(input.has("mcp_servers"));
        }
    }

    @Test public void selectedMCPSurvivesAMissingOrFailedServerList() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture);
            NativeRemoteAgents remote = h.get("remoteAgents");
            open(h, "/mcp"); h.io.run(); key(h, KeyEvent.KEYCODE_ENTER);
            assertEquals("[\"spool_federation\"]", remote.mcpServers().toString());
            // The list is absent while a route or session is being checked.
            h.set("federationTargets", null); h.call("updateComposer");
            assertNotNull(description(h.composer(), "Remove remote agent MCP"));
            h.set("federationTargets", targets()); h.call("updateComposer");
            assertEquals("[\"spool_federation\"]", remote.mcpServers().toString());
            // A changed list is refreshed from the server; that refresh fails.
            fixture.failServers = true;
            h.set("federationTargets", targets().put(json("id", "server-c", "name", "Trusted C"))); h.call("updateComposer");
            open(h, "/mcp"); drain(h);
            assertTrue(fixture.requests.stream().filter(input -> input.optString("operation").equals("servers")).count() > 1);
            assertEquals("[\"spool_federation\"]", remote.mcpServers().toString());
            // An empty list is an answer: no trusted server remains.
            h.set("federationTargets", new JSONArray()); h.call("updateComposer");
            assertEquals(0, remote.mcpServers().length());
            assertNull(description(h.composer(), "Remove remote agent MCP"));
        }
    }

    @Test public void selectedMCPIsSerializedForMultipartAndDefaultIsEmpty() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); prepare(h, fixture);
            assertEquals(0, ((NativeRemoteAgents) h.get("remoteAgents")).mcpServers().length());
            open(h, "/mcp"); h.io.run(); key(h, KeyEvent.KEYCODE_ENTER);
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            Class<?> type = Class.forName("com.cloudgeek.spool.android.MainActivity$Attachment");
            Object file = ReflectionHelpers.callConstructor(type, ReflectionHelpers.ClassParameter.from(String.class, "note.txt"),
                    ReflectionHelpers.ClassParameter.from(String.class, "text/plain"), ReflectionHelpers.ClassParameter.from(byte[].class, new byte[] {65}));
            List<Object> files = ReflectionHelpers.getField(draft, "files"); files.add(file);
            h.send("Local attachment task"); h.io.run();
            List<String> request = MainActivityGoalTest.ShadowClient.calls.stream().filter(call -> call.get(0).equals("UPLOAD")).findFirst().orElseThrow();
            JSONObject fields = new JSONObject(request.get(2)).getJSONObject("fields");
            assertEquals("[\"spool_federation\"]", fields.getString("mcp_servers")); assertEquals("Local attachment task", fields.getString("message"));
        }
    }

    @Test public void mcpCreatedCallsAppearWithoutSelectingARemoteTargetAndPauseInBackground() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); fixture.task = json("task_id", "mcp-task", "project_id", "project-b", "project_name", "Build project",
                    "conversation_id", "mcp-conversation", "agent", "codex", "status", "completed", "text", "MCP final answer");
            prepare(h, fixture);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1500)); drain(h);
            assertNotNull(button(h.composer(), "Remote agents · 1 · Completed"));
            assertFalse(((NativeRemoteAgents) h.get("remoteAgents")).targeted());
            h.set("foreground", false); h.call("updateComposer");
            int count = fixture.requests.size();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(3)); drain(h);
            assertEquals(count, fixture.requests.size());
        }
    }

    @Test public void historicalCallsRemainVisibleWithoutTrustAndOfferNoRemoteControls() throws Exception {
        for (String status : new String[] {"completed", "awaiting_approval"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            Fixture fixture = new Fixture(); fixture.trusted = false; fixture.historyOnly = true;
            fixture.task = json("task_id", "historical-task", "project_id", "project-b", "project_name", "Build project", "conversation_id", "original-conversation",
                    "agent", "codex", "status", status, "text", status.equals("completed") ? "**Historical final answer**" : "",
                    "pending_approval", json("id", "obsolete-approval", "title", "Obsolete approval"));
            prepare(h, fixture);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(1500)); drain(h);
            String label = status.equals("completed") ? "Completed" : "Approval required";
            View entry = button(h.composer(), "Remote agents · 1 · " + label); assertNotNull(entry); entry.performClick();
            button(dialog(h).body, "@Original PC/Build project · " + label).performClick();
            assertNull(button(dialog(h).body, "Continue conversation")); assertNull(button(dialog(h).body, "Remote history"));
            assertNull(button(dialog(h).body, "Stop response")); assertNull(button(dialog(h).body, "Allow once"));
            assertEquals(status.equals("completed"), button(dialog(h).body, "Use result as context") != null);
            assertTrue(fixture.requests.stream().noneMatch(input -> input.optString("operation").equals("task")));
        }
    }

    private void screenshot(MainActivityGoalTest.Harness h, String name) throws Exception {
        if (android.os.Build.VERSION.SDK_INT != 35) return;
        View root = h.get("root"); NativeMarkwonHistoryTest.layout(root); idle();
        android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(360, 800, android.graphics.Bitmap.Config.ARGB_8888);
        android.graphics.Canvas canvas = new android.graphics.Canvas(image); root.draw(canvas);
        NativePopupFrame frame = ReflectionHelpers.getField(h.composer().commands.menu, "frame");
        assertTrue(frame.window.width() <= 360);
        frame.measure(View.MeasureSpec.makeMeasureSpec(frame.window.width(), View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(frame.window.height(), View.MeasureSpec.EXACTLY));
        frame.layout(0, 0, frame.window.width(), frame.window.height());
        canvas.save(); canvas.translate(frame.window.left, frame.window.top); frame.draw(canvas); canvas.restore();
        java.io.File directory = new java.io.File(System.getProperty("spool.ui.artifacts"), "remote-agents");
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (java.io.FileOutputStream output = new java.io.FileOutputStream(new java.io.File(directory, name + ".png"))) {
            assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
        } finally { image.recycle(); }
    }

    private android.widget.CompoundButton checkbox(View view) {
        if (view instanceof android.widget.CompoundButton) return (android.widget.CompoundButton) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            android.widget.CompoundButton item = checkbox(((ViewGroup) view).getChildAt(i)); if (item != null) return item;
        }
        return null;
    }
    private String allText(View view) {
        StringBuilder text = new StringBuilder(view instanceof TextView ? ((TextView) view).getText() : "");
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) text.append(allText(((ViewGroup) view).getChildAt(i)));
        return text.toString();
    }
}
