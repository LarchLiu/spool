package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import android.widget.HorizontalScrollView;
import android.widget.ScrollView;
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
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowDialog;

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
    private static void paths(JSONObject write, String... expected) throws Exception {
        JSONArray paths = write.getJSONArray("paths"); assertEquals(expected.length, paths.length());
        for (int i = 0; i < expected.length; i++) assertEquals(expected[i], paths.getString(i));
    }
    private static void screenshot(View view, String name) throws Exception {
        layout(view); File output = new File(System.getProperty("spool.ui.artifacts"), "git-panel"); assertTrue(output.isDirectory() || output.mkdirs());
        Bitmap image = Bitmap.createBitmap(360, 800, Bitmap.Config.ARGB_8888); view.draw(new Canvas(image));
        try (java.io.OutputStream stream = Files.newOutputStream(new File(output, name + "-api" + android.os.Build.VERSION.SDK_INT + ".png").toPath())) { image.compress(Bitmap.CompressFormat.PNG, 100, stream); } finally { image.recycle(); }
    }
    private static JSONObject historyPage(int offset, boolean updated) throws Exception {
        JSONObject value = changes().put("files", new JSONArray()).put("commit", "a".repeat(40)); JSONArray commits = new JSONArray();
        for (int i = offset; i < Math.min(offset + 50, 105); i++) {
            JSONObject commit = new JSONObject().put("id", String.format("%040x", i + 1)).put("subject", "History row " + (i + 1)).put("author", "Developer").put("time", 1794900000 - i * 3600)
                .put("unpushed", !updated && i < 3);
            if (i == 0) commit.put("tags", new JSONArray().put(updated ? "v1.2.1" : "v1.2.0"));
            if (i == 55) commit.put("tags", new JSONArray().put("release/v1.1"));
            commits.put(commit);
        }
        value.put("commits", commits); if (offset + 50 < 105) value.put("next_cursor", "a".repeat(40) + ":" + (offset + 50)); return value;
    }
    private static int historyRows(NativeGitPanel panel) {
        int count = 0; for (int i = 0; i < panel.content.getChildCount(); i++) if (panel.content.getChildAt(i) instanceof NativeGitHistoryRow) count++; return count;
    }
    @Test public void commitDetailsShowBadgesAuthorFullTimeAndMessageAndRefreshTheirState() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), dark, "en"); NativeFileModel model = new NativeFileModel("p", "c"); model.workspace = workspace("/repo/main");
            List<NativeFilesTest.Pending> requests = new ArrayList<>();
            NativeGitPanel panel = new NativeGitPanel(ui, model, (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {});
            activity.get().setContentView(panel); panel.activate(true); take(requests, "view=changes").result.done(changes(), null);
            panel.tabs.action("history").performClick(); JSONObject history = historyPage(0, false); take(requests, "view=history").result.done(history, null);
            find(panel.content, "History row 1").performClick();
            JSONObject selected = new JSONObject(history.getJSONArray("commits").getJSONObject(0).toString());
            String message = "History row 1\n\nPreserve the full commit message and its paragraphs.\n第二行提交说明\n\nFinal paragraph.";
            selected.put("message", message); JSONObject detail = changes().put("selected_commit", selected);
            NativeFilesTest.Pending load = take(requests, "view=changes"); assertTrue(load.path.contains("commit=" + selected.getString("id"))); load.result.done(detail, null); layout(panel);
            assertNotNull(action(panel.content, "v1.2.0 · Not pushed"));
            assertEquals("v1.2.0 ↑", ((TextView) action(panel.content, "v1.2.0 · Not pushed")).getText().toString());
            String stamp = java.text.DateFormat.getDateTimeInstance(java.text.DateFormat.MEDIUM, java.text.DateFormat.MEDIUM).format(new java.util.Date(selected.getLong("time") * 1000));
            assertNotNull(find(panel.content, "Developer · " + stamp));
            TextView body = (TextView) find(panel.content, "Final paragraph."); assertEquals(message, body.getText().toString()); assertTrue(body.getLineCount() >= 6);
            assertTrue(body.getRight() <= panel.getWidth()); assertNull(child(panel, android.widget.Button.class));
            assertNotNull(find(panel.content, selected.getString("id")));
            View copy = action(panel.content, "Copy SHA"); assertNotNull(copy);
            assertTrue(copy.getLeft() >= find(panel.content, selected.getString("id")).getRight());
            screenshot(panel, "commit-detail-" + (dark ? "dark" : "light"));
            copy.performClick(); assertNotNull(action(panel.content, "Copied"));
            android.content.ClipboardManager clipboard = (android.content.ClipboardManager) ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            assertEquals(selected.getString("id"), clipboard.getPrimaryClip().getItemAt(0).getText().toString());
            find(panel.content, "file.txt").performClick(); take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH), null);
            NativeUnifiedDiff preview = child(panel.preview, NativeUnifiedDiff.class); assertNotNull(preview);
            panel.reload(); selected.put("unpushed", false).put("tags", new JSONArray().put("v1.2.1")); take(requests, "view=changes").result.done(detail, null);
            assertNull(action(panel.content, "v1.2.0 · Not pushed")); assertNotNull(action(panel.content, "v1.2.1")); assertSame(preview, child(panel.preview, NativeUnifiedDiff.class));
            find(panel.content, "History · ").performClick(); take(requests, "view=history").result.done(historyPage(0, true), null); assertEquals(50, historyRows(panel));
            panel.activate(false);
        }
    }
    @Test public void historyBadgesAndBottomPaginationPreserveRowsAndFenceOldDirectories() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), dark, "en"); NativeFileModel model = new NativeFileModel("p", "c");
            model.workspace = workspace("/repo/main"); model.workspaces = new JSONArray().put(model.workspace);
            List<NativeFilesTest.Pending> requests = new ArrayList<>();
            NativeGitPanel panel = new NativeGitPanel(ui, model, (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {});
            activity.get().setContentView(panel); panel.activate(true); take(requests, "view=changes").result.done(changes(), null);
            panel.tabs.action("history").performClick(); take(requests, "view=history").result.done(historyPage(0, false), null); layout(panel);
            assertEquals(50, historyRows(panel)); assertTrue(requests.isEmpty());
            View first = panel.content.getChildAt(1); assertTrue(first.getContentDescription().toString().contains("Not pushed")); assertTrue(first.getContentDescription().toString().contains("v1.2.0"));
            assertEquals("History row 1 v1.2.0 ↑", child(first, NativeTextView.class).getText().toString());
            screenshot(panel, "history-" + (dark ? "dark" : "light"));
            panel.scroll.scrollTo(0, panel.content.getHeight());
            int offset = panel.scroll.getScrollY(); View last = panel.content.getChildAt(50);
            int lastY = last.getTop() - offset;
            layout(panel); assertSame(first, panel.content.getChildAt(1));
            assertEquals(offset, panel.scroll.getScrollY()); assertEquals(lastY, last.getTop() - panel.scroll.getScrollY());
            take(requests, "cursor=").result.done(null, new Exception("History page temporarily unavailable"));
            layout(panel); assertSame(last, panel.content.getChildAt(50)); assertEquals(offset, panel.scroll.getScrollY());
            assertEquals(50, historyRows(panel)); assertNotNull(find(panel.content, "History page temporarily unavailable"));
            find(panel.content, "Retry").performClick(); NativeFilesTest.Pending more = take(requests, "cursor="); assertTrue(more.path.contains("%3A50"));
            more.result.done(historyPage(50, false), null); layout(panel); assertEquals(100, historyRows(panel));
            assertSame(first, panel.content.getChildAt(1)); assertSame(last, panel.content.getChildAt(50));
            assertEquals(offset, panel.scroll.getScrollY()); assertEquals(lastY, last.getTop() - panel.scroll.getScrollY());
            assertNotNull(find(panel.content, "release/v1.1"));
            panel.scroll.scrollTo(0, panel.content.getHeight()); more = take(requests, "cursor="); assertTrue(more.path.contains("%3A100"));
            more.result.done(historyPage(100, false), null); layout(panel); assertEquals(105, historyRows(panel));
            panel.scroll.scrollTo(0, panel.content.getHeight()); assertTrue(requests.isEmpty());
            panel.reload(); take(requests, "view=history").result.done(historyPage(0, true), null);
            take(requests, "cursor=").result.done(historyPage(50, true), null); take(requests, "cursor=").result.done(historyPage(100, true), null);
            assertEquals(105, historyRows(panel)); assertNotNull(find(panel.content, "v1.2.1"));
            assertFalse(panel.content.getChildAt(1).getContentDescription().toString().contains("Not pushed"));
            // An append in flight belongs only to the directory that requested it.
            panel.scopeChanged(); take(requests, "view=history").result.done(historyPage(0, false), null); layout(panel);
            panel.scroll.scrollTo(0, panel.content.getHeight()); NativeFilesTest.Pending stale = take(requests, "cursor=");
            model.workspace = workspace("/repo/tree"); model.epoch++; panel.scopeChanged();
            NativeFilesTest.Pending next = take(requests, "view=history"); assertTrue(next.path.contains("workspace=%2Frepo%2Ftree"));
            JSONObject feature = changes().put("commits", new JSONArray().put(new JSONObject().put("id", "f".repeat(40)).put("subject", "Feature-only history")));
            next.result.done(feature, null); stale.result.done(historyPage(50, false), null);
            assertEquals(1, historyRows(panel)); assertNotNull(find(panel.content, "Feature-only history")); assertNull(find(panel.content, "History row 51"));
            panel.activate(false);
        }
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
    @Test public void listPollingPreservesBothDiffScrollPositions() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            List<NativeFilesTest.Pending> requests = new ArrayList<>();
            NativeGitPanel panel = new NativeGitPanel(new NativeUi(activity.get(), false, "en"), new NativeFileModel("p"),
                (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {});
            activity.get().setContentView(panel); panel.activate(true);
            take(requests, "view=changes").result.done(changes(), null);
            panel.select("src/file.txt");
            StringBuilder patch = new StringBuilder("--- /dev/null\n+++ b/src/file.txt\n@@ -0,0 +1,100 @@\n");
            for (int i = 0; i < 100; i++) patch.append("+line ").append(i).append(" long text".repeat(30)).append('\n');
            take(requests, "view=diff").result.done(new JSONObject().put("patch", patch.toString()), null); layout(panel);
            ScrollView vertical = child(panel.preview, ScrollView.class); HorizontalScrollView horizontal = child(panel.preview, HorizontalScrollView.class);
            NativeUnifiedDiff original = child(panel.preview, NativeUnifiedDiff.class);
            child(panel.preview, ScrollView.class).scrollTo(0, 200);
            child(panel.preview, HorizontalScrollView.class).scrollTo(80, 0);
            assertEquals(200, child(panel.preview, ScrollView.class).getScrollY());
            assertEquals(80, child(panel.preview, HorizontalScrollView.class).getScrollX());
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes().put("branch", "refreshed-branch"), null); layout(panel);
            take(requests, "view=diff").result.done(new JSONObject().put("patch", patch.toString()), null); layout(panel);
            assertNotNull(find(panel.content, "refreshed-branch"));
            assertSame(original, child(panel.preview, NativeUnifiedDiff.class));
            assertEquals(200, child(panel.preview, ScrollView.class).getScrollY());
            assertEquals(80, child(panel.preview, HorizontalScrollView.class).getScrollX());
            assertTrue(requests.isEmpty());
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes(), null); NativeFilesTest.Pending slow = take(requests, "view=diff");
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes(), null); assertTrue("A slow diff request must not be duplicated", requests.isEmpty());
            slow.result.done(new JSONObject().put("patch", patch.toString().replace("+line 0 ", "+updated line 0 ")), null); layout(panel);
            assertTrue(child(panel.preview, NativeUnifiedDiff.class).rows.get(0).text.startsWith("updated line 0 "));
            assertSame(vertical, child(panel.preview, ScrollView.class)); assertSame(horizontal, child(panel.preview, HorizontalScrollView.class));
            assertEquals(200, vertical.getScrollY()); assertEquals(80, horizontal.getScrollX());
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes(), null);
            take(requests, "view=diff").result.done(null, new Exception("Diff temporarily unavailable")); layout(panel);
            assertNotNull(find(panel.preview, "Diff temporarily unavailable")); assertNotNull(child(panel.preview, NativeUnifiedDiff.class));
            assertEquals(200, vertical.getScrollY()); assertEquals(80, horizontal.getScrollX());
            action(panel.preview, "Refresh diff preview").performClick();
            take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH), null); layout(panel);
            assertEquals("new", child(panel.preview, NativeUnifiedDiff.class).rows.get(2).text);
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes(), null); slow = take(requests, "view=diff");
            action(panel.preview, "Close diff").performClick();
            slow.result.done(new JSONObject().put("patch", patch.toString()), null);
            assertNull(child(panel.preview, NativeUnifiedDiff.class));
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes(), null);
            assertTrue(requests.isEmpty());
            panel.select("src/file.txt"); take(requests, "view=diff").result.done(new JSONObject().put("patch", PATCH), null);
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10));
            take(requests, "view=changes").result.done(changes().put("files", new JSONArray()), null);
            assertNull(child(panel.preview, NativeUnifiedDiff.class)); assertTrue("Removed files must not trigger a diff read", requests.isEmpty());
            panel.activate(false);
            Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(10)); assertTrue(requests.isEmpty());
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

    @Test public void partialStageControlsCommitFooterAndRemoteOperationsUseTheCurrentScope() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), dark, "en"); NativeFileModel model = new NativeFileModel("p", "c");
            JSONObject tree = workspace("/repo/tree"); model.workspace = tree; model.workspaces = new JSONArray().put(tree);
            List<NativeFilesTest.Pending> requests = new ArrayList<>(); List<JSONObject> writes = new ArrayList<>();
            List<NativeFileBrowser.Completion> completions = new ArrayList<>();
            NativeGitPanel panel = new NativeGitPanel(ui, model, (path, done) -> requests.add(new NativeFilesTest.Pending(path, done)), value -> {});
            panel.write = (path, body, done) -> { assertTrue(path.contains("workspace=%2Frepo%2Ftree")); writes.add(body); completions.add(done); };
            activity.get().setContentView(panel); panel.activate(true);
            String id = "b".repeat(40);
            JSONObject listing = changes().put("writable", true).put("index", "snapshot").put("commit", id).put("remote", "origin").put("remote_branch", "feature/git-panel")
                .put("remote_allowed", true).put("upstream", "origin/feature/git-panel").put("ahead", 2)
                .put("last_commit", new JSONObject().put("id", id).put("subject", "Latest commit").put("author", "Developer"));
            JSONObject file = listing.getJSONArray("files").getJSONObject(0); file.put("staged", true).put("unstaged", true).put("status", "MM");
            take(requests, "view=changes").result.done(listing, null); layout(panel);
            View stage = action(panel.content, "Stage src/file.txt"), unstage = action(panel.content, "Unstage src/file.txt");
            assertNotNull(stage); assertNotNull(unstage);
            assertFalse(stage.createAccessibilityNodeInfo().isCheckable()); assertEquals("android.widget.Button", stage.createAccessibilityNodeInfo().getClassName());
            assertEquals(View.VISIBLE, panel.commitArea.getVisibility()); assertTrue(panel.lastCommit.getText().toString().contains("Latest commit"));
            assertNotNull(find(panel.content, "feature/git-panel")); assertTrue(panel.commitArea.getTop() >= panel.split.getBottom());
            assertTrue(panel.remoteArea.getTop() >= panel.commitArea.getBottom()); assertNull(action(panel.remoteArea, "Fetch"));
            String collapsedLabel = ((TextView) find(panel.commitArea, "Commit staged changes")).getText().toString();
            action(panel.commitArea, "Expand commit area").setHovered(true);
            screenshot(panel, "collapsed-" + (dark ? "dark" : "light"));
            action(panel.commitArea, "Expand commit area").setHovered(false);
            TextView collapsedText = (TextView) find(panel.commitArea, "Commit staged changes");
            assertTrue(collapsedText.isShown()); assertTrue(collapsedText.getWidth() >= collapsedText.getPaint().measureText(collapsedLabel));
            Bitmap labelImage = Bitmap.createBitmap(collapsedText.getWidth(), collapsedText.getHeight(), Bitmap.Config.ARGB_8888);
            try {
                collapsedText.draw(new Canvas(labelImage)); int[] pixels = new int[labelImage.getWidth() * labelImage.getHeight()];
                labelImage.getPixels(pixels, 0, labelImage.getWidth(), 0, 0, labelImage.getWidth(), labelImage.getHeight());
                assertTrue("Collapsed commit information paints visible text", java.util.Arrays.stream(pixels).anyMatch(pixel -> (pixel >>> 24) > 0));
            } finally { labelImage.recycle(); }
            assertFalse(find(panel.commitArea, "Commit staged changes").performClick()); assertFalse(panel.commitInput.isShown());
            action(panel.commitArea, "Expand commit area").performClick(); layout(panel);
            int oneLineHeight = panel.commitInput.getHeight();
            panel.commitInput.setText("Commit line 1\nCommit line 2\nCommit line 3"); layout(panel);
            int threeLineHeight = panel.commitInput.getHeight(); assertTrue(threeLineHeight > oneLineHeight);
            String sixLines = "Commit line 1\nCommit line 2\nCommit line 3\nCommit line 4\nCommit line 5\nCommit line 6";
            panel.commitInput.setText(sixLines); layout(panel); int maximumHeight = panel.commitInput.getHeight(); assertTrue(maximumHeight > threeLineHeight);
            panel.commitInput.setText(sixLines + "\n" + sixLines); layout(panel);
            assertEquals(maximumHeight, panel.commitInput.getHeight()); assertTrue(panel.commitInput.canScrollVertically(1));
            panel.commitInput.requestFocus(); action(panel.commitArea, "Collapse commit area").setHovered(true);
            screenshot(panel, "commit-max-" + (dark ? "dark" : "light"));
            action(panel.commitArea, "Collapse commit area").setHovered(false);
            panel.commitInput.setText("Commit from Android"); layout(panel); assertEquals(oneLineHeight, panel.commitInput.getHeight());
            assertNull(find(panel.commitArea, "Cancel"));
            TextView expandedSubmit = (TextView) action(panel.commitArea, "Commit staged changes (1)");
            assertEquals(collapsedLabel, expandedSubmit.getText().toString());
            action(panel.commitArea, "Collapse commit area").performClick();
            assertFalse(panel.commitInput.isShown());
            assertEquals(collapsedLabel, ((TextView) find(panel.commitArea, "Commit staged changes")).getText().toString());
            action(panel.commitArea, "Expand commit area").performClick(); assertEquals("Commit from Android", panel.commitInput.getText().toString());
            panel.commitInput.requestFocus(); panel.commitInput.selectAll();
            screenshot(panel, "staging-" + (dark ? "dark" : "light"));
            find(panel.content, "file.txt").performClick();
            NativeFilesTest.Pending diff = take(requests, "view=diff"); assertTrue(diff.path.contains("side=staged")); diff.result.done(new JSONObject().put("patch", PATCH), null);
            assertEquals(View.GONE, panel.commitArea.getVisibility()); assertEquals(View.VISIBLE, panel.lastCommit.getVisibility()); assertEquals(View.VISIBLE, panel.remoteArea.getVisibility());
            action(panel.preview, "Close diff").performClick(); assertEquals(View.VISIBLE, panel.commitArea.getVisibility()); assertEquals("Commit from Android", panel.commitInput.getText().toString());
            unstage.performClick(); assertEquals("unstage", writes.get(0).getString("action")); assertEquals(id, writes.get(0).getString("head"));
            assertNull(find(panel.content, "Running Git operation…"));
            assertFalse(action(panel.content, "Stage src/file.txt").isEnabled());
            completions.remove(0).done(new JSONObject().put("ok", true), null);
            file.put("staged", false); take(requests, "view=changes").result.done(listing, null);
            action(panel.content, "Stage src/file.txt").performClick(); assertEquals("stage", writes.get(1).getString("action"));
            completions.remove(0).done(new JSONObject().put("ok", true), null);
            file.put("staged", true).put("unstaged", false); take(requests, "view=changes").result.done(listing, null);
            View submit = action(panel.commitArea, "Commit staged changes (1)"); assertTrue(submit.isShown()); assertTrue(submit.isEnabled()); submit.performClick();
            assertEquals("commit", writes.get(2).getString("action")); assertEquals("Commit from Android", writes.get(2).getString("message")); assertEquals("snapshot", writes.get(2).getString("index"));
            completions.remove(0).done(new JSONObject().put("ok", true), null);
            file.put("staged", false); listing.getJSONObject("last_commit").put("subject", "Commit from Android"); take(requests, "view=changes").result.done(listing, null);
            assertEquals("", panel.commitInput.getText().toString()); assertTrue(panel.lastCommit.getText().toString().contains("Commit from Android"));
            action(panel.remoteArea, "Git remote actions").performClick(); assertNotNull(ui.activePopover); assertEquals(2, ui.activePopover.content.getChildCount());
            action(ui.activePopover.content, "Fetch").performClick(); assertEquals(3, writes.size()); assertTrue(completions.isEmpty());
            TextView fetch = (TextView) action(panel.remoteArea, "Fetch"); assertNotNull(fetch); assertNotNull(fetch.getCompoundDrawablesRelative()[0]);
            assertFalse(fetch.getText().toString().contains("↓"));
            action(panel.remoteArea, "Git remote actions").performClick(); assertTrue(action(ui.activePopover.content, "Fetch").isSelected());
            ui.activePopover.dismiss(); fetch.performClick(); assertEquals("fetch", writes.get(3).getString("action")); assertEquals("origin", writes.get(3).getString("remote"));
            completions.remove(0).done(new JSONObject().put("ok", true), null); take(requests, "view=changes").result.done(listing, null);
            assertNull(find(panel.content, "Git operation completed.")); assertNull(action(panel.content, "Dismiss"));
            action(panel.remoteArea, "Git remote actions").performClick(); action(ui.activePopover.content, "Push").performClick();
            assertEquals(4, writes.size()); assertTrue(completions.isEmpty());
            TextView push = (TextView) action(panel.remoteArea, "Push"); assertNotNull(push.getCompoundDrawablesRelative()[0]); assertFalse(push.getText().toString().contains("↑"));
            push.performClick(); assertEquals("push", writes.get(4).getString("action")); completions.remove(0).done(null, new Exception("Push rejected"));
            assertNotNull(find(panel.content, "Push rejected")); take(requests, "view=changes").result.done(listing, null);
            action(panel.content, "Dismiss").performClick(); assertNull(find(panel.content, "Push rejected"));
            panel.reload(); take(requests, "view=changes").result.done(listing, null); assertNull(find(panel.content, "Push rejected"));
            action(panel.remoteArea, "Push").performClick(); assertEquals(6, writes.size());
            completions.remove(0).done(null, new Exception("Push rejected")); take(requests, "view=changes").result.done(listing, null);
            assertNotNull(find(panel.content, "Push rejected")); assertNotNull(action(panel.content, "Dismiss"));
            screenshot(panel, "operations-" + (dark ? "dark" : "light"));
            action(panel.content, "Dismiss").performClick(); assertNull(find(panel.content, "Push rejected"));
            panel.activate(false);
        }
    }

    @Test public void fetchAndPushDisplayAndSendTheirSeparateTargets() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), false, "en"); NativeFileModel model = new NativeFileModel("p", "c");
            model.workspace = workspace("/repo/tree");
            JSONObject listing = changes().put("writable", true).put("remote_allowed", true).put("commit", "a".repeat(40))
                .put("remote", "origin").put("remote_branch", "main").put("upstream", "origin/main")
                .put("push_remote", "fork").put("push_remote_branch", "feature/git-panel").put("push_upstream", "").put("push_ahead", 0);
            List<JSONObject> writes = new ArrayList<>();
            NativeGitPanel panel = new NativeGitPanel(ui, model, (path, done) -> done.done(listing, null), value -> {});
            panel.write = (path, body, done) -> { writes.add(body); done.done(new JSONObject(), null); };
            activity.get().setContentView(panel); panel.activate(true); layout(panel);
            assertNotNull(find(panel.remoteArea, "fork/feature/git-panel"));
            action(panel.remoteArea, "Publish branch").performClick();
            assertEquals("fork", writes.get(0).getString("remote")); assertEquals("feature/git-panel", writes.get(0).getString("remote_branch"));
            layout(panel);
            action(panel.remoteArea, "Git remote actions").performClick(); action(ui.activePopover.content, "Fetch").performClick();
            assertNotNull(find(panel.remoteArea, "origin/main"));
            action(panel.remoteArea, "Fetch").performClick();
            assertEquals("origin", writes.get(1).getString("remote")); assertEquals("main", writes.get(1).getString("remote_branch"));
            listing.put("push_remote", ""); panel.reload();
            layout(panel);
            action(panel.remoteArea, "Git remote actions").performClick();
            assertFalse(action(ui.activePopover.content, "Publish branch").isEnabled()); ui.activePopover.dismiss();
            assertTrue(action(panel.remoteArea, "Fetch").isEnabled()); panel.activate(false);
        }
    }

    @Test public void discardRequiresConfirmationAndGroupSquaresUseOnlyTheirOwnFiles() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[]{false, true}) try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), dark, "en"); NativeFileModel model = new NativeFileModel("p", "c");
            JSONObject tree = workspace("/repo/tree"); model.workspace = tree; model.workspaces = new JSONArray().put(tree);
            JSONObject first = changes().put("writable", true).put("index", "confirmed-index");
            first.getJSONArray("files").getJSONObject(0).put("staged", true).put("unstaged", true).put("status", "MM");
            first.getJSONArray("files").put(new JSONObject().put("path", "notes.txt").put("untracked", true).put("unstaged", true).put("status", "??"));
            JSONObject[] listing = {first}; List<JSONObject> writes = new ArrayList<>();
            NativeGitPanel panel = new NativeGitPanel(ui, model, (path, done) -> done.done(listing[0], null), value -> {});
            panel.write = (path, body, done) -> { writes.add(body); done.done(new JSONObject(), null); };
            activity.get().setContentView(panel); panel.activate(true); layout(panel);
            assertNotNull(action(panel.content, "Unstage all")); assertNotNull(action(panel.content, "Stage all"));
            action(panel.content, "Discard src/file.txt").performClick();
            NativeDialog dialog = (NativeDialog) ShadowDialog.getLatestDialog(); assertTrue(dialog.isShowing());
            assertNotNull(find(dialog.body, "src/file.txt")); assertNull(find(dialog.body, "notes.txt"));
            screenshot(dialog.getWindow().getDecorView(), "discard-confirmation-" + (dark ? "dark" : "light"));
            assertTrue(writes.isEmpty()); find(dialog.actions, "Cancel").performClick(); assertTrue(writes.isEmpty());
            action(panel.content, "Discard src/file.txt").performClick(); dialog = (NativeDialog) ShadowDialog.getLatestDialog();
            // Polling may load a newer index while the file list is being confirmed.
            listing[0] = new JSONObject(first.toString()).put("index", "new-index"); panel.reload();
            find(dialog.actions, "Discard changes").performClick();
            assertEquals("discard", writes.get(0).getString("action")); assertEquals("confirmed-index", writes.get(0).getString("index"));
            paths(writes.get(0), "src/file.txt");
            action(panel.content, "Discard notes.txt").performClick(); dialog = (NativeDialog) ShadowDialog.getLatestDialog();
            assertNotNull(find(dialog.body, "notes.txt")); find(dialog.actions, "Discard changes").performClick();
            paths(writes.get(1), "notes.txt");
            // The first bulk discard is the Unstaged group, separate from Untracked.
            action(panel.content, "Discard all changes").performClick(); dialog = (NativeDialog) ShadowDialog.getLatestDialog();
            assertNotNull(find(dialog.body, "src/file.txt")); assertNull(find(dialog.body, "notes.txt"));
            find(dialog.actions, "Discard changes").performClick(); paths(writes.get(2), "src/file.txt");
            action(panel.content, "Unstage all").performClick(); assertEquals("unstage", writes.get(3).getString("action"));
            action(panel.content, "Stage all").performClick(); assertEquals("stage", writes.get(4).getString("action"));
            paths(writes.get(4), "src/file.txt");
            screenshot(panel, "discard-actions-" + (dark ? "dark" : "light"));
            action(panel.content, "Discard notes.txt").performClick(); dialog = (NativeDialog) ShadowDialog.getLatestDialog();
            panel.scopeChanged(); assertFalse(dialog.isShowing()); assertEquals(5, writes.size());
            panel.activate(false);
        }
    }
}
