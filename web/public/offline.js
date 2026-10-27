// Server-side preferences are unavailable offline. Use device preferences
// without caching an account, its settings, or its credentials.
const offlineMessages = {
  en: [
    "Pick up the conversation later.",
    "Cannot reach your desktop service. Make sure the computer is awake, Spool is running, and you are on the right network.",
    "Submitted tasks are not cancelled when your browser goes offline.",
    "Reconnect →",
  ],
  zh: [
    "稍后继续对话。",
    "暂时无法连接到桌面服务。请确认电脑已唤醒、Spool 正在运行，并已连接到正确的网络。",
    "已提交的任务不因浏览器离线而取消。",
    "重新连接 →",
  ],
  ja: [
    "会話は、また後で。",
    "デスクトップサービスに接続できません。コンピューターが起動し、Spool が動作していること、および正しいネットワークへの接続を確認してください。",
    "ブラウザーがオフラインになっても、送信済みのタスクは継続します。",
    "再接続 →",
  ],
};
const offlineLocale =
  navigator.languages
    .map((value) => value.split("-")[0])
    .find((value) => offlineMessages[value]) || "en";
document.documentElement.lang =
  offlineLocale === "zh" ? "zh-CN" : offlineLocale;
["title", "detail", "task", "reconnect"].forEach((name, index) => {
  document.getElementById("offline-" + name).textContent =
    offlineMessages[offlineLocale][index];
});
document.title = offlineMessages[offlineLocale][0] + " · Spool";
