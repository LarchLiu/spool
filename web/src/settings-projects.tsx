import { useState } from "react";
import {
  FolderCog,
  FolderOpen,
  Plus,
  RefreshCw,
  ShieldCheck,
  X,
} from "lucide-react";
import { SettingsBack, SettingsList } from "./settings-list";
import {
  newProject,
  projectDraft,
  projectMembers,
  type ManagedProject,
} from "./settings-client";
import {
  Card,
  Row,
  Toggle,
  TextField,
  PathField,
  Hint,
  Save,
  submit,
  knownMessage,
  type PageProps,
} from "./settings-ui";

export function ProjectsPage({ data, run, t }: PageProps) {
  const [draft, setDraft] = useState<ManagedProject>(newProject);
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState("");
  const [repository, setRepository] = useState("");
  const [githubPath, setGitHubPath] = useState(data.github_path);
  const [githubPathOpen, setGitHubPathOpen] = useState(false);
  const people = data.users.filter((person) => !person.disabled);
  const issue = draft.issues;
  const update = (value: Partial<typeof issue>) =>
    setDraft((old) => ({ ...old, issues: { ...old.issues, ...value } }));
  const select = (project: ManagedProject) => {
    setDraft(projectDraft(project));
    setLabel("");
    setRepository(project.issues.repository);
    setEditing(true);
  };
  const toggleMember = (id: string, checked: boolean) =>
    setDraft(
      projectMembers(
        draft,
        checked
          ? [...draft.members, id]
          : draft.members.filter((value) => value !== id),
      ),
    );
  const save = async () => {
    const next = await run({ kind: "save-project", project: draft });
    const saved = next?.projects.find(
      (project) => project.id === next.saved_id,
    );
    if (saved) setDraft(projectDraft(saved));
  };
  const live = data.projects.find((project) => project.id === draft.id);
  if (!editing)
    return (
      <>
        <SettingsList
          label={t("Projects")}
          summary={t("{count} projects", { count: data.projects.length })}
          empty={t("No projects yet. Add a local directory to get started.")}
          items={data.projects.map((project) => ({
            id: project.id,
            title: project.name,
            description: project.path,
            status: project.archived
              ? t("Archived")
              : t("{count} members", { count: project.members?.length ?? 0 }),
            icon: <FolderOpen size={18} />,
          }))}
          onSelect={(id) => {
            const project = data.projects.find((project) => project.id === id);
            if (project) select(project);
          }}
          action={
            <button
              type="button"
              className="settings-primary"
              data-new-entity
              onClick={() => select(newProject())}
            >
              <Plus size={15} />
              {t("New project")}
            </button>
          }
        />
        <div className="settings-github-cli">
          <Card title="GitHub CLI">
            <Row title={t("Status")} help={data.github_message}>
              <button
                type="button"
                onClick={() => void run({ kind: "check-github" })}
              >
                <RefreshCw size={14} />
                {t("Recheck GitHub CLI")}
              </button>
              <button
                type="button"
                aria-expanded={githubPathOpen}
                aria-controls="github-cli-path-settings"
                onClick={() => setGitHubPathOpen((open) => !open)}
              >
                <FolderCog size={14} />
                {t("Path settings")}
              </button>
            </Row>
            <div id="github-cli-path-settings" hidden={!githubPathOpen}>
              <PathField
                title={t("GitHub CLI executable (blank = auto-detect)")}
                value={githubPath}
                kind="executable"
                onChange={setGitHubPath}
                t={t}
              />
              <div className="settings-card-actions">
                <button
                  type="button"
                  className="settings-primary"
                  onClick={() =>
                    void run({ kind: "save-github", path: githubPath })
                  }
                >
                  {t("Save GitHub CLI path")}
                </button>
              </div>
            </div>
          </Card>
          {githubPathOpen && (
            <Hint>
              {t(
                "Applies to all projects. Enter the absolute path to gh (gh.exe on Windows), without quotes. Leave blank to auto-detect.",
              )}
            </Hint>
          )}
        </div>
      </>
    );
  return (
    <>
      <SettingsBack
        label={t("Back to list")}
        onClick={() => setEditing(false)}
      />
      <form onSubmit={(e) => submit(e, () => void save())}>
        <Card title={t("Project")}>
          <TextField
            title={t("Project name")}
            value={draft.name}
            onChange={(name) => setDraft({ ...draft, name })}
            required
          />
          <PathField
            title={t("Local directory / agent working directory")}
            value={draft.path}
            kind="directory"
            onChange={(path) => {
              setDraft({ ...draft, path });
              setRepository("");
            }}
            t={t}
          />
          <Row title={t("Archive project (deny new access, keep history)")}>
            <Toggle
              label={t("Archive project (deny new access, keep history)")}
              value={draft.archived}
              onChange={(archived) => setDraft({ ...draft, archived })}
            />
          </Row>
        </Card>
        <Card title={t("People allowed to use this project")}>
          {people.length ? (
            people.map((person) => (
              <Row
                key={person.id}
                title={person.nickname}
                help={person.username}
              >
                {draft.members.includes(person.id) && (
                  <div className="settings-member-permissions">
                    <div className="settings-member-permission">
                      <span>{t("In-app permissions")}</span>
                      <div
                        className="settings-member-access"
                        role="group"
                        aria-label={`${person.nickname} · ${t("In-app permissions")}`}
                      >
                        {(["read-only", "create"] as const).map((access) => (
                          <button
                            key={access}
                            type="button"
                            aria-pressed={
                              (draft.member_own_access[person.id] ??
                                "create") === access
                            }
                            onClick={() =>
                              setDraft((old) => ({
                                ...old,
                                member_own_access: {
                                  ...old.member_own_access,
                                  [person.id]: access,
                                },
                              }))
                            }
                          >
                            {t(
                              access === "read-only"
                                ? "Read-only"
                                : "Can create",
                            )}
                          </button>
                        ))}
                      </div>
                    </div>
                    <div className="settings-member-permission">
                      <span>{t("Shared session permissions")}</span>
                      <div
                        className="settings-member-access"
                        role="group"
                        aria-label={`${person.nickname} · ${t("Shared session permissions")}`}
                      >
                        {(["read-only", "control"] as const).map((access) => (
                          <button
                            key={access}
                            type="button"
                            aria-pressed={
                              (draft.member_access[person.id] ?? "control") ===
                              access
                            }
                            onClick={() =>
                              setDraft((old) => ({
                                ...old,
                                member_access: {
                                  ...old.member_access,
                                  [person.id]: access,
                                },
                              }))
                            }
                          >
                            {t(
                              access === "read-only"
                                ? "Read-only"
                                : "Can control sessions",
                            )}
                          </button>
                        ))}
                      </div>
                    </div>
                  </div>
                )}
                <Toggle
                  label={`${t("People allowed to use this project")} · ${person.nickname}`}
                  value={draft.members.includes(person.id)}
                  onChange={(checked) => toggleMember(person.id, checked)}
                />
              </Row>
            ))
          ) : (
            <Row title={t("Create a person first to grant project access.")} />
          )}
        </Card>
        <Hint>
          {t(
            "In-app permissions: read-only allows viewing existing chats; Can create also allows creating and controlling personal chats. Shared session permissions separately control other people's shared chats and shared local sessions. Assigned task permissions are unchanged.",
          )}
        </Hint>
        <Card>
          <Row
            title={t("Share in-app sessions")}
            help={t(
              "Choose whether each project member can only view or also control other people's shared chats.",
            )}
          >
            <Toggle
              label={t("Share in-app sessions")}
              value={draft.shared_chats}
              onChange={(shared_chats) => setDraft({ ...draft, shared_chats })}
            />
          </Row>
          <Row
            title={t("Share local sessions")}
            help={t(
              "Share the latest 20 Codex and Claude sessions started outside Spool. Chats started by Spool, including issue automation, do not count. Updates sync automatically about every 10 seconds.",
            )}
          >
            <Toggle
              label={t("Share local sessions")}
              value={!!draft.shared_local_chats}
              disabled={!draft.shared_chats}
              onChange={(shared_local_chats) =>
                setDraft({ ...draft, shared_local_chats })
              }
            />
          </Row>
        </Card>
        <Card title={t("Scheduled GitHub issues")}>
          <Row title={t("Enable issue automation")}>
            <Toggle
              label={t("Enable issue automation")}
              value={issue.enabled}
              onChange={(enabled) => update({ enabled })}
            />
          </Row>
          <Row
            title={
              repository
                ? t("GitHub repository (from origin): {repository}", {
                    repository,
                  })
                : t(
                    "Select a project directory to detect its GitHub repository.",
                  )
            }
          >
            <button
              type="button"
              disabled={!draft.path}
              onClick={async () => {
                const next = await run({
                  kind: "check-repository",
                  path: draft.path,
                });
                if (next?.repository) setRepository(next.repository);
              }}
            >
              <RefreshCw size={14} />
              {t("Recheck project Git")}
            </button>
          </Row>
          {issue.enabled && (
            <>
              <TextField
                title={t("Run interval (minutes, 1–10080)")}
                type="number"
                min={1}
                max={10080}
                value={issue.interval_minutes}
                onChange={(v) => update({ interval_minutes: Number(v) })}
                required
              />
              <Row
                title={t("Issue labels")}
                help={t(
                  "Labels are optional and empty by default: ignore issue labels and run one task at a time per project. Add any labels to run one task per label; matching is exact, and multi-label issues reserve all matching labels. Running tasks continue across check intervals.",
                )}
                wide
              >
                <div className="settings-labels">
                  {(issue.labels ?? []).map((value, i) => (
                    <div key={i}>
                      <input
                        value={value}
                        aria-label={`${t("Issue label")} ${i + 1}`}
                        onChange={(e) =>
                          update({
                            labels: issue.labels.map((old, index) =>
                              index === i ? e.target.value : old,
                            ),
                          })
                        }
                      />
                      <button
                        type="button"
                        className="icon"
                        aria-label={`${t("Remove label")} ${value}`}
                        title={`${t("Remove label")} ${value}`}
                        onClick={() =>
                          update({
                            labels: issue.labels.filter(
                              (_, index) => index !== i,
                            ),
                          })
                        }
                      >
                        <X size={16} />
                      </button>
                    </div>
                  ))}
                  <div>
                    <input
                      value={label}
                      aria-label={t("New issue label")}
                      onChange={(e) => setLabel(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          if (label.trim()) {
                            update({
                              labels: [...(issue.labels ?? []), label.trim()],
                            });
                            setLabel("");
                          }
                        }
                      }}
                    />
                    <button
                      type="button"
                      disabled={!label.trim()}
                      onClick={() => {
                        update({
                          labels: [...(issue.labels ?? []), label.trim()],
                        });
                        setLabel("");
                      }}
                    >
                      {t("Add label")}
                    </button>
                  </div>
                </div>
              </Row>
              <Row title={t("Automation agent")}>
                <select
                  aria-label={t("Automation agent")}
                  value={issue.backend}
                  onChange={(e) =>
                    update({
                      backend: e.target.value as typeof issue.backend,
                      model: "",
                      effort: "",
                      permission: "",
                    })
                  }
                >
                  <option value="codex">Codex</option>
                  <option value="claude">Claude Code</option>
                </select>
              </Row>
              <Row
                title={t("Task assignees (all can control task chats)")}
                help={t(
                  "Assignees can send messages, stop responses and answer approvals. Changes also apply to existing task chats; normal chats stay owner-only.",
                )}
                wide
              >
                <div className="settings-check-list">
                  {people
                    .filter((person) => draft.members.includes(person.id))
                    .map((person) => (
                      <label key={person.id}>
                        <input
                          type="checkbox"
                          checked={(issue.assignee_ids ?? []).includes(
                            person.id,
                          )}
                          onChange={(e) => {
                            const ids = e.target.checked
                              ? [...(issue.assignee_ids ?? []), person.id]
                              : (issue.assignee_ids ?? []).filter(
                                  (id) => id !== person.id,
                                );
                            update({
                              assignee_ids: ids,
                              user_id: ids.includes(issue.user_id)
                                ? issue.user_id
                                : (ids[0] ?? ""),
                            });
                          }}
                        />
                        {person.nickname}
                        <small>{person.username}</small>
                      </label>
                    ))}
                </div>
              </Row>
              <TextField
                title={t("Automation model (blank = agent default)")}
                value={issue.model || ""}
                onChange={(model) => update({ model })}
              />
              <TextField
                title={t("Automation reasoning level (blank = default)")}
                value={issue.effort || ""}
                onChange={(effort) => update({ effort })}
              />
              <Row title={t("Automation permissions")}>
                <select
                  aria-label={t("Automation permissions")}
                  value={issue.permission || ""}
                  onChange={(e) => update({ permission: e.target.value })}
                >
                  {[
                    "",
                    ...(issue.backend === "codex"
                      ? ["ask", "auto"]
                      : [
                          "manual",
                          "auto",
                          "acceptEdits",
                          "plan",
                          "dontAsk",
                          "bypassPermissions",
                        ]),
                  ].map((value) => (
                    <option key={value} value={value}>
                      {value || t("Default")}
                    </option>
                  ))}
                </select>
              </Row>
            </>
          )}
        </Card>
        {issue.enabled && (
          <div className="settings-info">
            <ShieldCheck size={17} />
            <div>
              <p>
                {t(
                  "Each actionable run starts a new chat, prioritizing unfinished work. For new issues, the agent creates a separate codex/issue-N-fix branch from the latest dev and works in a safe checkout/worktree.",
                )}
              </p>
              <p>
                {t(
                  "Only after validation passes does the agent push the fix branch and create or reuse a PR targeting dev. PRs are not merged automatically.",
                )}
              </p>
              <p>
                {t(
                  "Interrupted or failed work retains its branch, worktree and progress for a later run. No catch-up runs while the service is stopped.",
                )}
              </p>
              <p>
                {t(
                  "Requires gh login, a GitHub.com origin and a remote dev branch. Filter by configured labels, or leave empty to consider all issues. Shared ledger: data/projects/<encoded-working-directory>/issues/issues.json; each task writes isolated progress for Go to validate and merge.",
                )}
              </p>
            </div>
          </div>
        )}
        {live?.next_run ? (
          <Hint>
            {t("Next issue check: {time}", {
              time: new Date(live.next_run).toLocaleString(),
            })}
          </Hint>
        ) : null}
        {live?.last_chat && (
          <Hint>{t("Last automation chat: {id}", { id: live.last_chat })}</Hint>
        )}
        {live?.last_error && (
          <div className="settings-error">
            {knownMessage(live.last_error, t)}
          </div>
        )}
        <Hint>
          {t(
            "Members share the actual directory and its filesystem permissions. A project's directory cannot change after chats exist.",
          )}
        </Hint>
        <Save title={t("Save project")} />
      </form>
    </>
  );
}
