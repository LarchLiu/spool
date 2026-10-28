import type { Translator } from "./i18n";

export function LoginStory({ t }: { t: Translator }) {
  return (
    <section className="login-story">
      <div className="brand">
        <img className="brand-mark" src="/logo.png" alt="" />
        Spool
        <span className="tag">{t("Local agent workspace")}</span>
      </div>
      <div className="login-story-copy">
        <span className="eyebrow">{t("Your projects. Your agents.")}</span>
        <h1>{t("Ideas, brought to life.")}</h1>
        <p>
          {t(
            "Return to your projects from any device. Let your local agent keep going.",
          )}
        </p>
      </div>
      <span className="login-foot">
        {t("Private workspace · powered by your local CLI")}
      </span>
    </section>
  );
}
