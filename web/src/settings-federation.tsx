import { ArrowUpRight } from "lucide-react";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import type { Translator } from "./i18n";
import { SegmentedTabs } from "./segmented-tabs";
import {
  settingsCall,
  type ManagedProject,
  type Person,
  type SettingsSnapshot,
} from "./settings-client";
import "./server-connection-tabs.css";
import {
  Card,
  CopyAddressButton,
  Hint,
  Row,
  SettingsError,
  Toggle,
} from "./settings-ui";

const routeLabels = {
  https: "Public HTTPS",
  tailscale: "Tailscale",
  relay: "Spool Relay",
} as const;
type RouteKind = keyof typeof routeLabels;
type Route = { transport: RouteKind | ""; address: string };
type TrustPeer = {
  id: string;
  name: string;
  incoming: boolean;
  outgoing: boolean;
  accept: boolean;
  users?: string[];
  all_users?: boolean;
};
type Preview = { id: string; name: string; accepting: boolean };
type DefaultProject = {
  id: string;
  access: "read-only" | "control";
  own_access: "read-only" | "create";
};
type TrustRequest = {
  id: string;
  peer_id: string;
  peer_name?: string;
  outgoing: boolean;
  network?: boolean;
  expires_at: number;
  status: "pending" | "approved" | "rejected";
};
type TrustSnapshot = {
  server_id: string;
  inbox_open: boolean;
  requests: TrustRequest[];
  peers: TrustPeer[];
};
type TrustAction = "list" | "reject" | "open-inbox" | "close-inbox" | "check";
const settings = (action: TrustAction, value = "") =>
  settingsCall<TrustSnapshot>("FederationTrust", action, value);

export function ServerTrustPage({
  t,
  data: local,
}: {
  t: Translator;
  data?: Pick<SettingsSnapshot, "projects" | "users" | "status">;
}) {
  const [data, setData] = useState<TrustSnapshot>();
  const [route, setRoute] = useState<Route>({
    transport: "https",
    address: "",
  });
  const [preview, setPreview] = useState<Preview>();
  const [requestingLogin, setRequestingLogin] = useState(false);
  const [loginError, setLoginError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef(false);
  const run = async (fn: () => Promise<void>, refreshOnError = false) => {
    if (operation.current) return false;
    operation.current = true;
    setBusy(true);
    setError("");
    try {
      await fn();
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      if (refreshOnError) {
        try {
          setData(await settings("list"));
        } catch {
          /* 保留原错误。 */
        }
      }
      return false;
    } finally {
      operation.current = false;
      setBusy(false);
    }
  };
  const act = (action: TrustAction, value = "") =>
    run(async () => setData(await settings(action, value)));
  useEffect(() => {
    void act("list");
  }, []);
  const outgoing =
    data?.requests.filter(
      (r) =>
        r.outgoing &&
        (r.status === "rejected" || (r.status === "pending" && r.network)),
    ) ?? [];
  const tailnet = local?.status.TSNet;
  const tailnetReady = !!local?.status.TSNetOutboundReady;
  const signIn = async () => {
    setRequestingLogin(true);
    setLoginError("");
    try {
      await settingsCall<boolean>("TSNetLogin");
    } catch (e) {
      setLoginError(e instanceof Error ? e.message : String(e));
    } finally {
      setRequestingLogin(false);
    }
  };

  return (
    <>
      <SettingsError message={error} t={t} onDismiss={() => setError("")} />
      {!data ? (
        busy ? (
          <Hint>{t("Working…")}</Hint>
        ) : (
          <button type="button" onClick={() => void act("list")}>
            {t("Retry")}
          </button>
        )
      ) : (
        <fieldset
          className="settings-page-controls"
          disabled={busy}
          aria-busy={busy}
        >
          <Card>
            <Row
              title={t("This server’s ID")}
              help={t("Share this ID with the other administrator.")}
              wide
            >
              <code>{data.server_id}</code>
              <CopyAddressButton
                t={t}
                kind="copy-server-trust-id"
                label={t("Copy server ID")}
              />
            </Row>
          </Card>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (preview || (route.transport === "tailscale" && !tailnetReady))
                return;
              void run(async () =>
                setPreview(
                  await settingsCall<Preview>(
                    "PreviewFederationTrust",
                    route.transport,
                    route.address,
                  ),
                ),
              );
            }}
          >
            <Card title={t("Request trust")}>
              <RouteFields
                t={t}
                route={route}
                onChange={(v) => {
                  setRoute(v);
                  setPreview(undefined);
                }}
              >
                {route.transport === "tailscale" && (
                  <>
                    <Row
                      title={t("Local Tailscale node")}
                      help={t(
                        "Sign in to connect this server to the tailnet. This does not sign you in to the destination Spool server.",
                      )}
                      wide
                    >
                      {tailnetReady ? (
                        <span role="status" className="settings-state online">
                          <span className="settings-status-dot" />
                          {t("Connected")}
                        </span>
                      ) : tailnet?.State === "NeedsMachineAuth" ? (
                        <span role="status">
                          {t("Waiting for device approval")}
                        </span>
                      ) : (
                        <button
                          type="button"
                          disabled={requestingLogin}
                          onClick={() => void signIn()}
                        >
                          {t(
                            requestingLogin
                              ? "Getting login link..."
                              : "Sign in to Tailscale",
                          )}
                          <ArrowUpRight size={14} aria-hidden="true" />
                        </button>
                      )}
                    </Row>
                    <SettingsError
                      message={loginError}
                      t={t}
                      onDismiss={() => setLoginError("")}
                    />
                  </>
                )}
              </RouteFields>
              {!preview && (
                <div className="settings-card-actions">
                  <button
                    type="submit"
                    className="settings-primary"
                    disabled={
                      !route.address.trim() ||
                      (route.transport === "tailscale" && !tailnetReady)
                    }
                  >
                    {t("Connect and verify server")}
                  </button>
                </div>
              )}
              {preview && (
                <>
                  <Row
                    title={preview.name}
                    help={
                      <>
                        <code>{preview.id}</code>
                        <br />
                        {t("Verify this server ID before sending the request.")}
                      </>
                    }
                    wide
                  >
                    {!preview.accepting && (
                      <span>
                        {t(
                          "The destination is not accepting server trust applications",
                        )}
                      </span>
                    )}
                  </Row>
                  <div className="settings-card-actions">
                    <button
                      type="button"
                      className="settings-primary"
                      disabled={
                        !preview.accepting ||
                        (route.transport === "tailscale" && !tailnetReady)
                      }
                      onClick={() =>
                        void run(async () => {
                          setData(
                            await settingsCall<TrustSnapshot>(
                              "SubmitFederationTrust",
                              route.transport,
                              route.address,
                              preview.id,
                            ),
                          );
                          setPreview(undefined);
                          setRoute({ ...route, address: "" });
                        }, true)
                      }
                    >
                      {t("Send trust request")}
                    </button>
                    <button type="button" onClick={() => setPreview(undefined)}>
                      {t("Retry")}
                    </button>
                  </div>
                </>
              )}
            </Card>
          </form>
          {outgoing.length > 0 && (
            <Card title={t("Outgoing trust requests")}>
              {outgoing.map((r) => (
                <TrustRequestRow t={t} key={r.id} request={r}>
                  {r.status === "pending" && (
                    <button
                      type="button"
                      onClick={() => void act("check", r.id)}
                    >
                      {t("Check decision")}
                    </button>
                  )}
                </TrustRequestRow>
              ))}
            </Card>
          )}
          <Card title={t("Review incoming requests")}>
            <Row
              title={t("Accept online trust requests")}
              help={t(
                "Share one of this server’s connection routes with the requester.",
              )}
              wide
            >
              <Toggle
                label={t("Accept online trust requests")}
                value={data.inbox_open}
                onChange={(v) => void act(v ? "open-inbox" : "close-inbox")}
              />
              <button type="button" onClick={() => void act("list")}>
                {t("Refresh requests")}
              </button>
              {local?.status.Relay?.State === "connected" && (
                <CopyAddressButton
                  t={t}
                  kind="copy-server-trust-relay-route"
                  label={t("Copy trust Relay route")}
                />
              )}
            </Row>
            {data.requests
              .filter((r) => !r.outgoing)
              .map((r) => (
                <IncomingRequest
                  key={r.id}
                  request={r}
                  projects={local?.projects ?? []}
                  t={t}
                  reject={() => void act("reject", r.id)}
                  approve={(defaults, reverse) =>
                    void run(async () => {
                      setData(
                        await settingsCall<TrustSnapshot>(
                          "ApproveFederationTrust",
                          r.id,
                          { projects: defaults, ...reverse },
                        ),
                      );
                    })
                  }
                />
              ))}
          </Card>
          <Card title={t("Trusted servers")}>
            {data.peers.length === 0 && (
              <Row title={t("No trusted servers yet")} />
            )}
            {data.peers.map((peer) => (
              <TrustedServer
                key={peer.id}
                t={t}
                peer={peer}
                users={local?.users ?? []}
                saveReturnRoute={(route) =>
                  run(async () => {
                    setData(
                      await settingsCall<TrustSnapshot>(
                        "UpdateFederationTrustReturnRoute",
                        peer.id,
                        route.transport,
                        route.address,
                      ),
                    );
                  })
                }
                saveUsers={(all, people) =>
                  run(async () => {
                    setData(
                      await settingsCall<TrustSnapshot>(
                        "SetFederationTrustUsers",
                        peer.id,
                        all,
                        people,
                      ),
                    );
                  })
                }
              />
            ))}
          </Card>
        </fieldset>
      )}
    </>
  );
}

function RouteFields({
  t,
  route,
  onChange,
  optional,
  returnRoute,
  children,
}: {
  t: Translator;
  route: Route;
  onChange: (v: Route) => void;
  optional?: boolean;
  returnRoute?: boolean;
  children?: ReactNode;
}) {
  const id = useId();
  const drafts = useRef<Partial<Record<RouteKind, string>>>({});
  const title = t(
    optional || returnRoute
      ? "Route back to the requesting server"
      : "Connection method",
  );
  const kinds: Route["transport"][] = ["https", "tailscale", "relay"];
  if (optional) kinds.push("");
  return (
    <>
      <SegmentedTabs
        className="server-connection-tabs"
        label={title}
        value={route.transport}
        onChange={(kind) => {
          if (kind === route.transport) return;
          if (route.transport) drafts.current[route.transport] = route.address;
          onChange({
            transport: kind,
            address: kind ? (drafts.current[kind] ?? "") : "",
          });
        }}
        options={kinds.map((kind) => ({
          value: kind,
          label: t(kind ? routeLabels[kind] : "Configure later"),
          id: `${id}-tab-${kind}`,
          controls: `${id}-panel-${kind}`,
        }))}
      />
      {kinds.map((kind) => (
        <div
          key={kind}
          role="tabpanel"
          id={`${id}-panel-${kind}`}
          aria-labelledby={`${id}-tab-${kind}`}
          hidden={route.transport !== kind}
        >
          {route.transport === kind && kind && (
            <>
              <Row
                title={t(
                  kind === "relay"
                    ? "Trust Relay route"
                    : kind === "tailscale"
                      ? "Tailscale HTTPS address"
                      : "Public HTTPS address",
                )}
                wide
              >
                <input
                  aria-label={t(
                    kind === "relay"
                      ? "Trust Relay route"
                      : kind === "tailscale"
                        ? "Tailscale HTTPS address"
                        : "Public HTTPS address",
                  )}
                  placeholder={
                    kind === "relay"
                      ? "spool-pair:…"
                      : kind === "tailscale"
                        ? "https://spool.example.ts.net"
                        : "https://spool.example.com"
                  }
                  type={kind === "relay" ? "password" : "url"}
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={8192}
                  required
                  value={route.address}
                  onChange={(e) =>
                    onChange({ ...route, address: e.target.value })
                  }
                />
              </Row>
              {children}
            </>
          )}
        </div>
      ))}
    </>
  );
}

function TrustedServer({
  t,
  peer,
  users,
  saveReturnRoute,
  saveUsers,
}: {
  t: Translator;
  peer: TrustPeer;
  users: Person[];
  saveReturnRoute: (route: Route) => Promise<boolean>;
  saveUsers: (all: boolean, users: string[]) => Promise<boolean>;
}) {
  const [returnRoute, setReturnRoute] = useState<Route>();
  const [draft, setDraft] = useState<{ all: boolean; selected: string[] }>();
  const local = users.filter((person) => !person.source_server_id);
  const count = local.filter(
    (person) => !person.disabled && peer.users?.includes(person.id),
  ).length;
  return (
    <>
      <Row title={peer.name} help={<code>{peer.id}</code>} wide>
        {[
          peer.outgoing && t("This server → peer"),
          peer.incoming && t("Peer → this server"),
          !peer.outgoing && !peer.incoming && t("Manually configured"),
        ]
          .filter(Boolean)
          .join(" · ")}
        {peer.outgoing && !draft && (
          <button
            type="button"
            onClick={() =>
              setDraft({ all: !!peer.all_users, selected: peer.users ?? [] })
            }
          >
            {t("Manage allowed users")}
          </button>
        )}
      </Row>
      {peer.incoming && (
        <>
          <Row
            title={t(
              peer.accept
                ? "Cross-server sign-in enabled"
                : "Return route not configured",
            )}
            wide
          >
            {!returnRoute && (
              <button
                type="button"
                onClick={() =>
                  setReturnRoute({ transport: "https", address: "" })
                }
              >
                {t(
                  peer.accept
                    ? "Change return route"
                    : "Configure return route",
                )}
              </button>
            )}
          </Row>
          {returnRoute && (
            <form
              onSubmit={async (event) => {
                event.preventDefault();
                if (
                  returnRoute.address.trim() &&
                  (await saveReturnRoute(returnRoute))
                )
                  setReturnRoute(undefined);
              }}
            >
              <RouteFields
                t={t}
                route={returnRoute}
                onChange={setReturnRoute}
                returnRoute
              />
              <div className="settings-card-actions">
                <button
                  className="settings-primary"
                  type="submit"
                  disabled={!returnRoute.address.trim()}
                >
                  {t("Save return route")}
                </button>
                <button type="button" onClick={() => setReturnRoute(undefined)}>
                  {t("Cancel")}
                </button>
              </div>
            </form>
          )}
        </>
      )}
      {peer.outgoing &&
        (draft ? (
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              if (
                await saveUsers(
                  draft.all,
                  draft.all
                    ? []
                    : local
                        .filter((person) => draft.selected.includes(person.id))
                        .map((person) => person.id),
                )
              )
                setDraft(undefined);
            }}
          >
            <Row
              title={t("Allow all local users")}
              help={t(
                "Includes new local users. Disabled and external accounts cannot sign in to the target.",
              )}
              wide
            >
              <Toggle
                label={t("Allow all local users")}
                value={draft.all}
                onChange={(all) => setDraft({ ...draft, all })}
              />
            </Row>
            {!draft.all &&
              local.map((person) => (
                <Row
                  key={person.id}
                  title={person.nickname || person.username}
                  help={
                    person.disabled
                      ? `${person.username} · ${t("Disabled")}`
                      : person.username
                  }
                >
                  <input
                    type="checkbox"
                    aria-label={
                      person.nickname && person.nickname !== person.username
                        ? `${person.nickname} (${person.username})`
                        : person.username
                    }
                    checked={draft.selected.includes(person.id)}
                    disabled={
                      person.disabled && !draft.selected.includes(person.id)
                    }
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        selected: e.target.checked
                          ? [...draft.selected, person.id]
                          : draft.selected.filter((id) => id !== person.id),
                      })
                    }
                  />
                </Row>
              ))}
            <div className="settings-card-actions">
              <button className="settings-primary" type="submit">
                {t("Save allowed users")}
              </button>
              <button type="button" onClick={() => setDraft(undefined)}>
                {t("Cancel")}
              </button>
            </div>
          </form>
        ) : (
          <Hint>
            {peer.all_users
              ? t("All enabled local users are allowed.")
              : t("Allowed users: {count}", { count: String(count) })}
          </Hint>
        ))}
    </>
  );
}

function TrustRequestRow({
  t,
  request: r,
  children,
}: {
  t: Translator;
  request: TrustRequest;
  children?: React.ReactNode;
}) {
  const status = t(
    r.status === "pending"
      ? "Awaiting approval"
      : r.status === "approved"
        ? "Trust approved"
        : "Trust rejected",
  );
  return (
    <Row
      title={r.peer_name || t("Destination server ID")}
      wide
      help={
        <>
          <code>{r.peer_id}</code>
          <br />
          {status}
          {r.status === "pending" &&
            ` · ${t("Request expires {time}", {
              time: new Date(r.expires_at * 1000).toLocaleString(),
            })}`}
        </>
      }
    >
      {children}
    </Row>
  );
}

function IncomingRequest({
  t,
  request,
  projects,
  approve,
  reject,
}: {
  t: Translator;
  request: TrustRequest;
  projects: ManagedProject[];
  approve: (p: DefaultProject[], route: Route) => void;
  reject: () => void;
}) {
  const [review, setReview] = useState(false);
  const [selected, setSelected] = useState<Record<string, DefaultProject>>({});
  const [route, setRoute] = useState<Route>({ transport: "", address: "" });
  const active = projects.filter((p) => !p.archived);
  const update = (p: DefaultProject) => setSelected({ ...selected, [p.id]: p });
  return (
    <>
      <TrustRequestRow t={t} request={request}>
        {request.status === "pending" && (
          <>
            {!review && (
              <button type="button" onClick={() => setReview(true)}>
                {t("Review trust request")}
              </button>
            )}
            <button type="button" onClick={reject}>
              {t("Reject request")}
            </button>
          </>
        )}
      </TrustRequestRow>
      {review && request.status === "pending" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            approve(
              active.flatMap((p) => (selected[p.id] ? [selected[p.id]] : [])),
              route,
            );
          }}
        >
          <Row
            title={t("Default projects for new users")}
            help={t(
              "These permissions apply the first time an approved person signs in.",
            )}
            wide
          />
          {active.map((p) => {
            const rule = selected[p.id];
            return (
              <div key={p.id}>
                <Row title={p.name}>
                  <input
                    type="checkbox"
                    aria-label={p.name}
                    checked={!!rule}
                    onChange={(e) => {
                      if (e.target.checked)
                        update({
                          id: p.id,
                          access: "read-only",
                          own_access: "read-only",
                        });
                      else {
                        const next = { ...selected };
                        delete next[p.id];
                        setSelected(next);
                      }
                    }}
                  />
                </Row>
                {rule && (
                  <Row title={t("Project permissions")} wide>
                    <label>
                      {t("Shared session permissions")}{" "}
                      <select
                        aria-label={`${p.name} · ${t("Shared session permissions")}`}
                        value={rule.access}
                        onChange={(e) =>
                          update({
                            ...rule,
                            access: e.target.value as DefaultProject["access"],
                          })
                        }
                      >
                        <option value="read-only">{t("Read-only")}</option>
                        <option value="control">
                          {t("Can control sessions")}
                        </option>
                      </select>
                    </label>
                    <label>
                      {t("In-app permissions")}{" "}
                      <select
                        aria-label={`${p.name} · ${t("In-app permissions")}`}
                        value={rule.own_access}
                        onChange={(e) =>
                          update({
                            ...rule,
                            own_access: e.target
                              .value as DefaultProject["own_access"],
                          })
                        }
                      >
                        <option value="read-only">{t("Read-only")}</option>
                        <option value="create">{t("Can create")}</option>
                      </select>
                    </label>
                  </Row>
                )}
              </div>
            );
          })}
          <Hint>
            {t(
              "To accept sign-ins now, provide a route back to the requesting server. Otherwise configure it later.",
            )}
          </Hint>
          <RouteFields t={t} route={route} optional onChange={setRoute} />
          <div className="settings-card-actions">
            <button className="settings-primary" type="submit">
              {t("Approve trust")}
            </button>
            <button type="button" onClick={() => setReview(false)}>
              {t("Cancel")}
            </button>
          </div>
        </form>
      )}
    </>
  );
}
