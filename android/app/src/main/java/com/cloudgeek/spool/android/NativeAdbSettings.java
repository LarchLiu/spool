package com.cloudgeek.spool.android;

import android.text.InputType;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.function.BiConsumer;
import java.util.function.IntConsumer;

/** Offers this phone to the selected server's adb. Shown only to users the server allows. */
final class NativeAdbSettings extends LinearLayout {
    final TextView status, detail, usage;
    final EditText port, pairingCode, pairingPort;
    final Button action, pair;
    final LinearLayout pairing;
    private final NativeUi ui;
    private boolean sharing;

    NativeAdbSettings(NativeUi ui, IntConsumer start, Runnable stop, BiConsumer<String, Integer> pairDevice) {
        super(ui.context); this.ui = ui; setOrientation(VERTICAL);
        addView(ui.divider()); addView(ui.space(18));
        TextView title = ui.text(ui.t("ADB bridge"), 12, ui.text);
        ui.typography(title, 12, ui.text, 500);
        addView(title); addView(ui.space(8));
        TextView about = ui.text(ui.t("Share this phone with adb on the server over wireless debugging, so agents there can install and debug apps on it. While it is shared, the server and every agent and program on it can control the phone."), 11, ui.muted);
        ui.lineHeight(about, 1.5f);
        addView(about); addView(ui.space(10));
        status = ui.text("", 12, ui.text); ui.lineHeight(status, 1.5f);
        status.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        addView(status);
        detail = ui.text("", 11, ui.muted); detail.setTextIsSelectable(true); addView(detail);
        usage = ui.text("", 11, ui.muted); ui.lineHeight(usage, 1.5f); usage.setTextIsSelectable(true); addView(usage);
        port = number(ui.t("Port (automatic)"), 5);
        LayoutParams gap = new LayoutParams(-1, -2); gap.topMargin = ui.dp(8);
        addView(port, gap);
        pairing = ui.column();
        pairingCode = number(ui.t("Pairing code"), 12);
        pairingPort = number(ui.t("Pairing port (automatic)"), 5);
        pairing.addView(pairingCode, new LayoutParams(-1, -2));
        LayoutParams second = new LayoutParams(-1, -2); second.topMargin = ui.dp(8);
        pairing.addView(pairingPort, second);
        pair = ui.button(ui.t("Pair"), true, () -> pairDevice.accept(pairingCode.getText().toString(), value(pairingPort)));
        LayoutParams third = new LayoutParams(-1, -2); third.topMargin = ui.dp(8);
        pairing.addView(pair, third);
        LayoutParams block = new LayoutParams(-1, -2); block.topMargin = ui.dp(8);
        addView(pairing, block);
        action = ui.button("", false, () -> { if (sharing) stop.run(); else start.accept(value(port)); });
        LayoutParams last = new LayoutParams(-1, -2); last.topMargin = ui.dp(10);
        addView(action, last);
        addView(ui.space(24));
    }

    private EditText number(String hint, int length) {
        EditText field = ui.field(hint, false);
        field.setInputType(InputType.TYPE_CLASS_NUMBER);
        field.setFilters(new android.text.InputFilter[] {new android.text.InputFilter.LengthFilter(length)});
        field.setContentDescription(hint);
        field.setImportantForAutofill(IMPORTANT_FOR_AUTOFILL_NO);
        return field;
    }

    private static int value(EditText field) {
        try {
            int number = Integer.parseInt(field.getText().toString().trim());
            return number > 0 && number <= 65535 ? number : 0;
        } catch (NumberFormatException error) { return 0; }
    }

    /** availability is "" when the server accepts a share, its reason when not, null while unknown. */
    void bind(AdbShare share, String availability, boolean supported) {
        boolean shown = share.phase != AdbShare.Phase.IDLE
                || availability != null && !availability.equals("not_authorized");
        setVisibility(shown ? VISIBLE : GONE);
        if (!shown) return;
        sharing = share.active();
        String text = status(share, ui.locale);
        if (share.phase == AdbShare.Phase.IDLE) text = !supported ? ui.t("Sharing a phone needs Android 11 or later.")
                : "adb_missing".equals(availability) ? ui.t("adb is not installed on the server.")
                : "disabled".equals(availability) ? ui.t("The ADB bridge is turned off on the server.") : "";
        show(status, text);
        status.setTextColor(share.phase == AdbShare.Phase.FAILED ? ui.error : ui.text);
        show(detail, explained(share.code) ? "" : share.detail);
        show(usage, share.phase == AdbShare.Phase.ONLINE
                ? ui.t("Agents on the server use it with adb -s {serial}. Use the notification to stop sharing.", "serial", share.serial) : "");
        boolean pairable = share.phase == AdbShare.Phase.UNPAIRED;
        pairing.setVisibility(pairable ? VISIBLE : GONE);
        if (!pairable) pairingCode.setText("");
        boolean startable = !sharing && supported && "".equals(availability);
        port.setVisibility(startable ? VISIBLE : GONE);
        action.setVisibility(sharing || startable ? VISIBLE : GONE);
        String label = ui.t(sharing ? "Stop sharing" : "Share this phone");
        action.setText(label); action.setContentDescription(label);
    }

    private static void show(TextView view, String text) {
        if (!text.contentEquals(view.getText())) view.setText(text);
        view.setVisibility(text.isEmpty() ? GONE : VISIBLE);
    }

    // Failures whose text already says everything the server's diagnostic would.
    private static boolean explained(String code) {
        switch (code) {
            case "revoked": case "not_authorized": case "forbidden": case "disabled": case "adb_missing":
            case "unavailable": case "replaced": case "busy": case "device_unreachable": case "pairing_unreachable": return true;
            default: return false;
        }
    }

    /** One line for where sharing stands; empty while nothing is shared. Also the notification's text. */
    static String status(AdbShare share, String locale) {
        String key;
        switch (share.phase) {
            case IDLE: return "";
            case CONNECTING: key = "Connecting…"; break;
            case UNAUTHORIZED: key = "Allow USB debugging on the phone to continue."; break;
            case ONLINE: return NativeText.translate("The phone is shared as {serial}.", locale, "serial", share.serial);
            case WAITING:
                key = share.code.equals("device_unreachable") || share.code.equals("device_closed")
                        ? "Wireless debugging is not reachable. Waiting for it to come back…"
                        : "The connection to the server was interrupted. Reconnecting…";
                break;
            case PAIRING: key = "Pairing…"; break;
            case UNPAIRED:
                switch (share.code) {
                    case "pairing_unreachable": key = "Keep the pairing dialog open in Wireless debugging, then enter its code."; break;
                    case "": key = "This phone is not paired with the server yet. In Developer options, open Wireless debugging and choose Pair device with pairing code, then enter the code here or in the notification."; break;
                    case "network": key = "The connection to the server was interrupted. Try pairing again."; break;
                    case "busy": key = "Too many devices are pairing. Try again shortly."; break;
                    default: key = "Pairing failed. Check the code and try again.";
                }
                break;
            default:
                switch (share.code) {
                    case "notification_unavailable": key = "Allow ADB bridge notifications before sharing this phone."; break;
                    case "foreground_unavailable": key = "Android could not keep phone sharing running. Try again from Spool."; break;
                    case "revoked": case "not_authorized": case "forbidden": case "auth_expired": key = "Your access to the ADB bridge ended."; break;
                    case "disabled": key = "The ADB bridge is turned off on the server."; break;
                    case "adb_missing": case "unavailable": key = "adb is not installed on the server."; break;
                    case "replaced": key = "This phone is now shared from another connection."; break;
                    case "busy": key = "The server cannot take another device right now. Stop sharing one, or try again shortly."; break;
                    case "connect_failed": key = "The server's adb could not connect to the phone."; break;
                    case "device_unreachable": key = "Turn on Wireless debugging in Developer options, then try again."; break;
                    default: key = "Sharing the phone stopped unexpectedly.";
                }
        }
        return NativeText.translate(key, locale);
    }
}
