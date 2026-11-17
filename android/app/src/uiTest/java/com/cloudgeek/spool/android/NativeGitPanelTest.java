package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import java.io.File;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeGitPanelTest {
    static final String PATCH = "diff --git a/src/file.txt b/src/file.txt\n--- a/src/file.txt\n+++ b/src/file.txt\n@@ -1,2 +1,2 @@\n first\n-old\n+new\n";
    private static JSONObject workspace(String path) throws Exception { return new JSONObject().put("path", path).put("available", true).put("kind", "worktree"); }
    private static JSONObject changes() throws Exception {
        return new JSONObject().put("repository", true).put("branch", "feature/git-panel").put("commits", new JSONArray())
            .put("files", new JSONArray().put(new JSONObject().put("path", "src/file.txt").put("status", "M").put("additions", 1).put("deletions", 1)));
    }
    private static NativeFilesTest.Pending take(List<NativeFilesTest.Pending> requests, String suffix) {
        for (int i = 0; i < requests.size(); i++) if (requests.get(i).path.contains(suffix)) return requests.remove(i);
        throw new AssertionError("Missing " + suffix + ": " + requests.stream().map(item -> item.path).toList());
    }
    private static void layout(View view) {
        view.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY)); view.layout(0, 0, 360, 800);
    }
    private static View find(View view, String text) {
        if (view instanceof TextView && ((TextView) view).getText().toString().contains(text)) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) { View found = find(((ViewGroup) view).getChildAt(i), text); if (found != null) return found; }
        return null;
    }
    private static <T> T child(View view, Class<T> type) {
        if (type.isInstance(view)) return type.cast(view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) { T found = child(((ViewGroup) view).getChildAt(i), type); if (found != null) return found; }
        return null;
    }
    private static View action(View view, String description) {
        if (description.contentEquals(view.getContentDescription() == null ? "" : view.getContentDescription())) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) { View found = action(((ViewGroup) view).getChildAt(i), description); if (found != null) return found; }
        return null;
    }
    @Test public void headerRefreshOnlyReloadsCurrentListAndPreviewIconsOnlyReloadTheirContents() throws Exception {
        try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), false); List<NativeFilesTest.Pending> requests = new ArrayList<>();
            NativeFileModel model = new NativeFileModel("p");
            NativeFileBrowser browser = new NativeFileBrowser(ui, "Project", model, null, (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {}, value -> {}, () -> {}, value -> {});
            activity.get().setContentView(browser); browser.start();
            JSONObject listing = new JSONObject().put("entries", new JSONArray().put(new JSONObject().put("name", "a.txt").put("path", "a.txt").put("kind", "file")));
            take(requests, "/files?").result.done(listing, null);
            browser.select(new NativeFileTarget("a.txt", 0, 0));
            JSONObject file = new JSONObject().put("path", "a.txt").put("name", "a.txt").put("kind", "text").put("content", "old");
            take(requests, "/file?").result.done(file, null); assertTrue(requests.isEmpty());
            browser.header.refresh.performClick();
            assertEquals(ui.t("Refresh files"), browser.header.refresh.getContentDescription());
            assertEquals(1, requests.size()); take(requests, "/files?").result.done(listing, null); assertSame(file, model.file);
            browser.preview.refresh.performClick();
            assertEquals(1, requests.size()); take(requests, "/file?").result.done(new JSONObject(file.toString()).put("content", "new"), null);
            assertEquals("new", model.file.optString("content")); assertTrue(requests.isEmpty());
            browser.selectPanel(true); take(requests, "view=changes").result.done(changes(), null);
            browser.gitPanel.select("src/file.txt"); take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH), null);
            browser.header.refresh.performClick(); assertEquals(ui.t("Refresh Git information"), browser.header.refresh.getContentDescription());
            assertEquals(1, requests.size()); take(requests, "view=changes").result.done(changes(), null); assertTrue(requests.isEmpty());
            action(browser.gitPanel.preview, ui.t("Refresh diff preview")).performClick();
            assertEquals(1, requests.size()); take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH.replace("+new", "+refreshed")), null);
            assertEquals("refreshed", child(browser.gitPanel.preview, NativeUnifiedDiff.class).rows.get(2).text);
            assertTrue(requests.isEmpty());
            browser.gitPanel.select("src/file.txt"); NativeFilesTest.Pending stale = take(requests, "view=diff");
            browser.selectPanel(false); stale.result.done(new JSONObject().put("patch", PATCH), null);
            browser.selectPanel(true);
            take(requests, "view=changes").result.done(changes(), null);
            take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH), null);
            assertNotNull(child(browser.gitPanel.preview, NativeUnifiedDiff.class));
            assertTrue(requests.isEmpty()); browser.deactivate();
        }
    }
    @Test public void customTabsOpenUnifiedDiffWithWorkingResizeAndFenceOldWorkspaceResponses() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            List<NativeFilesTest.Pending> requests = new ArrayList<>(); NativeFileModel model = new NativeFileModel("p", "c");
            NativeUi ui = new NativeUi(activity.get(), dark);
            NativeFileBrowser browser = new NativeFileBrowser(ui, "Project", model, null, (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {}, value -> {}, () -> {}, value -> {});
            activity.get().setContentView(browser); browser.start();
            JSONObject main = workspace("/repo/main"), tree = workspace("/repo/tree");
            take(requests, "/workspace").result.done(new JSONObject(main.toString()).put("workspaces", new JSONArray().put(main).put(tree)), null);
            take(requests, "/files?").result.done(new JSONObject().put("entries", new JSONArray()), null);
            assertTrue(requests.isEmpty()); browser.panelTabs.action("git").performClick();
            assertEquals(View.GONE, browser.split.getVisibility()); assertEquals(View.VISIBLE, browser.gitPanel.getVisibility());
            assertNull(child(browser.gitPanel, android.widget.Button.class));
            NativeFilesTest.Pending listing = take(requests, "/git?"); assertTrue(listing.path.contains("workspace=%2Frepo%2Fmain")); listing.result.done(changes(), null);
            find(browser.gitPanel.content, "file.txt").performClick();
            take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH), null); layout(browser);
            NativeUnifiedDiff diff = child(browser.gitPanel.preview, NativeUnifiedDiff.class); assertNotNull(diff);
            assertEquals(3, diff.rows.size());
            assertEquals('-', diff.rows.get(1).kind); assertEquals(2, diff.rows.get(1).old); assertEquals('+', diff.rows.get(2).kind); assertEquals(2, diff.rows.get(2).next);
            double fraction = browser.gitPanel.split.current(); browser.gitPanel.split.handle.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_DOWN)); assertTrue(browser.gitPanel.split.current() > fraction);
            browser.gitPanel.split.update(.6); layout(browser); assertEquals(.6, browser.gitPanel.split.current(), .001);
            File output = new File(System.getProperty("spool.ui.artifacts"), "git-panel"); assertTrue(output.isDirectory() || output.mkdirs());
            Bitmap image = Bitmap.createBitmap(360, 800, Bitmap.Config.ARGB_8888); browser.draw(new Canvas(image));
            try (java.io.OutputStream stream = Files.newOutputStream(new File(output, "git-" + (dark ? "dark" : "light") + "-api" + android.os.Build.VERSION.SDK_INT + ".png").toPath())) { image.compress(Bitmap.CompressFormat.PNG, 100, stream); } finally { image.recycle(); }
            browser.gitPanel.tabs.action("history").performClick();
            String id = "a".repeat(40);
            take(requests, "view=history").result.done(new JSONObject().put("repository", true).put("files", new JSONArray()).put("commits", new JSONArray().put(new JSONObject().put("id", id).put("subject", "Historical commit").put("author", "Author"))), null);
            find(browser.gitPanel.content, "Historical commit").performClick();
            NativeFilesTest.Pending old = take(requests, "commit="); browser.selectWorkspace("/repo/tree");
            old.result.done(changes(), null); assertNull(find(browser.gitPanel.content, "file.txt"));
            NativeFilesTest.Pending next = take(requests, "/git?"); assertTrue(next.path.contains("workspace=%2Frepo%2Ftree")); next.result.done(changes(), null);
            browser.gitPanel.select("src/file.txt"); NativeFilesTest.Pending stale = take(requests, "view=diff"); browser.deactivate();
            stale.result.done(new JSONObject().put("patch", PATCH), null); assertNull(child(browser.gitPanel.preview, NativeUnifiedDiff.class));
        }
    }
}
