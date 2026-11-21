import { useState } from "react";
import type { PageProps } from "./settings-ui";
import { Card, Row, Toggle } from "./settings-ui";

// The host's side of the ADB bridge: the switch, the adb it will use and what
// is attached right now. Who may share devices is granted per person.
export function ADBHostSettings({ data, run, t }: PageProps) {
  const adb = data.status.ADB;
  const [pending, setPending] = useState(false);
  if (!adb) return null;
  const enabled = !!data.settings.adb_bridge_enabled;
  const owner = (id: string) => {
    const person = data.users.find((person) => person.id === id);
    return person ? person.nickname || person.username : id;
  };
  return (
    <Card title={t("ADB bridge")}>
      <Row
        title={t("Enable ADB bridge")}
        help={t(
          "Authorized users can share an Android device they have with them with adb on this computer. Every agent and program running as this computer's user can then install apps on that device and control it. Turning this off disconnects every shared device.",
        )}
      >
        <Toggle
          label={t("Enable ADB bridge")}
          value={enabled}
          disabled={pending}
          onChange={(value) => {
            setPending(true);
            void run({ kind: "set-adb-bridge", enabled: value }).finally(() =>
              setPending(false),
            );
          }}
        />
      </Row>
      <Row
        title="adb"
        help={
          adb.Path ||
          t(
            "adb was not found on this computer. Install Android SDK Platform-Tools to use the ADB bridge.",
          )
        }
      >
        <span role="status">{t(adb.Path ? "Installed" : "Not installed")}</span>
      </Row>
      <Row
        title={t("Who can share devices")}
        help={t(
          "Allow the ADB bridge for each person under People. Project access does not include it.",
        )}
      />
      {enabled && (
        <Row
          title={t("Shared devices")}
          help={t(
            adb.Devices.length
              ? "Programs on this computer reach a shared device with adb -s followed by its address."
              : "No devices are shared right now.",
          )}
        />
      )}
      {adb.Devices.map((device) => (
        <Row
          key={device.Serial}
          title={device.Serial}
          help={`${device.Transport === "wireless" ? t("Wireless debugging") : "USB"} · ${owner(device.Owner)}`}
        >
          <span className="settings-state online" data-adb-device>
            <span className="settings-status-dot" />
            {t("Connected")}
          </span>
        </Row>
      ))}
    </Card>
  );
}
