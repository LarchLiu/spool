package com.cloudgeek.spool.android;

import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.LinkedHashSet;
import java.util.Locale;
import java.util.UUID;
import java.util.function.Consumer;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.json.JSONArray;
import org.json.JSONObject;

/** Composer-only routing and opt-in MCP metadata. Remote history remains on the server. */
final class NativeRemoteAgents implements NativeComposerCommands.ClientMenu {
    interface Host {
        void request(JSONObject input, Consumer<Object> done, Consumer<Exception> failed);
        void parent(Consumer<String> done, Consumer<Exception> failed);
        void changed();
        void error(Exception error);
        void open(String url);
    }
    private static final Pattern MENTION = Pattern.compile("(?:^|\\s)@([^\\s]*)$");
    static final String MCP = "spool_federation";
    static final class MCPEntry {
        final String id, name, description, unavailable, removeLabel, keywords;
        final boolean requiresTrust;
        MCPEntry(String id, String name, String description, String unavailable, String removeLabel, String keywords, boolean requiresTrust) {
            this.id = id; this.name = name; this.description = description; this.unavailable = unavailable;
            this.removeLabel = removeLabel; this.keywords = keywords; this.requiresTrust = requiresTrust;
        }
    }
    static final List<MCPEntry> BUILTIN_MCPS = java.util.Collections.singletonList(new MCPEntry(MCP, "Remote agents",
            "Delegate to agents on trusted servers", "No trusted servers available", "Remove remote agent MCP", "跨服 Agent", true));
    private final List<MCPEntry> mcpCatalog;
    private final java.util.Set<String> selectedMCPs = new LinkedHashSet<>();
    private final Host host;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Runnable poll = this::refreshCalls;
    private NativeUi ui;
    private NativeUi.Composer composer;
    private LinearLayout controls;
    private String controlsKey = "", scope = "", chat = "", targetsKey = "";
    private String retryKey = "", retryID = "", projectError = "";
    private int epoch, projectRequest, agentRequest, serverRequest;
    private boolean enabled, active, writable, posting, running, busy, serversLoaded, serversLoading, projectsLoading, polling, scheduled;
    private JSONArray servers = new JSONArray(), projects = new JSONArray(), agents = new JSONArray(), calls = new JSONArray();
    private JSONObject menuServer, targetServer, targetProject;
    private String conversation = "";
    NativeModelSelection selection = new NativeModelSelection("codex", "", "");
    private final NativeModelSelection.Memory modelSelectionMemory = new NativeModelSelection.Memory();
    private NativeDialog dialog;
    private String displayedTask = "", displayedState = "";

    NativeRemoteAgents(Host host) { this(host, BUILTIN_MCPS); }
    NativeRemoteAgents(Host host, List<MCPEntry> mcpCatalog) {
        this.host = host; this.mcpCatalog = new ArrayList<>(mcpCatalog);
    }

    void attach(NativeUi ui, NativeUi.Composer composer) {
        closeDialog();
        this.ui = ui; this.composer = composer;
        controls = ui.column(); controlsKey = "";
        composer.box.addView(controls, 0, new LinearLayout.LayoutParams(-1, -2));
        composer.commands.clientMenu = this;
    }

    void bind(String scope, String chat, boolean enabled, boolean active, boolean writable,
            boolean posting, boolean running, JSONArray targets) {
        if (!this.scope.equals(scope) || !this.chat.equals(chat)) {
            reset(); this.scope = scope; this.chat = chat;
        }
        this.enabled = enabled; this.active = active; this.writable = writable;
        this.posting = posting; this.running = running;
        // A missing list (signed out for a moment, a route still being checked)
        // says nothing about trust: the known servers and chosen MCPs are kept.
        if (enabled && targets != null) {
            String key = targets.toString();
            if (!targetsKey.equals(key)) {
                ++serverRequest; serversLoading = false;
                targetsKey = key; servers = targets;
                serversLoaded = false;
            }
            retainAvailableMCPs();
        }
        if (targeted() && find(servers, targetServer.optString("id")) == null) {
            targetServer = targetProject = null; conversation = ""; agents = new JSONArray();
        }
        renderControls();
        if (active && enabled && !chat.isEmpty() && writable) schedule();
        else { main.removeCallbacks(poll); scheduled = false; }
    }

    void reset() {
        modelSelectionMemory.clear();
        ++epoch; ++projectRequest; ++agentRequest; ++serverRequest; main.removeCallbacks(poll); closeDialog();
        selectedMCPs.clear();
        busy = serversLoading = serversLoaded = polling = projectsLoading = scheduled = false;
        menuServer = targetServer = targetProject = null;
        conversation = retryKey = retryID = projectError = targetsKey = controlsKey = "";
        projects = agents = calls = servers = new JSONArray();
        selection = new NativeModelSelection("codex", "", "");
    }

    void suspend() { active = false; scheduled = false; main.removeCallbacks(poll); closeDialog(); }
    void adoptChat(String id) { chat = id; }
    boolean targeted() { return targetServer != null && targetProject != null; }
    boolean busy() { return busy; }
    private boolean mcpAvailable(MCPEntry item) { return enabled && (!item.requiresTrust || servers.length() > 0); }
    private void retainAvailableMCPs() {
        selectedMCPs.removeIf(id -> mcpCatalog.stream().noneMatch(item -> item.id.equals(id) && mcpAvailable(item)));
    }
    JSONArray mcpServers() {
        JSONArray values = new JSONArray();
        for (MCPEntry item : mcpCatalog) if (selectedMCPs.contains(item.id) && mcpAvailable(item)) values.put(item.id);
        return values;
    }
    /** Adds the chosen MCPs to a message. Servers that predate the field reject it, so none chosen sends nothing. */
    void attachMCPs(JSONObject payload) {
        JSONArray chosen = mcpServers();
        if (chosen.length() > 0) put(payload, "mcp_servers", chosen);
    }
    boolean ready() {
        if (!targeted() || busy || !writable || running) return false;
        boolean available = false;
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = at(agents, i);
            if (agent.optString("backend").equals(selection.backend)) available = agent.optBoolean("enabled") && agent.optBoolean("installed");
        }
        return available && targetProject.optBoolean("can_create");
    }

    @Override public NativeSlashCommand root() {
        return option("mcp", "mcp", ui.t("Choose MCP tools for this chat"), "plug", true, true);
    }

    static String mention(String value) {
        Matcher match = MENTION.matcher(value);
        return match.find() ? match.group(1) : null;
    }
    static String consumeMention(String value) {
        String text = value.replaceFirst("(^|\\s)@[^\\s]*$", "$1");
        int end = text.length(); while (end > 0 && NativeGoalCommand.space(text.charAt(end - 1))) --end;
        return text.substring(0, end);
    }
    private String draft() { return composer.input.getText().toString(); }
    private void draft(String value) {
        composer.input.setText(value); composer.input.setSelection(composer.input.length()); composer.input.requestFocus();
    }
    private boolean mcpMenu(String text) { return text.matches("(?i)^/mcp(?:\\s+.*)?$"); }

    @Override public List<NativeSlashCommand> suggestions(String text) {
        if (!targeted() && mcpMenu(text)) {
            if (enabled) loadServers();
            List<NativeSlashCommand> rows = new ArrayList<>();
            String query = text.substring(4).trim().toLowerCase(Locale.ROOT);
            for (MCPEntry item : mcpCatalog) {
                if (!(item.id + " " + item.name + " " + ui.t(item.name) + " " + item.description + " " + item.keywords).toLowerCase(Locale.ROOT).contains(query)) continue;
                rows.add(option("mcp:" + item.id, ui.t(item.name), ui.t(mcpAvailable(item) ? item.description : item.unavailable),
                        selectedMCPs.contains(item.id) ? "check" : "plug", mcpAvailable(item), false));
            }
            if (rows.isEmpty()) rows.add(option("empty", ui.t("No matching MCP tools"), "", "info", false, false));
            rows.add(option("mcp-back", ui.t("Back"), "mcp", "back", true, false));
            return rows;
        }
        if (!enabled) return null;
        String query = mention(text);
        if (query == null) { menuServer = null; return null; }
        loadServers();
        List<NativeSlashCommand> rows = new ArrayList<>();
        JSONArray entries = menuServer == null ? servers : projects;
        String filter = query.toLowerCase(Locale.ROOT);
        for (int i = 0; i < entries.length(); i++) {
            JSONObject item = at(entries, i);
            String detail = summary(item);
            if (!(item.optString("name") + " " + detail).toLowerCase(Locale.ROOT).contains(filter)) continue;
            if (menuServer != null && !item.optBoolean("can_create")) detail = (detail.isEmpty() ? "" : detail + " · ") + ui.t("Read-only");
            rows.add(option((menuServer == null ? "server:" : "project:") + item.optString("id"), item.optString("name"), detail,
                    menuServer == null ? "globe" : "folder", menuServer == null || item.optBoolean("can_create"), menuServer == null));
        }
        if (rows.isEmpty()) {
            // Ordinary text may end in "@word". With no server to offer, it stays a message.
            if (menuServer == null) return null;
            rows.add(option("empty", ui.t(projectsLoading ? "Loading conversation…" : "No authorized remote projects."), projectError, "info", false, false));
        }
        if (menuServer != null) rows.add(option("remote-back", ui.t("Back"), menuServer.optString("name"), "back", true, false));
        return rows;
    }

    @Override public void select(NativeSlashCommand command) {
        if (!command.enabled || busy || posting || running || !writable) return;
        String name = command.name;
        if (name.equals("mcp")) draft("/mcp ");
        else if (name.startsWith("mcp:")) {
            String id = name.substring(4);
            if (mcpCatalog.stream().noneMatch(item -> item.id.equals(id) && mcpAvailable(item))) return;
            if (!selectedMCPs.remove(id)) selectedMCPs.add(id);
            draft(""); changed();
        }
        else if (name.equals("mcp-back") || name.equals("remote-back")) back();
        else if (name.startsWith("server:")) {
            menuServer = find(servers, name.substring(7));
            projects = new JSONArray(); projectError = ""; projectsLoading = true;
            draft(draft().replaceFirst("@[^\\s]*$", "@"));
            int request = ++projectRequest;
            request("projects", json("server", menuServer.optString("id")), value -> {
                if (request != projectRequest) return;
                projects = (JSONArray) value; projectsLoading = false; changed();
            }, error -> { if (request == projectRequest) { projectsLoading = false; projectError = error.getMessage(); changed(); } });
        } else if (name.startsWith("project:") && menuServer != null) {
            JSONObject project = find(projects, name.substring(8));
            if (project == null || !project.optBoolean("can_create")) return;
            String message = consumeMention(draft());
            chooseTarget(menuServer, project, "", null);
            draft(message);
        }
    }

    @Override public boolean canBack() { return mcpMenu(draft()) || menuServer != null; }
    @Override public boolean commandsEnabled() { return !targeted(); }

    @Override public boolean back() {
        if (mcpMenu(draft())) { draft("/"); return true; }
        if (menuServer != null) { ++projectRequest; menuServer = null; draft(draft().replaceFirst("@[^\\s]*$", "@")); return true; }
        return false;
    }

    boolean menuSubmission() {
        if (!targeted() && mcpMenu(draft())) { composer.commands.select(); return true; }
        // Only the open target list takes the send. A mention that matched no
        // server, or a list the user dismissed, leaves the text to be sent.
        if (mention(draft()) != null && composer.commands.clientMenuShowing()) { composer.commands.select(); return true; }
        return false;
    }

    private void loadServers() {
        if (serversLoaded || serversLoading || !active || !writable || posting || running) return;
        serversLoading = true;
        int version = ++serverRequest;
        request("servers", new JSONObject(), value -> {
            if (version != serverRequest) return;
            serversLoading = false; serversLoaded = true; servers = (JSONArray) value;
            retainAvailableMCPs();
            changed();
        }, error -> {
            if (version != serverRequest) return;
            // A failed refresh is not an empty list.
            serversLoading = false; serversLoaded = true; host.error(error); changed();
        });
    }

    private void chooseTarget(JSONObject server, JSONObject project, String conversation, NativeModelSelection keep) {
        if (targetServer == null || targetProject == null
                || !targetServer.optString("id").equals(server.optString("id"))
                || !targetProject.optString("id").equals(project.optString("id"))) modelSelectionMemory.clear();
        targetServer = server; targetProject = project; this.conversation = conversation; menuServer = null;
        agents = new JSONArray(); if (keep != null) selection = keep;
        int version = ++agentRequest;
        String serverID = server.optString("id"), projectID = project.optString("id");
        request("agents", json("server", serverID, "project", projectID), value -> {
            if (version != agentRequest || !targeted() || !targetServer.optString("id").equals(serverID) || !targetProject.optString("id").equals(projectID)) return;
            agents = (JSONArray) value;
            if (keep == null) for (int i = 0; i < agents.length(); i++) {
                JSONObject agent = at(agents, i);
                if (!agent.optBoolean("enabled") || !agent.optBoolean("installed")) continue;
                selection = NativeModelSelection.chat(agents, json("backend", agent.optString("backend"))); break;
            }
            changed();
        });
        changed();
    }

    void chooseModel() {
        if (!targeted() || busy) return;
        JSONArray available = agents;
        if (!conversation.isEmpty()) {
            available = new JSONArray();
            for (int i = 0; i < agents.length(); i++) {
                JSONObject agent = at(agents, i);
                if (agent.optString("backend").equals(selection.backend)) available.put(agent);
            }
        }
        NativePopover menu = NativePopover.model(ui);
        menu.content.addView(new NativeAgentModelMenu(ui, available, selection, "", true, modelSelectionMemory, value -> { selection = value; changed(); }),
                new LinearLayout.LayoutParams(-1, -2));
        menu.show(composer.model, true);
    }

    void send(String message, boolean attachments, Consumer<Boolean> done) {
        if (!ready() || posting || running) return;
        if (attachments) { host.error(new IllegalArgumentException(ui.t("Remote calls currently accept text only"))); return; }
        JSONObject fields = json("server", targetServer.optString("id"), "project", targetProject.optString("id"),
                "agent", selection.backend, "model", selection.model, "reasoning_effort", selection.effort,
                "conversation_id", conversation, "message", message);
        String key = fields.toString();
        if (!retryKey.equals(key)) { retryKey = key; retryID = UUID.randomUUID().toString(); }
        put(fields, "request_id", retryID); put(fields, "wait_seconds", 1);
        busy = true; changed();
        int source = epoch;
        host.parent(id -> {
            if (source != epoch) return;
            adoptChat(id);
            request("delegate", fields, value -> {
                JSONObject task = (JSONObject) value;
                merge(json("server", fields.optString("server"), "message", message, "task", task));
                conversation = task.optString("conversation_id"); retryKey = retryID = "";
                busy = false; done.accept(true); changed(); schedule();
            }, error -> { busy = false; host.error(error); done.accept(false); changed(); });
        }, error -> { if (source == epoch) { busy = false; host.error(error); done.accept(false); changed(); } });
    }

    private void refreshCalls() {
        scheduled = false;
        if (!active || !enabled || !writable || chat.isEmpty() || polling) return;
        polling = true;
        request("calls", new JSONObject(), value -> {
            if (!active) { polling = false; return; }
            JSONArray latest = (JSONArray) value;
            for (int i = latest.length() - 1; i >= 0; i--) merge(at(latest, i));
            List<JSONObject> pending = new ArrayList<>();
            for (int i = 0; i < calls.length(); i++) if (!at(calls, i).optBoolean("history_only") && running(task(at(calls, i)))) pending.add(at(calls, i));
            if (pending.isEmpty()) { polling = false; changed(); schedule(); return; }
            int[] left = {pending.size()};
            for (JSONObject call : pending) request("task", taskFields(call), result -> {
                updateTask(call, result); if (--left[0] == 0) { polling = false; schedule(); }
            }, error -> { if (--left[0] == 0) { polling = false; schedule(); } });
            changed();
        }, error -> { polling = false; schedule(); });
    }

    private void schedule() {
        if (active && enabled && writable && !chat.isEmpty() && !scheduled && !polling) {
            scheduled = true; main.postDelayed(poll, 1500);
        }
    }

    private void merge(JSONObject call) {
        JSONArray next = new JSONArray().put(call);
        for (int i = 0; i < calls.length() && next.length() < 20; i++) {
            JSONObject prior = at(calls, i);
            if (!task(prior).optString("task_id").equals(task(call).optString("task_id"))) next.put(prior);
        }
        calls = next;
    }

    private void updateTask(JSONObject call, Object task) { put(call, "task", task); merge(call); changed(); }

    private void renderControls() {
        if (controls == null) return;
        String key = targeted() + "|" + targetServer + "|" + targetProject + "|" + conversation + "|" + selectedMCPs + "|" + busy + "|" + posting + "|" + running + "|" + writable + "|" + calls;
        if (controlsKey.equals(key)) return;
        controlsKey = key; controls.removeAllViews();
        controls.setVisibility(!targeted() && selectedMCPs.isEmpty() && calls.length() == 0 ? View.GONE : View.VISIBLE);
        controls.setPadding(ui.dp(12), ui.dp(8), ui.dp(12), 0);
        if (targeted()) {
            control("@" + targetServer.optString("name") + "/" + targetProject.optString("name"), "Remove remote target", () -> {
                targetServer = targetProject = null; conversation = ""; agents = new JSONArray();
            });
            if (!conversation.isEmpty()) {
                android.widget.Button fresh = ui.button(ui.t("New remote conversation"), false, () -> { conversation = ""; changed(); });
                fresh.setEnabled(!busy && !posting && !running && writable); controls.addView(fresh);
            }
        } else for (MCPEntry item : mcpCatalog) {
            if (selectedMCPs.contains(item.id)) control("MCP · " + ui.t(item.name), item.removeLabel, () -> selectedMCPs.remove(item.id));
        }
        if (calls.length() > 0) {
            JSONObject latest = task(at(calls, 0));
            controls.addView(ui.button(ui.t("Remote agents") + " · " + calls.length() + " · " + status(latest), false, this::showCalls));
        }
    }

    private void control(String title, String removeLabel, Runnable clear) {
        LinearLayout row = ui.row();
        TextView label = ui.text(title, 12, ui.text);
        label.setSingleLine(true); label.setEllipsize(android.text.TextUtils.TruncateAt.END);
        row.addView(label, new LinearLayout.LayoutParams(0, -2, 1));
        NativeUi.IconButton remove = ui.icon("close", 14, ui.t(removeLabel), () -> {
            if (busy || posting || running || !writable) return;
            clear.run(); changed();
        });
        remove.setEnabled(!busy && !posting && !running && writable);
        row.addView(remove, new LinearLayout.LayoutParams(ui.dp(32), ui.dp(32))); controls.addView(row);
    }

    private void showCalls() {
        closeDialog(); dialog = new NativeDialog(ui, ui.t("Remote agents"), ui.t("Close"));
        for (int i = 0; i < calls.length(); i++) {
            JSONObject call = at(calls, i), task = task(call);
            dialog.body.addView(ui.button(label(call) + " · " + status(task), false, () -> showTask(call)));
        }
        dialog.show();
    }

    private void showTask(JSONObject call) {
        closeDialog(); displayedTask = task(call).optString("task_id");
        dialog = new NativeDialog(ui, label(call), ui.t("Close"));
        renderTask(call); dialog.show();
    }

    private void renderTask(JSONObject call) {
        if (dialog == null) return;
        JSONObject task = task(call); displayedState = taskState(task);
        dialog.body.removeAllViews(); dialog.actions.removeAllViews();
        text(dialog.body, task.optString("agent") + " · " + task.optString("model") + " · " + task.optString("reasoning_effort") + " · " + status(task));
        text(dialog.body, call.optString("message"));
        boolean historyOnly = call.optBoolean("history_only");
        if (historyOnly) text(dialog.body, ui.t("Historical remote call. Open the original server to refresh or continue."));
        String result = task.optString(task.optString("status").equals("completed") ? "text" : "progress");
        if (!result.isEmpty()) dialog.body.addView(NativeMarkdown.view(ui, result, false, host::open));
        text(dialog.body, task.optString("error"));
        if (task.optBoolean("truncated")) text(dialog.body, ui.t("Output truncated. Read remote history for details."));
        JSONObject approval = task.optJSONObject("pending_approval");
        if (!historyOnly && approval != null) approval(call, approval);
        if (!historyOnly && running(task)) dialog.body.addView(ui.button(ui.t("Stop response"), false, () -> request("cancel", taskFields(call), value -> {
            updateTask(call, value);
        })));
        if (!historyOnly) {
            dialog.body.addView(ui.button(ui.t("Continue conversation"), false, () -> resume(call)));
            dialog.body.addView(ui.button(ui.t("Remote history"), false, () -> history(call, "")));
        }
        if (task.optString("status").equals("completed") && !task.optString("text").isEmpty()) {
            dialog.body.addView(ui.button(ui.t("Use result as context"), false, () -> {
                if (!writable || busy || posting || running) return;
                JSONObject context = json("remote_server", call.optString("server"), "remote_conversation", task.optString("conversation_id"),
                        "remote_task", task.optString("task_id"), "reference_output", task.optString("text"), "truncated", task.optBoolean("truncated"));
                String reference = context.toString();
                String next = draft().isEmpty() ? reference : draft() + "\n\n" + reference;
                if (next.getBytes(java.nio.charset.StandardCharsets.UTF_8).length > 131072) {
                    host.error(new IllegalArgumentException(ui.t("Message and remote result exceed the message limit."))); return;
                }
                targetServer = targetProject = null; conversation = ""; closeDialog(); draft(next); changed();
            }));
        }
    }

    private void approval(JSONObject call, JSONObject approval) {
        text(dialog.body, ui.t("Approval required")); text(dialog.body, ui.t(approval.optString("title")));
        text(dialog.body, approval.optString("reason")); text(dialog.body, approval.optString("detail"));
        text(dialog.body, ui.t(approval.optString("unavailable")));
        String url = approval.optString("url");
        if (!url.isEmpty()) dialog.body.addView(ui.button(ui.t("Open authorization page"), false, () -> host.open(url)));
        NativeApprovalForm form = new NativeApprovalForm(ui, approval); form.setEnabled(writable); dialog.body.addView(form);
        TextView detail = ui.text("", 11, ui.muted); dialog.body.addView(detail);
        NativeApprovalActions[] actions = new NativeApprovalActions[1];
        actions[0] = new NativeApprovalActions(ui, approval, detail, choice -> {
            if (!writable || !active) return;
            JSONObject current = task(call).optJSONObject("pending_approval");
            if (current == null || !approval.optString("id").equals(current.optString("id"))) return;
            try {
                boolean allow = NativeApprovalActions.contentRequired(choice);
                JSONObject body = taskFields(call);
                put(body, "approval", json("id", approval.optString("id"), "turn_id", task(call).optString("turn_id"),
                        "allow", allow, "decision", choice.optString("id"), "content", allow ? form.content() : null, "feedback", form.feedback()));
                actions[0].setEnabled(false); form.setEnabled(false);
                request("approve", body, value -> {
                    request("task", taskFields(call), next -> updateTask(call, next));
                }, error -> { host.error(error); actions[0].setEnabled(writable); form.setEnabled(writable); });
            } catch (IllegalArgumentException error) { host.error(error); }
        });
        actions[0].setEnabled(writable); dialog.body.addView(actions[0]);
    }

    private void resume(JSONObject call) {
        String server = call.optString("server"); JSONObject task = task(call);
        request("projects", json("server", server), value -> {
            JSONObject project = find((JSONArray) value, task.optString("project_id"));
            if (project == null) { host.error(new IllegalArgumentException(ui.t("No authorized remote projects."))); return; }
            if (!project.optBoolean("can_create")) { host.error(new IllegalArgumentException(ui.t("Read-only"))); return; }
            JSONObject target = find(servers, server);
            chooseTarget(target == null ? json("id", server, "name", server) : target, project, task.optString("conversation_id"),
                    new NativeModelSelection(task.optString("agent"), task.optString("model"), task.optString("reasoning_effort")));
            closeDialog(); composer.input.requestFocus();
        });
    }

    private void history(JSONObject call, String before) {
        request("history", json("server", call.optString("server"), "conversation_id", task(call).optString("conversation_id"), "before", before), value -> {
            JSONObject page = (JSONObject) value;
            closeDialog(); dialog = new NativeDialog(ui, ui.t("Remote history"), ui.t("Close"));
            JSONArray turns = page.optJSONArray("turns");
            if (turns != null) for (int i = 0; i < turns.length(); i++) {
                JSONArray messages = at(turns, i).optJSONArray("messages");
                if (messages != null) for (int j = 0; j < messages.length(); j++) {
                    JSONObject message = at(messages, j); text(dialog.body, message.optString("role"));
                    dialog.body.addView(NativeMarkdown.view(ui, message.optString("text"), message.optString("role").equals("user"), host::open));
                }
            }
            if (!page.optString("next").isEmpty()) dialog.body.addView(ui.button(ui.t("Load older messages"), false, () -> history(call, page.optString("next"))));
            dialog.show();
        });
    }

    private String label(JSONObject call) {
        JSONObject task = task(call), server = find(servers, call.optString("server"));
        return "@" + (server == null ? call.optString("server_name", call.optString("server")) : server.optString("name")) + "/" + task.optString("project_name", task.optString("project_id"));
    }
    private String status(JSONObject task) {
        switch (task.optString("status")) {
            case "running": return ui.t("Running");
            case "completed": return ui.t("Completed");
            case "awaiting_approval": return ui.t("Approval required");
            case "stopped": return ui.t("Stopped");
            case "failed": return ui.t("Failed");
            default: return task.optString("status");
        }
    }
    private void text(LinearLayout parent, String value) {
        if (value == null || value.isEmpty()) return;
        TextView text = ui.text(value, 12, ui.text); text.setTextIsSelectable(true);
        text.setPadding(0, ui.dp(6), 0, ui.dp(6)); parent.addView(text);
    }
    private void changed() {
        renderControls();
        if (composer != null) composer.commands.refresh();
        if (dialog != null && !displayedTask.isEmpty()) for (int i = 0; i < calls.length(); i++) {
            JSONObject call = at(calls, i);
            if (displayedTask.equals(task(call).optString("task_id")) && !displayedState.equals(taskState(task(call)))) renderTask(call);
        }
        host.changed();
    }
    private void closeDialog() { if (dialog != null) dialog.dismiss(); dialog = null; displayedTask = displayedState = ""; }
    private void request(String operation, JSONObject fields, Consumer<Object> done) { request(operation, fields, done, host::error); }
    private void request(String operation, JSONObject fields, Consumer<Object> done, Consumer<Exception> failed) {
        put(fields, "operation", operation); put(fields, "origin_chat", chat);
        int source = epoch;
        host.request(fields, result -> { if (source == epoch) done.accept(result); }, error -> { if (source == epoch) failed.accept(error); });
    }
    private static boolean running(JSONObject task) { return task.optString("status").equals("running") || task.optString("status").equals("awaiting_approval"); }
    private static String taskState(JSONObject task) {
        try {
            JSONObject state = new JSONObject(task.toString()); state.remove("updated_at"); return state.toString();
        } catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
    }
    private static JSONObject task(JSONObject call) { JSONObject value = call.optJSONObject("task"); return value == null ? new JSONObject() : value; }
    private static JSONObject taskFields(JSONObject call) { return json("server", call.optString("server"), "task_id", task(call).optString("task_id")); }
    private static NativeSlashCommand option(String name, String title, String detail, String icon, boolean enabled, boolean next) {
        return new NativeSlashCommand(name, title, detail, icon, enabled, next);
    }
    private static String summary(JSONObject item) { JSONObject summary = item.optJSONObject("summary"); return summary == null ? "" : summary.optString("text"); }
    private static JSONObject at(JSONArray values, int index) { JSONObject value = values.optJSONObject(index); return value == null ? new JSONObject() : value; }
    private static JSONObject find(JSONArray values, String id) { for (int i = 0; i < values.length(); i++) if (at(values, i).optString("id").equals(id)) return at(values, i); return null; }
    static JSONObject json(Object... pairs) {
        JSONObject value = new JSONObject();
        for (int i = 0; i + 1 < pairs.length; i += 2) put(value, (String) pairs[i], pairs[i + 1]);
        return value;
    }
    private static void put(JSONObject value, String key, Object data) {
        try { value.put(key, data); } catch (org.json.JSONException error) { throw new IllegalArgumentException(error); }
    }
}
