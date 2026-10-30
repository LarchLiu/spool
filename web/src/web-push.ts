import { api } from "./api";
import type { MessageKey } from "./i18n";
import type { Locale } from "./preferences";

export class PushError extends Error {
  constructor(public key: MessageKey) {
    super(key);
  }
}

export function pushSupport(): "supported" | "insecure" | "unsupported" {
  if (!window.isSecureContext) return "insecure";
  return "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
    ? "supported"
    : "unsupported";
}

export function applicationServerKey(value: string): Uint8Array<ArrayBuffer> {
  const binary = atob(value.replaceAll("-", "+").replaceAll("_", "/"));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

function usesKey(subscription: PushSubscription, key: string): boolean {
  const existing = subscription.options.applicationServerKey;
  if (!existing) return false;
  const expected = applicationServerKey(key);
  const actual = new Uint8Array(existing);
  return (
    actual.length === expected.length &&
    actual.every((v, i) => v === expected[i])
  );
}

function subscriptionBody(subscription: PushSubscription) {
  const value = subscription.toJSON();
  return { endpoint: subscription.endpoint, keys: value.keys };
}

async function saveSubscription(
  subscription: PushSubscription,
  language: Locale,
  signal?: AbortSignal,
) {
  await api(
    "/push/subscribe",
    { subscription: subscriptionBody(subscription), language },
    signal,
  );
}

export async function currentPushSubscription() {
  if (pushSupport() !== "supported") return null;
  const registration = await navigator.serviceWorker.getRegistration("/");
  return registration?.pushManager.getSubscription() ?? null;
}

// Refresh an existing opt-in without reviving a disabled or signed-out device.
// Never request permission or create a subscription on page load.
export async function syncExistingPush(language: Locale, signal?: AbortSignal) {
  if (pushSupport() !== "supported" || Notification.permission !== "granted")
    return;
  const subscription = await currentPushSubscription();
  if (!subscription || signal?.aborted) return;
  await api(
    "/push/refresh",
    { endpoint: subscription.endpoint, language },
    signal,
  );
}

export async function pushEnabled() {
  const subscription = await currentPushSubscription();
  if (!subscription || Notification.permission !== "granted") return false;
  const status = await api<{ subscribed: boolean }>("/push/status", {
    endpoint: subscription.endpoint,
  });
  return status.subscribed;
}

function workerState(worker: ServiceWorker, states: ServiceWorkerState[]) {
  return new Promise<void>((resolve, reject) => {
    const cleanup = () => {
      clearTimeout(timeout);
      worker.removeEventListener("statechange", changed);
    };
    const changed = () => {
      if (states.includes(worker.state)) {
        cleanup();
        resolve();
      } else if (worker.state === "redundant") {
        cleanup();
        reject(new PushError("Could not start notification service"));
      }
    };
    const timeout = setTimeout(() => {
      cleanup();
      reject(new PushError("Could not start notification service"));
    }, 15000);
    worker.addEventListener("statechange", changed);
    changed();
  });
}

export async function enablePush(language: Locale) {
  if (pushSupport() !== "supported")
    throw new PushError(
      "Push notifications are not supported in this browser. On iPhone or iPad, add Spool to the Home Screen first.",
    );
  // Keep this call before any network await: Safari requires a user gesture.
  const permission = await Notification.requestPermission();
  if (permission !== "granted")
    throw new PushError(
      "Notifications are blocked. Allow them in your browser or system settings.",
    );
  const config = await api<{ public_key: string }>("/push/config");
  const registration = await navigator.serviceWorker.register("/sw.js");
  const worker =
    registration.installing || registration.waiting || registration.active;
  if (!worker) throw new PushError("Could not start notification service");
  await workerState(worker, ["installed", "activating", "activated"]);
  if (worker.state === "installed")
    worker.postMessage({ type: "SKIP_WAITING" });
  await workerState(worker, ["activated"]);
  let subscription = await registration.pushManager.getSubscription();
  if (subscription && !usesKey(subscription, config.public_key)) {
    if (!(await subscription.unsubscribe()))
      throw new PushError("Could not save push subscription");
    subscription = null;
  }
  subscription ??= await registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: applicationServerKey(config.public_key),
  });
  try {
    await saveSubscription(subscription, language);
  } catch (error) {
    // Do not leave an unsaved opt-in that a later page load would enable.
    await subscription.unsubscribe().catch(() => {});
    throw error;
  }
}

export async function disablePush() {
  const subscription = await currentPushSubscription();
  if (!subscription) return;
  await api("/push/unsubscribe", { endpoint: subscription.endpoint });
  await subscription.unsubscribe();
}

export async function testPush() {
  const subscription = await currentPushSubscription();
  if (!subscription) throw new PushError("Push subscription not found");
  await api("/push/test", { endpoint: subscription.endpoint });
}

export async function clearDevicePush() {
  if (pushSupport() !== "supported") return;
  const registration = await navigator.serviceWorker.getRegistration("/");
  const subscription = await registration?.pushManager.getSubscription();
  await subscription?.unsubscribe();
  for (const notification of (await registration?.getNotifications()) ?? [])
    notification.close();
}

export function notificationChatFromURL(value: string) {
  const id = new URL(value).searchParams.get("chat") || "";
  return /^[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}$/.test(id) ? id : "";
}

export type NotificationInboxTarget = { peer: string; user: string };

export function notificationInboxFromURL(
  value: string,
): NotificationInboxTarget | null {
  const params = new URL(value).searchParams;
  const peer = params.get("inbox") || "";
  const user = params.get("recipient") || "";
  const valid = /^[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}$/;
  return valid.test(peer) && valid.test(user) && peer !== user
    ? { peer, user }
    : null;
}
