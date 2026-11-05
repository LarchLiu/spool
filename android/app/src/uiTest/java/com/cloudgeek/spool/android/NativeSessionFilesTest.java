package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.io.File;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.time.Duration;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeSessionFilesTest {
    private JSONObject workspace(String path, boolean available) throws Exception {
        return new JSONObject().put("path", path).put("available", available).put("kind", "worktree").put("branch", "feature/session-files");
    }
    private JSONObject file(String path, boolean external) throws Exception {
        return new JSONObject().put("path", path).put("name", path.substring(path.lastIndexOf('/') + 1))
                .put("external", external).put("kind", "text").put("content", "first\nsecond\nthird").put("size", 18);
    }
    private JSONObject listing() throws Exception { return new JSONObject().put("entries", new JSONArray()); }
    private NativeFilesTest.Pending take(List<NativeFilesTest.Pending> requests, String suffix) {
        for (int i = 0; i < requests.size(); i++) if (requests.get(i).path.endsWith(suffix)) return requests.remove(i);
        throw new AssertionError("Missing request " + suffix + ": " + requests.stream().map(item -> item.path).toList());
    }
    private NativeFileBrowser browser(Activity activity, NativeFileModel model, List<NativeFilesTest.Pending> requests) {
        NativeFileBrowser browser = new NativeFileBrowser(new NativeUi(activity, false), "Project", model, null,
                (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {}, url -> {}, () -> {}, value -> {});
        activity.setContentView(browser); return browser;
    }
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }
    private String text(View view) {
        String value = view instanceof TextView ? ((TextView) view).getText().toString() : "";
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) value += "\n" + text(((ViewGroup) view).getChildAt(i));
        return value;
    }
    private void snapshot(View view, String name) throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"), "session-files"); assertTrue(directory.isDirectory() || directory.mkdirs());
        Bitmap bitmap = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888); view.draw(new Canvas(bitmap));
        try (java.io.OutputStream output = Files.newOutputStream(new File(directory, name + "-api" + android.os.Build.VERSION.SDK_INT + ".png").toPath())) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
        finally { bitmap.recycle(); }
    }

    @Test public void sessionTreeWaitsForWorkspaceAndPathChangesDiscardOldSelectionAndResponses() throws Exception {
        List<NativeFilesTest.Pending> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeFileModel model = new NativeFileModel("project/one", "chat/one");
            NativeFileBrowser browser = browser(controller.get(), model, requests); browser.start();
            assertEquals(1, requests.size());
            assertEquals("/api/chats/chat%2Fone/workspace", requests.get(0).path);
            assertEquals(View.GONE, browser.empty.getVisibility());
            assertTrue(text(browser.notices).contains("Loading session directory…"));
            take(requests, "/workspace").result.done(workspace("/repo/worktrees/one", true), null);
            NativeFilesTest.Pending staleDirectory = take(requests, "/files?path=.");
            browser.select(new NativeFileTarget("README.md", 3, 0));
            take(requests, "/file?path=README.md").result.done(file("README.md", false), null);
            browser.toggleSearch(true); browser.search.setText("readme");
            browser.select(new NativeFileTarget("stale.md", 1, 0));
            NativeFilesTest.Pending staleFile = take(requests, "/file?path=stale.md");
            browser.refreshWorkspace();
            take(requests, "/workspace").result.done(workspace("/repo/worktrees/two", true), null);
            assertNull(model.target); assertNull(model.file); assertNull(browser.split.preview);
            assertEquals("", model.selected); assertFalse(model.searchOpen); assertEquals("", model.query);
            assertTrue(model.expanded.isEmpty());
            staleDirectory.result.done(new JSONObject().put("entries", new JSONArray().put(new JSONObject().put("name", "private").put("path", "private").put("kind", "file"))), null);
            staleFile.result.done(file("stale.md", false), null);
            assertTrue(model.directories.isEmpty()); assertNull(model.file);
            take(requests, "/files?path=.").result.done(listing(), null);
            assertEquals(View.VISIBLE, browser.empty.getVisibility());
            assertTrue(requests.isEmpty()); browser.deactivate();
        }
    }

    @Test public void branchUpdatesKeepSelectionWhileUnavailableAndDeniedWorkspacesNeverFallBackToProject() throws Exception {
        List<NativeFilesTest.Pending> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeFileModel model = new NativeFileModel("p", "c");
            NativeFileBrowser browser = browser(controller.get(), model, requests); browser.start();
            take(requests, "/workspace").result.done(workspace("/repo/worktree", true), null);
            take(requests, "/files?path=.").result.done(listing(), null);
            browser.select(new NativeFileTarget("readme.txt", 2, 0));
            take(requests, "/file?path=readme.txt").result.done(file("readme.txt", false), null);
            JSONObject loaded = model.file;
            browser.refreshWorkspace();
            take(requests, "/workspace").result.done(workspace("/repo/worktree", true).put("branch", "next-branch"), null);
            assertSame(loaded, model.file); assertTrue(text(browser.header.workspaceInfo.popover.content).contains("next-branch"));
            browser.refreshWorkspace();
            take(requests, "/workspace").result.done(workspace("/repo/worktree", false), null);
            // A referenced external file can still be requested when its session directory disappeared.
            requests.clear(); browser.select(new NativeFileTarget("/exports/report.md", 3, 0));
            take(requests, "/file?path=%2Fexports%2Freport.md").result.done(file("/exports/report.md", true), null);
            assertTrue(requests.isEmpty()); assertTrue(model.directories.isEmpty()); assertTrue(model.expanded.isEmpty());
            assertEquals(View.VISIBLE, browser.preview.externalFile.getVisibility());
            assertEquals("report.md:3", browser.preview.name.getText().toString());
            browser.refreshWorkspace(); take(requests, "/workspace").result.done(null, new java.io.IOException("Access denied"));
            assertEquals("Access denied", model.workspaceError);
            assertTrue(text(browser.notices).contains("Access denied"));
            assertTrue(requests.isEmpty());
            browser.notices.getChildAt(0).performClick();
            assertEquals(1, requests.size());
            take(requests, "/workspace").result.done(workspace("/repo/worktree", true), null);
            assertTrue(requests.stream().allMatch(item -> item.path.startsWith("/api/chats/c/")));
            browser.deactivate();
        }
    }

    @Test public void referencedExternalFilesNeverExpandDirectoriesAndDeniedFilesCannotBeDownloaded() throws Exception {
        List<NativeFilesTest.Pending> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeFileModel model = new NativeFileModel("p", "c");
            NativeFileBrowser browser = browser(controller.get(), model, requests); browser.start();
            take(requests, "/workspace").result.done(workspace("/repo", true), null);
            take(requests, "/files?path=.").result.done(listing(), null);
            browser.select(new NativeFileTarget("/exports/report.md", 3, 0));
            take(requests, "/file?path=%2Fexports%2Freport.md").result.done(file("/exports/report.md", true), null);
            assertTrue(requests.isEmpty()); assertTrue(model.expanded.isEmpty());
            assertEquals("/api/chats/c/file?path=%2Fexports%2Freport.md&download=1", model.filePath(model.file.getString("path"), "download"));
            browser.select(new NativeFileTarget("/exports/private.md", 1, 0));
            take(requests, "/file?path=%2Fexports%2Fprivate.md").result.done(null, new java.io.IOException("File access denied"));
            assertNull(model.file); assertEquals(View.GONE, browser.preview.download.getVisibility());
            assertEquals(View.GONE, browser.preview.scroll.getVisibility());
            assertTrue(text(browser.preview.notices).contains("File access denied"));
            assertTrue(requests.isEmpty()); browser.deactivate();
        }
    }

    @Test public void pollingAndForegroundResumeRefreshMetadataAndIgnoreClosedCallbacks() throws Exception {
        List<NativeFilesTest.Pending> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeFileModel model = new NativeFileModel("p", "c");
            NativeFileBrowser browser = browser(controller.get(), model, requests); browser.start();
            take(requests, "/workspace").result.done(workspace("/repo", true), null);
            take(requests, "/files?path=.").result.done(listing(), null);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(10));
            assertEquals(1, requests.size()); browser.refreshWorkspace(); assertEquals(1, requests.size());
            NativeFilesTest.Pending stale = take(requests, "/workspace"); browser.deactivate();
            stale.result.done(workspace("/wrong", true), null);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(30)); assertTrue(requests.isEmpty());
            browser.resume(); assertEquals(1, requests.size());
            take(requests, "/workspace").result.done(workspace("/new", true), null);
            assertEquals("/new", model.workspace.getString("path")); browser.deactivate();
        }
    }

    @Test public void sessionImagesUseEncodedAuthenticatedChatPathsAndTheBinaryPolicyStaysNarrow() throws Exception {
        for (String mode : new String[] {"", "image", "download"}) {
            String path = NativeFileData.filePath("project", "chat/id", "/exports/a + b&c.png", mode);
            assertTrue(NativeFileData.allowedPath(path));
            assertEquals("/api/chats/chat%2Fid/file", android.net.Uri.parse(path).getEncodedPath());
            assertEquals("/exports/a + b&c.png", android.net.Uri.parse(path).getQueryParameter("path"));
        }
        for (String path : new String[] {"https://example.org/api/chats/c/file?path=x", "/api/chats/c/files?path=x", "/api/chats/c/file?path=x&redirect=1", "/api/chats/c/file?path=x&download=1&image=1"}) assertFalse(path, NativeFileData.allowedPath(path));
        List<String> reads = new ArrayList<>(); List<Runnable> jobs = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeImages images = new NativeImages(jobs::add, path -> { reads.add(path); throw new java.io.IOException("Image unavailable"); });
            NativeFilePreview preview = new NativeFilePreview(ui, "p", "chat/id", images, target -> {}, url -> {}, value -> {}, () -> {});
            controller.get().setContentView(preview);
            preview.bind(file("/exports/picture.png", true).put("kind", "image"), new NativeFileTarget("/exports/picture.png", 0, 0), "");
            jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(List.of("/api/chats/chat%2Fid/file?path=%2Fexports%2Fpicture.png&image=1"), reads); images.clear();
        }
    }

    @Test public void changingWorktreesDoesNotReuseAnImageWithTheSameChatAndRelativePath() throws Exception {
        List<NativeFilesTest.Pending> requests = new ArrayList<>(); List<Runnable> jobs = new ArrayList<>(); List<String> reads = new ArrayList<>();
        Bitmap image = Bitmap.createBitmap(4, 4, Bitmap.Config.ARGB_8888);
        java.io.ByteArrayOutputStream output = new java.io.ByteArrayOutputStream(); assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); image.recycle();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); NativeFileModel model = new NativeFileModel("p", "c");
            NativeImages images = new NativeImages(jobs::add, path -> { reads.add(path); return new NativeFileData("image/png", output.toByteArray()); });
            NativeFileBrowser browser = new NativeFileBrowser(ui, "Project", model, images,
                    (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {}, url -> {}, () -> {}, value -> {});
            controller.get().setContentView(browser); browser.start();
            JSONObject picture = file("picture.png", false).put("kind", "image");
            take(requests, "/workspace").result.done(workspace("/repo/one", true), null);
            take(requests, "/files?path=.").result.done(listing(), null);
            browser.select(new NativeFileTarget("picture.png", 0, 0)); take(requests, "/file?path=picture.png").result.done(picture, null);
            assertEquals(1, jobs.size()); jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            browser.refreshWorkspace(); take(requests, "/workspace").result.done(workspace("/repo/two", true), null);
            take(requests, "/files?path=.").result.done(listing(), null);
            browser.select(new NativeFileTarget("picture.png", 0, 0)); take(requests, "/file?path=picture.png").result.done(picture, null);
            assertEquals("A different checkout must fetch its own bytes", 1, jobs.size());
            jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(List.of("/api/chats/c/file?path=picture.png&image=1", "/api/chats/c/file?path=picture.png&image=1"), reads);
            browser.deactivate(); images.clear();
        }
    }

    @Test public void bothDrawersFillTheViewportAndWorkspacePopoverShowsPathBranchAndDetachedCommit() throws Exception {
        for (int width : new int[] {320, 360, 412, 800}) for (boolean dark : new boolean[] {false, true}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, "zh-CN");
                for (boolean right : new boolean[] {false, true}) {
                    NativeDrawer drawer = new NativeDrawer(ui, right, () -> {}); controller.get().setContentView(drawer);
                    layout(drawer, width, 800); assertEquals(width, drawer.sheet.getWidth()); assertEquals(0, drawer.sheet.getLeft());
                }
                NativeFileModel model = new NativeFileModel("p", "c"); model.workspace = workspace("/repo/worktrees/feature-session-files", true);
                model.file = file("/exports/report.md", true); model.target = model.loadedTarget = new NativeFileTarget("/exports/report.md", 2, 0);
                NativeFileBrowser browser = new NativeFileBrowser(ui, "项目名称", model, null, (path, done) -> {}, file -> {}, url -> {}, () -> {}, value -> {});
                controller.get().setContentView(browser); layout(browser, width, 800);
                assertEquals(browser.header.title.getChildAt(1).getRight() + ui.dp(8), browser.header.info.getLeft());
                assertEquals(ui.dp(14), browser.header.info.getWidth() - browser.header.info.getPaddingLeft() - browser.header.info.getPaddingRight());
                if (width == 360) snapshot(browser, "files-" + (dark ? "dark" : "light"));
                assertEquals(View.VISIBLE, browser.header.info.getVisibility()); browser.header.info.performClick();
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertTrue(browser.header.workspaceInfo.popover.isShowing());
                View panel = browser.header.workspaceInfo.popover.panel();
                assertTrue(text(panel).contains("/repo/worktrees/feature-session-files")); assertTrue(text(panel).contains("feature/session-files"));
                assertTrue(text(panel).contains("Worktree")); assertTrue(panel.getWidth() <= width - 16);
                if (width == 360) {
                    snapshot(panel, "workspace-" + (dark ? "dark" : "light"));
                }
                model.workspace = workspace("/repo", true).put("kind", "main").put("branch", "").put("commit", "a1b2c3d");
                browser.header.workspace(model);
                assertTrue(text(panel).contains(ui.t("Main checkout"))); assertTrue(text(panel).contains(ui.t("Detached HEAD · {commit}", "commit", "a1b2c3d")));
                browser.deactivate(); assertFalse(browser.header.workspaceInfo.popover.isShowing());
            }
        }
    }
}
