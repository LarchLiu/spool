package com.cloudgeek.spool.android;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import androidx.core.app.NotificationCompat;
import androidx.core.app.RemoteInput;
import androidx.core.app.ServiceCompat;
import androidx.core.content.ContextCompat;

/**
 * Keeps the app running while this phone is shared with a server's adb, and
 * says so in a notification that can stop it. An agent that installs and
 * starts another app puts Spool in the background, and the system's pairing
 * dialog only stays open while its settings page is on screen, so the pairing
 * code is taken from the notification. The share itself belongs to the
 * activity; this service holds no connection.
 */
public final class AdbShareService extends Service {
    interface Controls {
        void stop();
        void pair(String code);
        void foregroundUnavailable();
        void notificationUnavailable();
    }
    private static final String CHANNEL = "adb-share", START = "start", STOP = "stop", PAIR = "pair", CODE = "code";
    private static final int ID = 41;
    private static final long NOTIFICATION_CHECK_MS = 5000;

    // Accessed only on the main thread, by the Activity and this service.
    private static Session current;
    private Session foreground;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Runnable notificationCheck = this::checkNotification;
    private final BroadcastReceiver notificationChanges = new BroadcastReceiver() {
        @Override public void onReceive(Context context, Intent intent) {
            if (NotificationManager.ACTION_NOTIFICATION_CHANNEL_BLOCK_STATE_CHANGED.equals(intent.getAction())
                    && !CHANNEL.equals(intent.getStringExtra(NotificationManager.EXTRA_NOTIFICATION_CHANNEL_ID))) return;
            checkNotification();
        }
    };

    private static final class Session {
        final Controls owner;
        final android.net.Uri identity = android.net.Uri.fromParts(CHANNEL, java.util.UUID.randomUUID().toString(), null);
        Notification notice;
        AdbShareService service;
        Session(Controls owner) { this.owner = owner; }
        Intent command(Context context, String action) {
            return new Intent(context, AdbShareService.class).setAction(action).setData(identity);
        }
    }

    static boolean canNotify(Context context) {
        NotificationManager manager = context.getSystemService(NotificationManager.class);
        NotificationChannel channel = manager.getNotificationChannel(CHANNEL);
        return manager.areNotificationsEnabled() && (channel == null || channel.getImportance() != NotificationManager.IMPORTANCE_NONE);
    }

    /** Shows where the share stands, starting the service from the visible Activity for a new share. */
    static boolean show(Context context, Controls owner, AdbShare share, String locale) {
        Context app = context.getApplicationContext();
        boolean starting = current == null;
        if (!starting && current.owner != owner) return false;
        Session session = starting ? new Session(owner) : current;
        try {
            NotificationManager manager = app.getSystemService(NotificationManager.class);
            session.notice = notification(app, session, share, locale);
            if (!starting) {
                if (session.service != null) manager.notify(ID, session.notice);
                return true;
            }
            NotificationChannel channel = new NotificationChannel(CHANNEL, NativeText.translate("ADB bridge", locale), NotificationManager.IMPORTANCE_LOW);
            channel.setShowBadge(false);
            manager.createNotificationChannel(channel);
            current = session;
            // Called from the visible Activity after checking notification permission. A stop
            // before onStartCommand does not leave a foreground-start deadline.
            if (app.startService(session.command(app, START)) != null) return true;
        } catch (RuntimeException ignored) {
            // The caller closes the share if Android does not let this service run.
        }
        return false;
    }

    static void end(Context context, Controls owner) {
        if (current == null || current.owner != owner) return;
        Session ended = current;
        current = null;
        if (ended.service != null) {
            ServiceCompat.stopForeground(ended.service, ServiceCompat.STOP_FOREGROUND_REMOVE);
            ended.service.foreground = null;
        }
        context.stopService(new Intent(context, AdbShareService.class));
    }

    private static PendingIntent command(Context context, Session session, String action, boolean mutable) {
        int flags = mutable ? (Build.VERSION.SDK_INT >= 31 ? PendingIntent.FLAG_MUTABLE : 0) : PendingIntent.FLAG_IMMUTABLE;
        return PendingIntent.getService(context, 0, session.command(context, action), flags);
    }

    private static Notification notification(Context context, Session session, AdbShare share, String locale) {
        String text = NativeAdbSettings.status(share, locale);
        NotificationCompat.Builder builder = new NotificationCompat.Builder(context, CHANNEL)
                .setSmallIcon(R.drawable.web_icon_smartphone)
                .setContentTitle(NativeText.translate("Sharing this phone", locale))
                .setContentText(text)
                .setStyle(new NotificationCompat.BigTextStyle().bigText(text))
                .setOngoing(true).setOnlyAlertOnce(true).setShowWhen(false)
                .setCategory(NotificationCompat.CATEGORY_SERVICE)
                .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
                .setContentIntent(PendingIntent.getActivity(context, 0,
                        new Intent(context, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP), PendingIntent.FLAG_IMMUTABLE));
        if (share.phase == AdbShare.Phase.UNPAIRED) {
            String label = NativeText.translate("Enter pairing code", locale);
            builder.addAction(new NotificationCompat.Action.Builder(0, label, command(context, session, PAIR, true))
                    .addRemoteInput(new RemoteInput.Builder(CODE).setLabel(NativeText.translate("Pairing code", locale)).build())
                    .setAllowGeneratedReplies(false).build());
        }
        PendingIntent stop = command(context, session, STOP, false);
        // Android can let the user dismiss even an ongoing notification.
        builder.addAction(0, NativeText.translate("Stop sharing", locale), stop).setDeleteIntent(stop);
        return builder.build();
    }

    @Override public void onCreate() {
        super.onCreate();
        IntentFilter filter = new IntentFilter(NotificationManager.ACTION_APP_BLOCK_STATE_CHANGED);
        filter.addAction(NotificationManager.ACTION_NOTIFICATION_CHANNEL_BLOCK_STATE_CHANGED);
        // The system sends these to the affected app; don't accept other apps' broadcasts.
        ContextCompat.registerReceiver(this, notificationChanges, filter, ContextCompat.RECEIVER_NOT_EXPORTED);
    }

    private void checkNotification() {
        main.removeCallbacks(notificationCheck);
        Session session = foreground;
        if (session == null || session != current) return;
        if (!canNotify(this)) session.owner.notificationUnavailable();
        // Also catch permission changes on Android versions/devices that omit a broadcast.
        else main.postDelayed(notificationCheck, NOTIFICATION_CHECK_MS);
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        Session session = current;
        if (session == null) {
            stopSelfResult(startId);
            return START_NOT_STICKY;
        }
        // A delayed start or notification action is only valid for its own share.
        if (intent == null || !session.identity.equals(intent.getData())) return START_NOT_STICKY;
        String action = intent.getAction();
        if (START.equals(action)) {
            if (!canNotify(this)) {
                session.owner.notificationUnavailable();
                return START_NOT_STICKY;
            }
            try {
                ServiceCompat.startForeground(this, ID, session.notice,
                        Build.VERSION.SDK_INT >= 34 ? ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE : 0);
                session.service = this;
                foreground = session;
                checkNotification();
            } catch (RuntimeException ignored) {
                session.owner.foregroundUnavailable();
            }
        } else if (STOP.equals(action)) session.owner.stop();
        else if (PAIR.equals(action)) {
            Bundle reply = RemoteInput.getResultsFromIntent(intent);
            CharSequence code = reply == null ? null : reply.getCharSequence(CODE);
            // The code is handed over and never stored or logged.
            session.owner.pair(code == null ? "" : code.toString());
        }
        return START_NOT_STICKY;
    }

    @Override public void onDestroy() {
        unregisterReceiver(notificationChanges);
        main.removeCallbacks(notificationCheck);
        if (foreground != null && foreground == current) {
            Session ended = foreground;
            current = null;
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE);
            ended.owner.foregroundUnavailable();
        }
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) { return null; }
}
