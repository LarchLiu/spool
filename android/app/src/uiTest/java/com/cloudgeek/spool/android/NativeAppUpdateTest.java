package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.Context;
import android.content.ContextWrapper;
import android.content.Intent;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import java.io.File;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeAppUpdateTest {
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private static final class Fixture implements AutoCloseable {
        final Context context = new ContextWrapper(RuntimeEnvironment.getApplication()) {
            @Override public String getPackageName() { return NativeUpdateRelease.PACKAGE; }
        };
        final MainActivityGoalTest.Queue worker = new MainActivityGoalTest.Queue();
        final List<String> requests = new ArrayList<>();
        final byte[] apk = NativeUpdateTest.bytes("verified fixture APK");
        final File directory = new File(context.getCacheDir(), "app-updates");
        final NativeAppUpdate update;
        int status = 200;
        Fixture() throws Exception {
            byte[] metadata = NativeUpdateTest.bytes(NativeUpdateTest.metadata("0.1.18", apk).toString());
            NativeUpdateRelease release = NativeUpdateTest.release(apk);
            byte[] sums = NativeUpdateTest.bytes(NativeUpdateTest.hash(apk) + "  " + release.name);
            org.robolectric.shadows.ShadowPackageManager manager = Shadows.shadowOf(context.getPackageManager());
            manager.installPackage(NativeUpdateTest.identity("0.1.17", "release-key"));
            for (String name : new String[]{"update.apk", "update.apk.part"})
                manager.setPackageArchiveInfo(new File(directory, name).getAbsolutePath(), NativeUpdateTest.identity("0.1.18", "release-key"));
            update = new NativeAppUpdate(context, worker, new Handler(Looper.getMainLooper()), () -> new NativeUpdateNetwork(url -> {
                requests.add(url.toString());
                byte[] body = url.toString().endsWith("/latest") ? metadata : url.toString().endsWith("SHA256SUMS") ? sums : apk;
                return new NativeUpdateTest.Connection(url, body, status, null);
            }), () -> {});
        }
        @Override public void close() {
            update.close(); worker.jobs.clear();
            new File(directory, "update.apk").delete(); new File(directory, "update.apk.part").delete();
        }
    }

    @Test public void checksOnlyOfferAnUpdateDownloadsAndInstallationRequireSeparateUserActions() throws Exception {
        try (Fixture f = new Fixture()) {
            assertTrue(f.requests.isEmpty()); assertTrue(f.worker.jobs.isEmpty());
            f.update.check(); f.update.check(); assertEquals(1, f.worker.jobs.size());
            f.worker.run(); assertEquals(NativeAppUpdate.Phase.AVAILABLE, f.update.phase);
            assertEquals(1, f.requests.size()); assertTrue(f.requests.get(0).endsWith("/latest"));
            idle(); assertTrue("An available update never starts downloading itself", f.worker.jobs.isEmpty());
            f.update.download(); f.worker.run();
            assertEquals(NativeAppUpdate.Phase.READY, f.update.phase); assertEquals(3, f.requests.size());
            assertArrayEquals(f.apk, Files.readAllBytes(new File(f.directory, "update.apk").toPath()));
            idle(); assertTrue("A finished download never starts installation itself", f.worker.jobs.isEmpty());
        }
    }

    @Test public void cancellationAndDisposalFenceLateCheckResultsAndProgress() throws Exception {
        try (Fixture f = new Fixture()) {
            f.update.check(); f.update.cancel(); f.worker.run();
            assertEquals(NativeAppUpdate.Phase.IDLE, f.update.phase); assertNull(f.update.release); assertTrue(f.requests.isEmpty());
            f.update.check(); f.worker.run(); assertEquals(NativeAppUpdate.Phase.AVAILABLE, f.update.phase);
            f.update.download(); f.update.cancel(); f.worker.run();
            assertEquals(NativeAppUpdate.Phase.AVAILABLE, f.update.phase); assertFalse(new File(f.directory, "update.apk.part").exists());
            f.update.check(); Runnable pending = f.worker.jobs.get(0); f.update.close(); pending.run(); idle(); assertNull(f.update.release);
        }
    }

    @Test public void networkErrorsAreRetryableAndDontExposeSignedDownloadUrls() throws Exception {
        try (Fixture f = new Fixture()) {
            f.status = 403; f.update.check(); f.worker.run();
            assertEquals(NativeAppUpdate.Phase.ERROR, f.update.phase); assertFalse(f.update.message.contains("https:"));
            f.status = 200; f.update.check(); f.worker.run(); assertEquals(NativeAppUpdate.Phase.AVAILABLE, f.update.phase);
            f.status = 500; f.update.download(); f.worker.run(); assertEquals(NativeAppUpdate.Phase.ERROR, f.update.phase);
            assertNotNull(f.update.release);
            f.status = 200; f.update.download(); f.worker.run(); assertEquals(NativeAppUpdate.Phase.READY, f.update.phase);
        }
    }

    @Test public void permissionDenialKeepsTheDownloadedApkAndChangedFilesCannotBeInstalled() throws Exception {
        try (Fixture f = new Fixture(); org.robolectric.android.controller.ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            f.update.check(); f.worker.run(); f.update.download(); f.worker.run();
            Shadows.shadowOf(activity.get().getPackageManager()).setCanRequestPackageInstalls(false);
            f.update.install(activity.get());
            Intent permission = Shadows.shadowOf(activity.get()).getNextStartedActivityForResult().intent;
            Shadows.shadowOf(activity.get()).getNextStartedActivity();
            assertEquals(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, permission.getAction());
            assertEquals("package:" + activity.get().getPackageName(), permission.getDataString());
            f.update.permissionResult(activity.get()); assertEquals(NativeAppUpdate.Phase.READY, f.update.phase);
            assertTrue(f.worker.jobs.isEmpty());
            Files.write(new File(f.directory, "update.apk").toPath(), NativeUpdateTest.bytes("tampered"));
            Shadows.shadowOf(activity.get().getPackageManager()).setCanRequestPackageInstalls(true);
            f.update.install(activity.get()); f.worker.run();
            assertEquals(NativeAppUpdate.Phase.ERROR, f.update.phase);
            assertNull(Shadows.shadowOf(activity.get()).getNextStartedActivity());
        }
    }

    @Test public void grantingInstallPermissionResumesOnlyThePreviouslyRequestedInstallation() throws Exception {
        try (Fixture f = new Fixture(); org.robolectric.android.controller.ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            // The debug test manifest has a .dev authority; register the release authority used by this fixture.
            android.content.pm.PackageManager manager = f.context.getPackageManager();
            android.content.pm.ProviderInfo provider = new android.content.pm.ProviderInfo(manager.resolveContentProvider(RuntimeEnvironment.getApplication().getPackageName() + ".updates", 0));
            provider.authority = NativeUpdateRelease.PACKAGE + ".updates";
            Shadows.shadowOf(manager).addOrUpdateProvider(provider);
            f.update.check(); f.worker.run(); f.update.download(); f.worker.run();
            Shadows.shadowOf(manager).setCanRequestPackageInstalls(false);
            f.update.install(activity.get());
            Shadows.shadowOf(activity.get()).getNextStartedActivity();
            Shadows.shadowOf(activity.get()).getNextStartedActivityForResult();
            Shadows.shadowOf(manager).setCanRequestPackageInstalls(true);
            f.update.permissionResult(activity.get());
            assertEquals(NativeAppUpdate.Phase.VERIFYING, f.update.phase); f.worker.run();
            Intent install = Shadows.shadowOf(activity.get()).getNextStartedActivity();
            assertNotNull(f.update.message, install);
            assertEquals(Intent.ACTION_VIEW, install.getAction()); assertEquals("content", install.getData().getScheme());
            assertEquals(NativeAppUpdate.Phase.READY, f.update.phase);
            f.update.permissionResult(activity.get()); assertTrue(f.worker.jobs.isEmpty());
            assertNull(Shadows.shadowOf(activity.get()).getNextStartedActivity());
        }
    }

    @Test public void viewBindsProgressErrorsAndExplicitDownloadAndInstallActionsWithoutNetwork() throws Exception {
        try (Fixture f = new Fixture()) {
            int[] clicks = new int[4];
            NativeUi ui = new NativeUi(RuntimeEnvironment.getApplication(), false, "zh-CN");
            NativeUpdateView view = new NativeUpdateView(ui, () -> clicks[0]++, () -> clicks[1]++, () -> clicks[2]++, () -> clicks[3]++);
            view.bind(f.update); assertTrue(f.requests.isEmpty()); view.action.performClick(); assertEquals(1, clicks[0]);
            f.update.check(); f.worker.run(); view.bind(f.update); assertEquals("下载 v0.1.18", view.action.getText().toString());
            assertEquals(android.view.View.GONE, view.status.getVisibility());
            view.action.performClick(); assertEquals(1, clicks[1]); assertEquals(1, f.requests.size());
            f.update.phase = NativeAppUpdate.Phase.DOWNLOADING; f.update.received = f.update.release.size / 2;
            view.bind(f.update); assertFalse(view.action.isEnabled()); assertEquals(50, view.progress.getProgress());
            view.cancel.performClick(); assertEquals(1, clicks[3]);
            f.update.phase = NativeAppUpdate.Phase.READY; view.bind(f.update);
            assertEquals("更新 v0.1.18", view.action.getText().toString());
            assertEquals(android.view.View.GONE, view.status.getVisibility());
            assertEquals("更新をインストール", new NativeUi(RuntimeEnvironment.getApplication(), false, "ja").t("Install update"));
            view.action.performClick(); assertEquals(1, clicks[2]); assertEquals(1, f.requests.size());
        }
    }

    @Test @Config(sdk = 35) public void appearanceUpdateStatesFitThePhoneDialog() throws Exception {
        try (Fixture f = new Fixture(); org.robolectric.android.controller.ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            f.update.check(); f.worker.run();
            File output = new File(System.getProperty("spool.ui.artifacts"), "apk-updates");
            assertTrue(output.isDirectory() || output.mkdirs());
            for (String locale : new String[]{"en", "zh-CN", "ja"}) for (int dialogWidth : new int[]{282, 322, 374}) for (NativeAppUpdate.Phase phase : new NativeAppUpdate.Phase[]{NativeAppUpdate.Phase.IDLE, NativeAppUpdate.Phase.CHECKING, NativeAppUpdate.Phase.AVAILABLE, NativeAppUpdate.Phase.DOWNLOADING, NativeAppUpdate.Phase.READY}) {
                boolean dark = phase == NativeAppUpdate.Phase.DOWNLOADING;
                NativeUi ui = new NativeUi(activity.get(), dark, locale);
                NativeSettings.Content content = new NativeSettings.Content(ui, dark ? "dark" : "light", "system", "0.1.17", (theme, language) -> {}, () -> {});
                NativeUpdateView update = new NativeUpdateView(ui, () -> {}, () -> {}, () -> {}, () -> {});
                content.setUpdateView(update); f.update.phase = phase; f.update.received = f.apk.length / 2; update.bind(f.update);
                int width = ui.dp(dialogWidth);
                content.measure(android.view.View.MeasureSpec.makeMeasureSpec(width, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED));
                content.layout(0, 0, width, content.getMeasuredHeight());
                assertTrue(content.getHeight() < ui.dp(760));
                assertSame(content.versionRow, update.buttons.getParent());
                assertTrue(update.buttons.getLeft() >= content.version.getRight());
                assertTrue(update.buttons.getRight() <= content.versionRow.getWidth());
                assertEquals(0, content.version.getLayout().getEllipsisCount(content.version.getLineCount() - 1));
                assertEquals(locale + " " + phase + " at " + dialogWidth, 1, content.version.getLineCount());
                assertTrue(locale + " " + phase + " at " + dialogWidth + " must show the complete version", content.version.getWidth() >= android.text.Layout.getDesiredWidth(content.version.getText(), content.version.getPaint()));
                android.widget.Button visible = update.action.getVisibility() == android.view.View.VISIBLE ? update.action : update.cancel;
                assertTrue(visible.getWidth() >= visible.getPaint().measureText(visible.getText().toString()) + visible.getPaddingLeft() + visible.getPaddingRight());
                android.graphics.Bitmap bitmap = android.graphics.Bitmap.createBitmap(width, content.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                content.draw(new android.graphics.Canvas(bitmap));
                String defaultLocale = dark ? "zh-CN" : phase == NativeAppUpdate.Phase.IDLE ? "ja" : "en";
                String name = phase.name().toLowerCase(java.util.Locale.ROOT) + (dialogWidth == 322 ? "" : "-" + dialogWidth) + (locale.equals(defaultLocale) ? "" : "-" + locale) + ".png";
                Files.writeString(new File(output, name.replace(".png", ".json")).toPath(), new org.json.JSONObject()
                        .put("row", bounds(content.versionRow)).put("version", bounds(content.version))
                        .put("buttons", bounds(update.buttons)).put("action", bounds(update.action)).put("cancel", bounds(update.cancel)).toString(2));
                try (java.io.FileOutputStream image = new java.io.FileOutputStream(new File(output, name))) {
                    assertTrue(bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, image));
                } finally { bitmap.recycle(); }
            }
        }
    }

    private static org.json.JSONArray bounds(android.view.View view) {
        return new org.json.JSONArray().put(view.getLeft()).put(view.getTop()).put(view.getWidth()).put(view.getHeight());
    }
}
