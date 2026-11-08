package com.cloudgeek.spool.android;

import android.app.Activity;
import android.content.ClipData;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.net.Uri;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import androidx.core.content.FileProvider;
import java.io.File;
import java.io.IOException;
import java.util.Arrays;
import java.util.HashSet;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Activity-owned update state; network and APK verification never run on the UI thread. */
final class NativeAppUpdate implements AutoCloseable {
    static final int INSTALL_PERMISSION = 12;
    enum Phase { IDLE, CHECKING, CURRENT, AVAILABLE, DOWNLOADING, READY, VERIFYING, ERROR, DISABLED }
    final Context context;
    final ExecutorService worker;
    final Handler main;
    private final Runnable changed;
    private final java.util.function.Supplier<NativeUpdateNetwork> networks;
    Phase phase;
    NativeUpdateRelease release;
    long received;
    String message = "", checksum;
    private int epoch;
    private boolean closed, permissionPending;
    private NativeUpdateNetwork request;
    private File ready;

    NativeAppUpdate(Context context, Runnable changed) {
        this(context.getApplicationContext(), Executors.newSingleThreadExecutor(), new Handler(Looper.getMainLooper()), NativeUpdateNetwork::new, changed);
    }
    NativeAppUpdate(Context context, ExecutorService worker, Handler main, java.util.function.Supplier<NativeUpdateNetwork> networks, Runnable changed) {
        this.context = context; this.worker = worker; this.main = main; this.networks = networks; this.changed = changed;
        phase = context.getPackageName().equals(NativeUpdateRelease.PACKAGE) ? Phase.IDLE : Phase.DISABLED;
    }
    boolean busy() { return phase == Phase.CHECKING || phase == Phase.DOWNLOADING || phase == Phase.VERIFYING; }
    String version() throws PackageManager.NameNotFoundException { return context.getPackageManager().getPackageInfo(context.getPackageName(), 0).versionName; }
    private void update(Phase next, String text) { phase = next; message = text; changed.run(); }
    private void post(int token, Runnable change) { main.post(() -> { if (!closed && epoch == token) change.run(); }); }

    void check() {
        if (closed || busy() || phase == Phase.DISABLED) return;
        int token = ++epoch;
        NativeUpdateNetwork network = networks.get(); request = network;
        release = null; ready = null; checksum = null;
        update(Phase.CHECKING, "");
        worker.execute(() -> {
            try {
                NativeUpdateRelease result = network.latest(version());
                post(token, () -> { release = result; update(result == null ? Phase.CURRENT : Phase.AVAILABLE, ""); });
            } catch (Exception error) { post(token, () -> update(Phase.ERROR, "Could not check for updates. Try again.")); }
        });
    }

    void download() {
        if (closed || busy() || release == null) return;
        int token = ++epoch;
        NativeUpdateRelease selected = release;
        NativeUpdateNetwork network = networks.get(); request = network;
        ready = null; received = 0; update(Phase.DOWNLOADING, "");
        worker.execute(() -> {
            File partial = null;
            try {
                File directory = new File(context.getCacheDir(), "app-updates");
                if (!directory.isDirectory() && !directory.mkdirs()) throw new IOException("Cannot create update directory");
                partial = new File(directory, "update.apk.part");
                String hash = network.download(selected, partial, bytes -> post(token, () -> { received = bytes; changed.run(); }));
                verify(context, partial, selected);
                network.checkCancelled();
                File apk = new File(directory, "update.apk");
                if (apk.exists() && !apk.delete() || !partial.renameTo(apk)) throw new IOException("Cannot save verified APK");
                post(token, () -> { ready = apk; checksum = hash; update(Phase.READY, ""); });
            } catch (Exception error) { post(token, () -> update(Phase.ERROR, "Could not download or verify the update. Try again.")); }
            finally { if (partial != null) partial.delete(); }
        });
    }

    void cancel() {
        if (closed || !busy()) return;
        epoch++; if (request != null) request.cancel();
        update(release == null ? Phase.IDLE : Phase.AVAILABLE, "");
    }

    void install(Activity activity) {
        if (closed || phase != Phase.READY || ready == null) return;
        if (!activity.getPackageManager().canRequestPackageInstalls()) {
            try {
                permissionPending = true;
                activity.startActivityForResult(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + activity.getPackageName())), INSTALL_PERMISSION);
                update(Phase.READY, "Allow app installs in system settings, then return.");
            } catch (RuntimeException error) { permissionPending = false; update(Phase.READY, "Could not open the system installer."); }
            return;
        }
        int token = ++epoch;
        File apk = ready; NativeUpdateRelease selected = release;
        update(Phase.VERIFYING, "");
        worker.execute(() -> {
            try {
                if (!apk.isFile() || apk.length() != selected.size || !NativeUpdateNetwork.digest(apk).equals(checksum)) throw new IOException("Cached APK changed");
                verify(context, apk, selected);
                post(token, () -> {
                    update(Phase.READY, "");
                    if (activity.isFinishing() || activity.isDestroyed()) return;
                    try { activity.startActivity(installIntent(context, apk)); }
                    catch (RuntimeException error) { update(Phase.READY, "Could not open the system installer."); }
                });
            } catch (Exception error) {
                post(token, () -> { ready = null; update(Phase.ERROR, "Could not download or verify the update. Try again."); });
            }
        });
    }

    void permissionResult(Activity activity) {
        if (!permissionPending) return;
        permissionPending = false;
        if (activity.getPackageManager().canRequestPackageInstalls()) install(activity);
        else update(Phase.READY, "Allow app installs in system settings, then return.");
    }

    static Intent installIntent(Context context, File apk) {
        Uri uri = FileProvider.getUriForFile(context, context.getPackageName() + ".updates", apk);
        Intent intent = new Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        intent.setClipData(ClipData.newRawUri("APK", uri));
        return intent;
    }

    @SuppressWarnings("deprecation")
    static void verify(Context context, File apk, NativeUpdateRelease release) throws Exception {
        PackageManager manager = context.getPackageManager();
        int flags = Build.VERSION.SDK_INT >= 28 ? PackageManager.GET_SIGNING_CERTIFICATES : PackageManager.GET_SIGNATURES;
        PackageInfo installed = manager.getPackageInfo(context.getPackageName(), flags);
        PackageInfo candidate = manager.getPackageArchiveInfo(apk.getAbsolutePath(), flags);
        if (candidate == null) throw new IOException("Invalid APK");
        verifyIdentity(installed, candidate, release);
    }

    @SuppressWarnings("deprecation")
    static void verifyIdentity(PackageInfo installed, PackageInfo candidate, NativeUpdateRelease release) throws Exception {
        long currentCode = Build.VERSION.SDK_INT >= 28 ? installed.getLongVersionCode() : installed.versionCode;
        long nextCode = Build.VERSION.SDK_INT >= 28 ? candidate.getLongVersionCode() : candidate.versionCode;
        if (!NativeUpdateRelease.PACKAGE.equals(candidate.packageName) || !candidate.packageName.equals(installed.packageName)
                || !release.version.equals(candidate.versionName) || NativeUpdateRelease.compare(candidate.versionName, installed.versionName) <= 0
                || nextCode != release.versionCode || nextCode < currentCode)
            throw new IOException("APK identity or version mismatch");
        Signature[] current = signatures(installed), next = signatures(candidate);
        if (current == null || next == null || current.length == 0 || next.length == 0
                || !new HashSet<>(Arrays.asList(current)).equals(new HashSet<>(Arrays.asList(next))))
            throw new IOException("APK signing key does not match this app");
    }

    @SuppressWarnings("deprecation")
    private static Signature[] signatures(PackageInfo info) {
        return Build.VERSION.SDK_INT >= 28 ? info.signingInfo == null ? null : info.signingInfo.getApkContentsSigners() : info.signatures;
    }

    @Override public void close() {
        closed = true; epoch++;
        if (request != null) request.cancel();
        worker.shutdownNow();
    }
}
