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
  revoked?: boolean;
  sync_pending?: boolean;
  users?: string[];
  all_users?: boolean;
  projects?: DefaultProject[];
  revocations?: {
    outgoing: boolean;
    initiator: string;
    recorded_at: number;
    sync_pending?: boolean;
  }[];
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
  return_transport?: string;
  expires_at: number;
  status: "pending" | "approved" | "rejected";
};
type TrustSnapshot = {
  server_id: string;
  inbox_open: boolean;
  requests: TrustRequest[];
  peers: TrustPeer[];
};
type TrustAction =
  | "list"
  | "reject"
  | "revoke"
  | "withdraw"
  | "open-inbox"
  | "close-inbox"
  | "check";
const settings = (action: TrustAction, value = "") =>
  settingsCall<TrustSnapshot>("FederationTrust", action, value);

export function ServerTrustPage({
  t,
  data: local,
}: {
  t: Translator;
  data?: Pick<SettingsSnapshot, "projects" | "users" | "status" | "settings">;
}) {
  const [data, setData] = useState<TrustSnapshot>();
  const [route, setRoute] = useState<Route>({
    transport: "https",
    address: "",
  });
  const [preview, setPreview] = useState<Preview>();
  const [reverse, setReverse] = useState<"https" | "relay" | "">("");
  const publicReturnURL = configuredPublicReturnURL(local?.settings);
  const relayReturn = !!local?.settings.relay_enabled;
  const reverseKinds = (["https", "relay"] as const).filter((kind) =>
    kind === "https" ? !!publicReturnURL : relayReturn,
  );
  const reverseKind =
    reverse && reverseKinds.includes(reverse)
      ? reverse
      : publicReturnURL
        ? "https"
        : relayReturn
          ? "relay"
          : "";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef(false);
  const revision = useRef(0);
  const run = async (fn: () => Promise<void>, refreshOnError = false) => {
    if (operation.current) return false;
    operation.current = true;
    revision.current++;
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
    let active = true;
    let reading = false;
    const timer = window.setInterval(async () => {
      if (reading || operation.current || document.hidden) return;
      reading = true;
      const before = revision.current;
      try {
        const next = await settings("list");
        if (active && !operation.current && revision.current === before)
          setData(next);
      } catch {
        // A background refresh must not interrupt an open editor.
      } finally {
        reading = false;
      }
    }, 1000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);
  const outgoing =
    data?.requests.filter(
      (r) =>
        r.outgoing &&
        (r.status === "rejected" || (r.status === "pending" && r.network)),
    ) ?? [];
  const activePeers =
    data?.peers.filter(
      (peer) => !peer.revoked || peer.incoming || peer.outgoing,
    ) ?? [];
  const revokedPeers =
    data?.peers.filter(
      (peer) => peer.revoked && !peer.incoming && !peer.outgoing,
    ) ?? [];
  const renderPeer = (peer: TrustPeer) => (
    <TrustedServer
      key={peer.id}
      t={t}
      peer={peer}
      localServerID={data?.server_id ?? ""}
      users={local?.users ?? []}
      projects={local?.projects ?? []}
      revoke={(outgoing) => act(outgoing ? "withdraw" : "revoke", peer.id)}
      saveProjects={(projects) =>
        run(async () => {
          setData(
            await settingsCall<TrustSnapshot>(
              "SetFederationTrustProjects",
              peer.id,
              projects,
            ),
          );
        })
      }
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
  );

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
                iconOnly
              />
            </Row>
          </Card>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (preview) return;
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
              />
              <div role="group" aria-label={t("Return connection")}>
                <Hint>
                  {t(
                    "Choose how the destination connects back to this server. Approval saves this return route automatically.",
                  )}
                </Hint>
                {reverseKinds.length ? (
                  <>
                    <SegmentedTabs
                      className="server-connection-tabs"
                      label={t("Return connection")}
                      value={reverseKind}
                      onChange={setReverse}
                      options={reverseKinds.map((kind) => ({
                        value: kind,
                        label: t(routeLabels[kind]),
                      }))}
                    />
                    {reverseKind === "https" ? (
                      <Row title={t("Public HTTPS address")} wide>
                        <code>{publicReturnURL}</code>
                      </Row>
                    ) : (
                      <Hint>
                        {t(
                          "This server’s trust Relay route will be included automatically.",
                        )}
                      </Hint>
                    )}
                  </>
                ) : (
                  <Hint>
                    {t(
                      "Configure a Public HTTPS address or enable Relay in Service settings to include a return connection.",
                    )}
                  </Hint>
                )}
              </div>
              {!preview && (
                <div className="settings-card-actions">
                  <button
                    type="submit"
                    className="settings-primary"
                    disabled={!route.address.trim()}
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
                      disabled={!preview.accepting}
                      onClick={() =>
                        void run(async () => {
                          setData(
                            await settingsCall<TrustSnapshot>(
                              "SubmitFederationTrust",
                              route.transport,
                              route.address,
                              preview.id,
                              { transport: reverseKind, address: "" },
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
                  defaults={
                    data.peers.find((peer) => peer.id === r.peer_id)
                      ?.projects ?? []
                  }
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
          <div className="settings-trust-lists">
            <Card title={t("Trusted servers")}>
              {activePeers.length === 0 && (
                <Row
                  title={t(
                    revokedPeers.length
                      ? "No active server trust"
                      : "No trusted servers yet",
                  )}
                />
              )}
              {activePeers.map(renderPeer)}
            </Card>
            {revokedPeers.length > 0 && (
              <details className="settings-group settings-trust-revoked">
                <summary>
                  <span>
                    {t("Revoked trust ({count})", {
                      count: revokedPeers.length,
                    })}
                  </span>
                  {revokedPeers.some((peer) => peer.sync_pending) && (
                    <span className="settings-trust-status">
                      {" "}
                      · {t("Waiting to sync revocation")}
                    </span>
                  )}
                </summary>
                <div className="settings-card">
                  {revokedPeers.map(renderPeer)}
                </div>
              </details>
            )}
          </div>
        </fieldset>
      )}
    </>
  );
}

function configuredPublicReturnURL(
  settings?: SettingsSnapshot["settings"],
): string {
  if (!settings?.web_enabled || !settings.public_url) return "";
  try {
    const url = new URL(settings.public_url);
    return url.protocol === "https:" &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash &&
      url.pathname === "/"
      ? url.origin
      : "";
  } catch {
    return "";
  }
}

function RouteFields({
  t,
  route,
  onChange,
  optional,
  returnRoute,
  automaticRoute,
  children,
}: {
  t: Translator;
  route: Route;
  onChange: (v: Route) => void;
  optional?: boolean;
  returnRoute?: boolean;
  automaticRoute?: boolean;
  children?: ReactNode;
}) {
  const id = useId();
  const drafts = useRef<Partial<Record<RouteKind, string>>>({});
  const title = t(
    optional || returnRoute
      ? "Route back to the requesting server"
      : "Connection method",
  );
  const kinds: Route["transport"][] = ["https", "relay"];
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
          label: t(
            kind
              ? routeLabels[kind]
              : automaticRoute
                ? "Use requester’s return route"
                : "Configure later",
          ),
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
  localServerID,
  users,
  projects,
  saveProjects,
  saveReturnRoute,
  saveUsers,
  revoke,
}: {
  t: Translator;
  peer: TrustPeer;
  localServerID: string;
  users: Person[];
  projects: ManagedProject[];
  saveProjects: (projects: DefaultProject[]) => Promise<boolean>;
  saveReturnRoute: (route: Route) => Promise<boolean>;
  saveUsers: (all: boolean, users: string[]) => Promise<boolean>;
  revoke: (outgoing: boolean) => Promise<boolean>;
}) {
  const [returnRoute, setReturnRoute] = useState<Route>();
  const [projectDraft, setProjectDraft] = useState<DefaultProject[]>();
  const [draft, setDraft] = useState<{ all: boolean; selected: string[] }>();
  const [confirmRevocation, setConfirmRevocation] = useState(false);
  const [revokeOutgoing, setRevokeOutgoing] = useState(!peer.incoming);
  const outgoing = revokeOutgoing;
  const confirming =
    confirmRevocation && (outgoing ? peer.outgoing : peer.incoming);
  const pendingReturnRoute = !!peer.revocations?.some(
    (record) =>
      !record.outgoing &&
      record.initiator === localServerID &&
      record.sync_pending,
  );
  useEffect(() => {
    if (!peer.incoming && !pendingReturnRoute) setReturnRoute(undefined);
  }, [peer.incoming, pendingReturnRoute]);
  useEffect(() => {
    if (!peer.incoming) setProjectDraft(undefined);
  }, [peer.incoming]);
  useEffect(() => {
    if (confirmRevocation && !(outgoing ? peer.outgoing : peer.incoming))
      setConfirmRevocation(false);
  }, [confirmRevocation, outgoing, peer.incoming, peer.outgoing]);
  const local = users.filter((person) => !person.source_server_id);
  const count = local.filter(
    (person) => !person.disabled && peer.users?.includes(person.id),
  ).length;
  return (
    <>
      <Row title={peer.name} help={<code>{peer.id}</code>} wide>
        <div className="settings-trust-directions">
          {([true, false] as const).map((isOutgoing) => {
            if (!(isOutgoing ? peer.outgoing : peer.incoming)) return null;
            const direction = t(
              isOutgoing ? "This server → peer" : "Peer → this server",
            );
            return (
              <div
                key={String(isOutgoing)}
                className="settings-trust-controls"
                role="group"
                aria-label={direction}
              >
                <span className="settings-trust-direction">{direction}</span>
                <div className="settings-trust-details">
                  {isOutgoing ? (
                    !draft && (
                      <button
                        type="button"
                        onClick={() =>
                          setDraft({
                            all: !!peer.all_users,
                            selected: peer.users ?? [],
                          })
                        }
                      >
                        {t("Manage allowed users")}
                      </button>
                    )
                  ) : (
                    <>
                      {!projectDraft && (
                        <button
                          type="button"
                          onClick={() =>
                            setProjectDraft(
                              activeDefaults(projects, peer.projects ?? []),
                            )
                          }
                        >
                          {t("Manage default projects")}
                        </button>
                      )}
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
                    </>
                  )}
                  <span className="settings-trust-status">
                    {isOutgoing
                      ? peer.all_users
                        ? t("All enabled local users are allowed.")
                        : t("Allowed users: {count}", { count: String(count) })
                      : t(
                          peer.accept
                            ? "Cross-server sign-in enabled"
                            : "Return route not configured",
                        )}
                  </span>
                </div>
                {!(confirming && outgoing === isOutgoing) && (
                  <button
                    type="button"
                    className="settings-destructive settings-trust-revoke"
                    onClick={() => {
                      setRevokeOutgoing(isOutgoing);
                      setConfirmRevocation(true);
                    }}
                  >
                    {t("Revoke trust")}
                  </button>
                )}
              </div>
            );
          })}
          {((!peer.outgoing && !peer.incoming) || peer.sync_pending) && (
            <div className="settings-trust-details">
              {pendingReturnRoute && !peer.incoming && !returnRoute && (
                <button
                  type="button"
                  onClick={() =>
                    setReturnRoute({ transport: "https", address: "" })
                  }
                >
                  {t("Configure return route")}
                </button>
              )}
              <span className="settings-trust-status">
                {[
                  !peer.outgoing &&
                    !peer.incoming &&
                    t(peer.revoked ? "Trust revoked" : "Manually configured"),
                  peer.revoked &&
                    t("Approve a new trust request to restore access."),
                  peer.sync_pending && t("Waiting to sync revocation"),
                ]
                  .filter(Boolean)
                  .map((status, index) => (
                    <span key={index}>{status}</span>
                  ))}
              </span>
            </div>
          )}
          {!peer.incoming &&
            !peer.outgoing &&
            peer.revocations?.map((record) => (
              <div
                className="settings-trust-controls"
                key={String(record.outgoing)}
              >
                <span className="settings-trust-direction">
                  {t(
                    record.outgoing
                      ? "This server → peer"
                      : "Peer → this server",
                  )}
                </span>
                <span
                  className="settings-trust-status"
                  title={record.initiator}
                >
                  {t(
                    record.initiator === localServerID
                      ? "Revoked by this server"
                      : "Revoked by peer",
                  )}
                </span>
              </div>
            ))}
        </div>
      </Row>
      {peer.incoming && projectDraft && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            if (await saveProjects(activeDefaults(projects, projectDraft)))
              setProjectDraft(undefined);
          }}
        >
          <DefaultProjects
            t={t}
            projects={projects}
            value={projectDraft}
            onChange={setProjectDraft}
          />
          <div className="settings-card-actions">
            <button className="settings-primary" type="submit">
              {t("Save default projects")}
            </button>
            <button type="button" onClick={() => setProjectDraft(undefined)}>
              {t("Cancel")}
            </button>
          </div>
        </form>
      )}
      {confirming && !peer.revoked && (
        <Row
          title={t("Revoke trust with {name}?", {
            name: peer.name,
          })}
          help={t(
            "Revokes {direction} and immediately notifies the peer. If unreachable, synchronization is retried. The reverse direction is preserved. New approval is required to restore access.",
            {
              direction: t(
                outgoing ? "This server → peer" : "Peer → this server",
              ),
            },
          )}
          wide
        >
          <button type="button" onClick={() => setConfirmRevocation(false)}>
            {t("Cancel")}
          </button>
          <button
            type="button"
            className="settings-destructive"
            onClick={async () => {
              if (await revoke(outgoing)) {
                setConfirmRevocation(false);
                if (outgoing) setDraft(undefined);
                else setReturnRoute(undefined);
              }
            }}
          >
            {t("Confirm revocation")}
          </button>
        </Row>
      )}
      {(peer.incoming || pendingReturnRoute) && returnRoute && (
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
      {peer.outgoing && draft && (
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
                <Toggle
                  label={
                    person.nickname && person.nickname !== person.username
                      ? `${person.nickname} (${person.username})`
                      : person.username
                  }
                  value={draft.selected.includes(person.id)}
                  disabled={
                    person.disabled && !draft.selected.includes(person.id)
                  }
                  onChange={(allowed) =>
                    setDraft({
                      ...draft,
                      selected: allowed
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
      )}
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
  defaults,
  approve,
  reject,
}: {
  t: Translator;
  request: TrustRequest;
  projects: ManagedProject[];
  defaults: DefaultProject[];
  approve: (p: DefaultProject[], route: Route) => void;
  reject: () => void;
}) {
  const [review, setReview] = useState(false);
  const [selected, setSelected] = useState<DefaultProject[]>([]);
  const [route, setRoute] = useState<Route>({ transport: "", address: "" });
  return (
    <>
      <TrustRequestRow t={t} request={request}>
        {request.status === "pending" && (
          <>
            {!review && (
              <button
                type="button"
                onClick={() => {
                  setSelected(activeDefaults(projects, defaults));
                  setReview(true);
                }}
              >
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
            approve(activeDefaults(projects, selected), route);
          }}
        >
          <DefaultProjects
            t={t}
            projects={projects}
            value={selected}
            onChange={setSelected}
          />
          <Hint>
            {t(
              request.return_transport
                ? "The request includes a verified return route. Approval saves it automatically."
                : "To accept sign-ins now, provide a route back to the requesting server. Otherwise configure it later.",
            )}
          </Hint>
          <RouteFields
            t={t}
            route={route}
            optional
            automaticRoute={!!request.return_transport}
            onChange={setRoute}
          />
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

function activeDefaults(
  projects: ManagedProject[],
  defaults: DefaultProject[],
) {
  return projects.flatMap((project) => {
    const rule = defaults.find((item) => item.id === project.id);
    return !project.archived && rule ? [rule] : [];
  });
}

function DefaultProjects({
  t,
  projects,
  value,
  onChange,
}: {
  t: Translator;
  projects: ManagedProject[];
  value: DefaultProject[];
  onChange: (value: DefaultProject[]) => void;
}) {
  const update = (rule: DefaultProject) =>
    onChange([...value.filter((item) => item.id !== rule.id), rule]);
  return (
    <>
      <Row
        title={t("Default projects for new users")}
        help={t(
          "Defaults apply only on a new user's first sign-in. Changes do not affect existing users' projects or permissions.",
        )}
        wide
      />
      {projects
        .filter((project) => !project.archived)
        .map((project) => {
          const rule = value.find((item) => item.id === project.id);
          return (
            <Row key={project.id} title={project.name}>
              {rule && (
                <div className="settings-member-permissions">
                  <div className="settings-member-permission">
                    <span>{t("In-app permissions")}</span>
                    <div
                      className="settings-member-access"
                      role="group"
                      aria-label={`${project.name} · ${t("In-app permissions")}`}
                    >
                      {(["read-only", "create"] as const).map((access) => (
                        <button
                          key={access}
                          type="button"
                          aria-pressed={rule.own_access === access}
                          onClick={() =>
                            update({ ...rule, own_access: access })
                          }
                        >
                          {t(
                            access === "read-only" ? "Read-only" : "Can create",
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
                      aria-label={`${project.name} · ${t("Shared session permissions")}`}
                    >
                      {(["read-only", "control"] as const).map((access) => (
                        <button
                          key={access}
                          type="button"
                          aria-pressed={rule.access === access}
                          onClick={() => update({ ...rule, access })}
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
                label={project.name}
                value={!!rule}
                onChange={(enabled) => {
                  if (enabled)
                    update({
                      id: project.id,
                      access: "read-only",
                      own_access: "read-only",
                    });
                  else onChange(value.filter((item) => item.id !== project.id));
                }}
              />
            </Row>
          );
        })}
    </>
  );
}
