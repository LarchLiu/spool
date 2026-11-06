package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import android.widget.ImageView;
import android.text.Spanned;
import android.text.style.ClickableSpan;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeFilesTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    @Test public void treeSearchExpansionIconsPreviewKindsAndSplitLimitsMatchActualWeb() throws Exception {
        JSONObject fixture = fixtures();
        NativeFileModel model = new NativeFileModel("mobile");
        JSONObject directories = fixture.getJSONObject("fileDirectories");
        for (java.util.Iterator<String> keys = directories.keys(); keys.hasNext();) { String path = keys.next(); model.accept(path, directories.getJSONObject(path)); }
        JSONArray cases = fixture.getJSONArray("fileTrees");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject test = cases.getJSONObject(i), operation = test.getJSONObject("operation");
            switch (operation.getString("type")) {
                case "expand": model.expanded.add(operation.getString("path")); break;
                case "collapse": model.expanded.remove(operation.getString("path")); break;
                case "search": model.beginSearch(); model.search(operation.getString("query")); break;
                case "close-search": model.closeSearch(); break;
                default: break;
            }
            List<NativeFileModel.Row> rows = model.rows(); JSONArray expected = test.getJSONArray("expected");
            assertEquals(operation.toString(), expected.length(), rows.size());
            for (int j = 0; j < rows.size(); j++) {
                assertEquals(expected.getJSONObject(j).getString("path"), rows.get(j).entry.path);
                assertEquals(expected.getJSONObject(j).getInt("depth"), rows.get(j).depth);
                assertEquals(expected.getJSONObject(j).getBoolean("expanded"), rows.get(j).expanded);
            }
        }
        JSONArray icons = fixture.getJSONArray("fileIcons");
        for (int i = 0; i < icons.length(); i++) assertEquals(icons.getJSONObject(i).getString("expected"), NativeTreeIcons.file(icons.getJSONObject(i).getString("path")));
        JSONArray previews = fixture.getJSONArray("filePreviews");
        for (int i = 0; i < previews.length(); i++) assertEquals(previews.getJSONObject(i).getString("expected"), NativeFileModel.previewKind(previews.getJSONObject(i).getJSONObject("file")));
        JSONArray links = fixture.getJSONArray("fileLinks");
        for (int i = 0; i < links.length(); i++) {
            JSONObject input = links.getJSONObject(i), expected = input.getJSONObject("expected");
            NativeFileTarget target = NativeFileTarget.parse(input.getString("href"), input.getString("base"));
            assertNotNull(target); assertEquals(expected.getString("path"), target.path); assertEquals(expected.optInt("line"), target.line); assertEquals(expected.optInt("column"), target.column);
        }
        JSONArray splits = fixture.getJSONArray("fileSplits");
        for (int i = 0; i < splits.length(); i++) {
            JSONObject input = splits.getJSONObject(i), limits = input.getJSONObject("limits"); double height = input.getDouble("height");
            assertEquals(limits.getDouble("min"), NativeFileSplit.minimum(height), .00001);
            assertEquals(limits.getDouble("max"), NativeFileSplit.maximum(height), .00001);
            assertEquals(input.getDouble("expected"), NativeFileSplit.fraction(input.getDouble("value"), height), .00001);
        }
        assertEquals("1.0 KB", NativeFileModel.size(1024)); assertEquals("1.5 MB", NativeFileModel.size(1572864));
        assertTrue(NativeFileData.allowedPath(NativeFileData.projectPath("project/id", "src/a & b.java", "download")));
        assertFalse(NativeFileData.allowedPath("/api/projects/mobile/file?path=x&download=1&redirect=evil"));
    }

    static final class Pending {
        final String path; final NativeFileBrowser.Completion result;
        Pending(String path, NativeFileBrowser.Completion result) { this.path = path; this.result = result; }
    }

    @Test public void closedReloadedAndSupersededRequestsCannotReplaceTheCurrentProjectFile() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        List<Pending> requests = new ArrayList<>();
        JSONObject fixture = fixtures();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeFileModel model = new NativeFileModel("mobile");
            NativeFileBrowser browser = new NativeFileBrowser(new NativeUi(controller.get(), false), "Mobile project", model, null,
                    (path, result) -> requests.add(new Pending(path, result)), file -> {}, address -> {}, () -> {}, value -> {});
            controller.get().setContentView(browser);
            browser.start(); browser.loadDirectory("."); assertEquals(1, requests.size());
            Pending root = requests.remove(0);
            root.result.done(fixture.getJSONObject("fileDirectories").getJSONObject("."), null);
            browser.select(new NativeFileTarget("README.md", 0, 0));
            browser.select(new NativeFileTarget("src/NativeUi.java", 3, 0));
            assertEquals(2, requests.size());
            Pending oldFile = requests.remove(0), currentFile = requests.remove(0);
            oldFile.result.done(fixture.getJSONObject("projectFiles").getJSONObject("README.md"), null);
            assertNull(model.file);
            currentFile.result.done(fixture.getJSONObject("projectFiles").getJSONObject("src/NativeUi.java"), null);
            assertEquals("src/NativeUi.java", model.file.getString("path"));
            assertEquals(1, requests.size());
            Pending directory = requests.remove(0);
            browser.reload();
            directory.result.done(fixture.getJSONObject("fileDirectories").getJSONObject("src"), null);
            assertFalse(model.directories.containsKey("src"));
            assertEquals(2, requests.size());
            browser.deactivate();
            for (Pending pending : requests) pending.result.done(new JSONObject("{entries:[]}"), null);
            assertTrue(model.directories.isEmpty());
            assertFalse(model.requested.contains("."));
        }
    }

    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }
    private JSONObject box(View view, View root) throws Exception {
        int x = 0, y = 0; for (View current = view; current != root; current = (View) current.getParent()) { x += current.getLeft(); y += current.getTop(); }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }
    private void snapshot(View view, String name) throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts")); assertTrue(directory.isDirectory() || directory.mkdirs());
        Bitmap bitmap = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888); view.draw(new Canvas(bitmap));
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
        bitmap.recycle();
    }

    private <T extends View> T child(View root, Class<T> type) {
        if (type.isInstance(root)) return type.cast(root);
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) {
            T result = child(((ViewGroup) root).getChildAt(i), type); if (result != null) return result;
        }
        return null;
    }

    private void markdownInsets(NativeUi ui, NativeFilePreview preview) {
        ViewGroup document = (ViewGroup) preview.scroll.getChildAt(0);
        assertTrue(document.getChildCount() > 0);
        for (int i = 0; i < document.getChildCount(); i++) {
            View block = document.getChildAt(i);
            assertEquals("Markdown blocks must be inset from the left edge", ui.dp(18), block.getLeft());
            assertEquals("Markdown blocks must be inset from the right edge", document.getWidth() - ui.dp(18), block.getRight());
        }
        assertEquals(ui.dp(8), document.getChildAt(0).getTop());
        View last = document.getChildAt(document.getChildCount() - 1);
        assertEquals("Bottom padding must be included in the scrollable content", last.getBottom() + ui.dp(28), document.getHeight());
    }

    private void clickLinks(View root) {
        if (root instanceof TextView && ((TextView) root).getText() instanceof Spanned) {
            Spanned text = (Spanned) ((TextView) root).getText();
            for (ClickableSpan link : text.getSpans(0, text.length(), ClickableSpan.class)) link.onClick(root);
        }
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) clickLinks(((ViewGroup) root).getChildAt(i));
    }

    @Test public void directoryErrorsRetryTruncationAndNoProjectStayInsideTheNativePanel() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        JSONObject fixture = fixtures();
        List<Pending> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeFileModel state = new NativeFileModel("mobile");
            NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", state, null,
                    (path, result) -> requests.add(new Pending(path, result)), file -> {}, address -> {}, () -> {}, value -> {});
            controller.get().setContentView(browser); browser.start();
            assertEquals(View.VISIBLE, browser.loading.getVisibility());
            requests.remove(0).result.done(null, new java.io.IOException("Directory unavailable"));
            assertEquals(View.GONE, browser.loading.getVisibility()); assertEquals(View.GONE, browser.empty.getVisibility());
            assertEquals(1, browser.notices.getChildCount());
            browser.notices.getChildAt(0).performClick(); assertEquals(1, requests.size());
            JSONObject listing = new JSONObject(fixture.getJSONObject("fileDirectories").getJSONObject(".").toString()); listing.put("truncated", true);
            requests.remove(0).result.done(listing, null); layout(browser, 331, 800);
            assertEquals("", state.error); assertTrue(state.truncated);
            assertEquals(ui.t("Large directories show up to 2,000 entries."), ((TextView) browser.notices.getChildAt(0)).getText().toString());
            browser.list.performItemClick(browser.list.getChildAt(0), 0, 0);
            assertEquals(".config", android.net.Uri.parse(requests.get(0).path).getQueryParameter("path"));
            requests.remove(0).result.done(fixture.getJSONObject("fileDirectories").getJSONObject(".config"), null);
            assertEquals(View.GONE, browser.empty.getVisibility());
            browser.deactivate();
            NativeFileBrowser unselected = new NativeFileBrowser(ui, "", new NativeFileModel(""), null,
                    (path, result) -> fail("No project may not issue a file request"), file -> {}, address -> {}, () -> {}, value -> {});
            controller.get().setContentView(unselected); unselected.start(); layout(unselected, 331, 800);
            assertEquals(View.GONE, unselected.searchToggle.getVisibility());
            assertEquals(View.VISIBLE, unselected.empty.getVisibility());
            assertEquals(ui.t("Choose a project to browse its files."), unselected.empty.getText().toString());
            assertNull(unselected.split.preview); unselected.deactivate();
        }
    }

    @Test public void previewKeepsLoadedContentsUntilSelectionFinishesAndRendersErrorsBelowItsHeader() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        JSONObject fixture = fixtures();
        List<Pending> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeFileModel model = new NativeFileModel("mobile");
            NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", model, null,
                    (path, result) -> requests.add(new Pending(path, result)), file -> {}, address -> {}, () -> {}, value -> {});
            controller.get().setContentView(browser);
            browser.select(new NativeFileTarget("README.md", 4, 0));
            requests.remove(0).result.done(fixture.getJSONObject("projectFiles").getJSONObject("README.md"), null);
            browser.select(new NativeFileTarget("missing.md", 20, 0));
            assertEquals("README.md:4", browser.preview.name.getText().toString());
            assertEquals("README.md:4", browser.preview.name.getContentDescription().toString());
            requests.remove(requests.size() - 1).result.done(null, new java.io.IOException("File unavailable"));
            layout(browser, 331, 800);
            assertEquals("missing.md", browser.preview.name.getText().toString());
            assertEquals(View.GONE, browser.preview.scroll.getVisibility()); assertEquals(View.GONE, browser.preview.size.getVisibility());
            assertEquals(View.GONE, browser.preview.download.getVisibility());
            assertEquals(browser.preview.header.getBottom(), browser.preview.notices.getTop());
            assertEquals("File unavailable", ((TextView) browser.preview.notices.getChildAt(0)).getText().toString());
            browser.select(new NativeFileTarget("src/NativeUi.java", 3, 0));
            Pending stale = requests.remove(requests.size() - 1);
            browser.preview.close.performClick();
            stale.result.done(fixture.getJSONObject("projectFiles").getJSONObject("src/NativeUi.java"), null);
            assertNull(model.file); assertNull(browser.split.preview); browser.deactivate();
        }
    }

    @Test public void inlineMarkdownLinksImagesBinaryAndTruncationUseSharedNativeComponents() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        JSONObject fixture = fixtures();
        List<NativeFileTarget> opened = new ArrayList<>(); List<String> external = new ArrayList<>(), reads = new ArrayList<>();
        List<Runnable> jobs = new ArrayList<>();
        Bitmap image = Bitmap.createBitmap(8, 4, Bitmap.Config.ARGB_8888);
        java.io.ByteArrayOutputStream encoded = new java.io.ByteArrayOutputStream(); assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, encoded)); image.recycle();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeImages images = new NativeImages(jobs::add, path -> { reads.add(path); return new NativeFileData("image/png", encoded.toByteArray()); });
            NativeFilePreview preview = new NativeFilePreview(ui, "project/id", images, opened::add, external::add, file -> {}, () -> {});
            controller.get().setContentView(preview);
            JSONObject markdown = new JSONObject(fixture.getJSONObject("projectFiles").getJSONObject("README.md").toString());
            markdown.put("path", "docs/guide.md").put("content", "[Source](../src/NativeUi.java#L3) and [Web](https://example.com) or person@example.org").put("truncated", true);
            preview.bind(markdown, new NativeFileTarget("docs/guide.md", 0, 0), "");
            assertNotNull(child(preview.scroll, NativeFileSource.class));
            preview.mode.getChildAt(1).performClick(); layout(preview, 331, 505);
            assertNull(child(preview.scroll, NativeFileSource.class)); assertEquals(1, preview.notices.getChildCount());
            markdownInsets(ui, preview);
            assertTrue(opened.isEmpty()); assertTrue(external.isEmpty());
            clickLinks(preview.scroll);
            assertEquals(1, opened.size()); assertEquals("docs/../src/NativeUi.java", opened.get(0).path); assertEquals(3, opened.get(0).line);
            assertEquals(List.of("https://example.com", "mailto:person@example.org"), external);
            preview.bind(fixture.getJSONObject("projectFiles").getJSONObject("archive.zip"), new NativeFileTarget("archive.zip", 0, 0), "");
            assertEquals(View.GONE, preview.mode.getVisibility());
            assertEquals(ui.t("This file has no text preview. Download it to open locally."), child(preview.scroll, TextView.class).getText().toString());
            preview.bind(fixture.getJSONObject("projectFiles").getJSONObject("src/assets/mobile.png"), new NativeFileTarget("src/assets/mobile.png", 0, 0), "");
            assertEquals(1, jobs.size()); jobs.remove(0).run(); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            layout(preview, 331, 505);
            ImageView picture = child(preview.scroll, NativeContainedImage.class);
            assertNotNull(picture.getDrawable()); assertEquals(8, picture.getWidth()); assertEquals(4, picture.getHeight());
            assertEquals(List.of(NativeFileData.projectPath("project/id", "src/assets/mobile.png", "image")), reads);
            images.clear();
        }
    }

    @Test public void sourceLineNavigationCentersTheSelectedRowWithoutReplacingCopyableContents() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), true);
            String contents = "line\twith tabs\n".repeat(2000);
            NativeFilePreview preview = new NativeFilePreview(ui, "mobile", null, target -> {}, address -> {}, file -> {}, () -> {});
            android.widget.FrameLayout host = new android.widget.FrameLayout(controller.get());
            host.addView(preview, new android.widget.FrameLayout.LayoutParams(331, 505));
            controller.get().setContentView(host);
            preview.bind(new JSONObject().put("path", "large.txt").put("name", "large.txt").put("kind", "text").put("content", contents).put("size", contents.length()), new NativeFileTarget("large.txt", 1000, 0), "");
            layout(host, 360, 800); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            NativeFileSource source = child(preview.scroll, NativeFileSource.class);
            assertEquals(contents, source.getText().toString()); assertTrue(source.isTextSelectable());
            assertEquals(2001, source.getLineCount());
            int centered = Math.max(0, source.targetTop() - (preview.scroll.getHeight() - ui.dp(20)) / 2);
            assertEquals(centered, preview.scroll.getScrollY());
            assertTrue(source.targetTop() > 19000);
            assertEquals(-1, new NativeFileSource(ui, "one line", 10).targetTop());
        }
    }

    @Test public void sourceGutterStaysPinnedAboveHorizontallyScrolledCode() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[] {false,true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark);
            NativeFilePreview preview = new NativeFilePreview(ui, "mobile", null, target -> {}, address -> {}, file -> {}, () -> {});
            preview.setBackgroundColor(ui.elevated);
            android.widget.FrameLayout host = new android.widget.FrameLayout(controller.get());
            host.addView(preview, new android.widget.FrameLayout.LayoutParams(331, 505)); controller.get().setContentView(host);
            String contents = "// Long native source\n" + "selectable code ".repeat(30) + "\n" + "selected line ".repeat(30);
            preview.bind(new JSONObject().put("path", "long.txt").put("name", "long.txt").put("kind", "text").put("content", contents).put("size", contents.length()), new NativeFileTarget("long.txt", 3, 0), "");
            layout(host, 360, 800); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            NativeFileSource source = child(preview.scroll, NativeFileSource.class);
            android.widget.HorizontalScrollView horizontal = (android.widget.HorizontalScrollView) source.getParent();
            assertTrue(source.getWidth() > horizontal.getWidth());
            horizontal.scrollTo(150, 0); assertEquals(150, horizontal.getScrollX());
            Bitmap bitmap = Bitmap.createBitmap(331, 505, Bitmap.Config.ARGB_8888); preview.draw(new Canvas(bitmap));
            int selectedY = preview.scroll.getTop() + source.targetTop() + 2;
            assertEquals(new NativeSourcePalette(dark).gutterSelection, bitmap.getPixel(4, selectedY));
            assertEquals(ui.elevated, bitmap.getPixel(4, selectedY - 20));
            assertEquals(contents, source.getText().toString()); bitmap.recycle();
            snapshot(preview, "files-source-scroll-" + (dark ? "dark" : "light"));
        }
    }

    @Test public void fileBrowserRendersAtAllMobileWidthsAndThemesWithInlinePreviewAndWorkingGrip() throws Exception {
        JSONObject fixture = fixtures();
        for (int width : new int[] {320,360,412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false,true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    NativeFileModel model = new NativeFileModel("mobile");
                    AtomicInteger downloads = new AtomicInteger();
                    NativeFileBrowser browser = new NativeFileBrowser(ui, "Mobile project", model, null, (path, completion) -> {
                        android.net.Uri uri = android.net.Uri.parse(path); String target = uri.getQueryParameter("path");
                        try { completion.done(fixture.getJSONObject(path.contains("/files?") ? "fileDirectories" : "projectFiles").getJSONObject(target), null); }
                        catch (Exception error) { completion.done(null, error); }
                    }, file -> downloads.incrementAndGet(), address -> {}, () -> {}, value -> {});
                    controller.get().setContentView(browser); browser.start(); browser.select(new NativeFileTarget("src/NativeUi.java", 3, 0));
                    int side = Math.min(360, Math.round(width * .92f)); layout(browser, side, 800);
                    org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); layout(browser, side, 800);
                    String suffix = width + (dark ? "-dark" : "-light"); snapshot(browser, "files-" + suffix);
                    JSONArray rows = new JSONArray(); for (int i = 0; i < browser.list.getChildCount(); i++) { JSONObject row = box(browser.list.getChildAt(i), browser); row.put("x", row.getInt("x") + 2).put("width", row.getInt("width") - 4); rows.put(row); }
                    JSONObject header = box(browser.header, browser); header.put("height", header.getInt("height") + ui.dp(1));
                    JSONObject geometry = new JSONObject().put("panel", box(browser, browser)).put("header", header).put("split", box(browser.split, browser))
                            .put("tree", box(browser.treePane, browser)).put("handle", box(browser.split.handle, browser)).put("preview", box(browser.preview, browser)).put("previewHeader", box(browser.preview.header, browser))
                            .put("previewScroll", box(browser.preview.scroll, browser)).put("rows", rows);
                    java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-files-" + suffix + ".json").toPath(), geometry.toString(2));
                    if (width == 360) {
                        NativeFileSource source = child(browser.preview.scroll, NativeFileSource.class);
                        JSONObject metrics = new JSONObject().put("size", source.getTextSize()).put("character", source.character)
                                .put("paddingLeft", source.getPaddingLeft()).put("paddingTop", source.getPaddingTop()).put("lineHeight", source.getLineHeight()).put("targetTop", source.targetTop());
                        java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-file-source-" + (dark ? "dark" : "light") + ".json").toPath(), metrics.toString(2));
                    }
                    browser.preview.download.performClick(); assertEquals(1, downloads.get());
                    android.view.accessibility.AccessibilityNodeInfo range = browser.split.handle.createAccessibilityNodeInfo();
                    assertEquals(android.view.accessibility.AccessibilityNodeInfo.RangeInfo.RANGE_TYPE_FLOAT, range.getRangeInfo().getType());
                    assertEquals(32, range.getRangeInfo().getCurrent(), .001);
                    android.os.Bundle progress = new android.os.Bundle();
                    progress.putFloat(android.view.accessibility.AccessibilityNodeInfo.ACTION_ARGUMENT_PROGRESS_VALUE, 60);
                    assertTrue(browser.split.handle.performAccessibilityAction(android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_SET_PROGRESS.getId(), progress));
                    assertEquals(.6, browser.split.current(), .0001); browser.split.update(.32);
                    double fraction = browser.split.current(); browser.split.handle.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_DOWN)); assertTrue(browser.split.current() > fraction);
                    browser.split.handle.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MOVE_HOME)); assertEquals(NativeFileSplit.minimum(750), browser.split.current(), .0001);
                    browser.split.update(.32);
                    model.expanded.clear();
                    browser.select(new NativeFileTarget("README.md", 0, 0)); layout(browser, side, 800);
                    browser.preview.mode.getChildAt(1).performClick(); layout(browser, side, 800); markdownInsets(ui, browser.preview); snapshot(browser, "files-markdown-" + suffix);
                    browser.toggleSearch(true); browser.search.setText("nativeui"); layout(browser, side, 800); snapshot(browser, "files-search-" + suffix);
                    browser.toggleSearch(false);
                    browser.preview.close.performClick(); assertNull(browser.split.preview);
                    browser.deactivate();
                }
            }
        }
    }
}
