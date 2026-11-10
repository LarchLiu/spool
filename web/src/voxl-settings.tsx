import { useState } from "react";
import type { PageProps } from "./settings-ui";
import { Card, Row, Toggle, knownMessage } from "./settings-ui";

export function VoxlSettings({ data, run, t }: PageProps) {
  const voxl = data.status.Voxl;
  const [pending, setPending] = useState(false);
  if (!voxl) return null;
  const enabled = !!data.settings.remote_desktop_enabled;
  const working = pending || voxl.installing;
  const state = voxl.installing
    ? voxl.install_step ||
      (voxl.upgrade_required ? "Upgrading voxl…" : "Installing voxl…")
    : voxl.running
      ? "Running"
      : voxl.installed
        ? enabled && data.status.Running
          ? "Waiting for connection"
          : "Installed"
        : voxl.upgrade_required
          ? "Upgrade required"
          : "Not installed";
  return (
    <Card title={t("Remote desktop")}>
      <Row title="voxl" help={voxl.directory}>
        <span role="status">{knownMessage(state, t)}</span>
        {!voxl.installed && (
          <button
            type="button"
            disabled={working}
            onClick={() => {
              setPending(true);
              void run({ kind: "install-voxl" }).finally(() =>
                setPending(false),
              );
            }}
          >
            {t(voxl.upgrade_required ? "Upgrade voxl" : "Install voxl")}
          </button>
        )}
      </Row>
      {voxl.error && (
        <p className="settings-error" role="alert">
          {voxl.error
            .split("\n")
            .map((line) => knownMessage(line, t))
            .join("\n")}
        </p>
      )}
      <Row
        title={t("Enable remote desktop")}
        help={t(
          "Only authorized users can connect while Spool is running. voxl starts on demand and stops when the last connection closes. Turning this off disconnects everyone.",
        )}
      >
        <Toggle
          label={t("Enable remote desktop")}
          value={enabled}
          disabled={working || !voxl.installed}
          onChange={(value) => {
            setPending(true);
            void run({ kind: "set-remote-desktop", enabled: value }).finally(
              () => setPending(false),
            );
          }}
        />
      </Row>
      {voxl.installed && (
        <Row title={t("Shared folder")} help={`${voxl.directory}/shared`} />
      )}
      {voxl.application && (
        <Row
          title={t("macOS permissions")}
          help={
            <>
              {t(
                "Allow Voxl Helper in System Settings > Privacy & Security > Screen Recording and Accessibility. Connect again after granting access.",
              )}
              <br />
              {voxl.application}
            </>
          }
        />
      )}
      <Row
        title={t("Local installation only")}
        help={
          <>
            {t(
              "The component is installed on this computer and started on demand by Spool.",
            )}
            <br />
            {t(
              "Spool first copies an existing local voxl. Otherwise it builds from crates.io: macOS installs missing dependencies with Homebrew; Linux checks system build libraries; Windows prepares native libraries with vcpkg. See the Spool README for prerequisites.",
            )}
            <br />
            {t(
              "On Linux, run install.sh to prepare WebKitGTK, voxl build libraries and a compatible Rust toolchain before installing voxl.",
            )}
            <br />
            <br />
            <strong className="settings-license-title">
              {t("Component licenses")}
            </strong>
            <br />
            {t(
              "voxl source code is licensed under MIT. x264 is licensed under GPLv2 or later; builds linked with x264 are subject to applicable GPL terms. Other dependencies retain their respective licenses.",
            )}
            <br />
            {t(
              "If you later provide the built binaries to others, assess the applicable distribution obligations separately, including license notices and corresponding source code requirements.",
            )}
            <br />
            <br />
            {t("View upstream projects and licenses:")}
            <br />
            <a
              href="https://github.com/tembo/voxl"
              target="_blank"
              rel="noopener noreferrer"
            >
              voxl
            </a>
            {" · "}
            <a
              href="https://github.com/tembo/voxl/blob/main/LICENSE"
              target="_blank"
              rel="noopener noreferrer"
            >
              MIT
            </a>
            {" / "}
            <a
              href="https://www.videolan.org/developers/x264.html"
              target="_blank"
              rel="noopener noreferrer"
            >
              x264
            </a>
            {" · "}
            <a
              href="https://www.gnu.org/licenses/old-licenses/gpl-2.0.html"
              target="_blank"
              rel="noopener noreferrer"
            >
              GPLv2+
            </a>
          </>
        }
      />
    </Card>
  );
}
