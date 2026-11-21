# ADB bridge

The ADB bridge attaches an Android phone that is with you to the `adb` server
on the Spool host, so agents there can install and debug apps on it. The phone
appears on the host as `127.0.0.1:<port>`; use it with `adb -s 127.0.0.1:<port>`.
Spool tries to reuse the same serial for the same share while the server is
running. Check the current serial after reconnecting or restarting the server.

## Trust model

- A shared phone is visible to **every process of the host's OS user**,
  including every agent of every Spool user, exactly as if it were plugged in.
- Sharing needs two local decisions on the host: the host-level switch (off by
  default) and a grant for the user. Project access implies neither.
- The bridge relays bytes and holds no key. The phone authorizes the host's
  own adb key (the USB debugging prompt, or wireless pairing).
- Traffic follows the route the client uses to reach Spool, inside its HTTPS
  session. USB traffic is plain ADB inside that session.
- Turning the switch off, revoking the grant or signing out detaches the phone.

## Enable on the host

Install Android SDK Platform-Tools so `adb` is on `PATH` (or set `ANDROID_HOME`).
In the desktop app, use **Settings → Service → ADB bridge** and grant access
under **People**. With the CLI, the host switch takes effect the next time
`spool-server serve` starts:

```sh
spool-server user update --user alice --adb-bridge=true
spool-server config set --adb-bridge=true
```

## Share a phone from a computer (USB)

1. Enable USB debugging on the phone and plug it into the computer running the
   browser.
2. Open Spool in Chrome or Edge over HTTPS (or `localhost`), open the
   appearance and settings dialog, and choose **Share a phone over USB**.
3. Pick the phone and allow USB debugging on it.

Only one program can hold the phone's USB interface. If the browser cannot open
it, release the device in any local debugging tools first. Sharing ends when the
page closes; the sidebar indicates when a phone is shared. If the browser
cannot identify the same phone after a disconnect, choose the phone again.

## Share the phone running the Spool app (wireless debugging)

Requires Android 11 or later and Wi-Fi, which wireless debugging needs.

1. In Developer options, turn on **Wireless debugging**.
2. In the Spool app, open the server's Settings and choose **Share this phone**.
   Allow Spool to show ADB bridge notifications; the app does not start a share
   without them.
3. The first time, pair the phone with the host's adb: in Wireless debugging
   choose **Pair device with pairing code**, keep that dialog open, and enter
   the code in Spool's notification (or in Spool Settings in split screen).
   If automatic discovery fails, enter the port shown in Android's Wireless
   debugging settings, or the pairing port shown in its pairing dialog.

An ongoing notification lets you stop sharing while another app is in the
foreground. Sharing also stops if you disable Spool's notifications or the ADB
bridge notification channel, or dismiss the notification where Android allows it.
Sharing ends if Android stops Spool or when the selected server or account changes. Stopping the share does not remove the pairing; to forget the host,
remove it under Wireless debugging → Paired devices.
