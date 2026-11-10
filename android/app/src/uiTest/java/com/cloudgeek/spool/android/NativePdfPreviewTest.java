package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import java.io.File;
import java.io.IOException;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import static org.robolectric.Shadows.shadowOf;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativePdfPreviewTest {
    // Robolectric does not implement the system PDF backend on API 26/35.
    // Host tests cover async UI/lifecycle; device-pdf-smoke.mjs exercises PdfRenderer itself.
    private static class Document extends NativePdfDocument {
        boolean closed;
        final List<Integer> renders = new ArrayList<>();
        @Override int pageCount() { return 2; }
        @Override Size pageSize(int index) { return index % 2 == 0 ? new Size(320, 480) : new Size(480, 320); }
        @Override Page render(int index, int width) throws IOException {
            if (closed) throw new IOException("closed");
            renders.add(index);
            Bitmap bitmap = Bitmap.createBitmap(32, 48, Bitmap.Config.ARGB_8888);
            bitmap.eraseColor(index == 0 ? 0xffffeeee : 0xffeeeeff);
            Size size = pageSize(index);
            return new Page(bitmap, size.width, size.height);
        }
        @Override public void close() { closed = true; }
    }
    private NativeFileData fixture() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/pdf-preview.pdf")) {
            assertNotNull(input);
            return new NativeFileData("application/octet-stream", input.readAllBytes());
        }
    }

    private JSONObject metadata() throws Exception {
        return new JSONObject().put("path", "/work/报告 1.pdf").put("name", "报告 1.pdf").put("kind", "binary")
                .put("media_type", "application/pdf").put("size", 1024).put("external", true);
    }

    private void noScratch(File cache) {
        File[] files = cache.listFiles((directory, name) -> name.startsWith("spool-pdf-"));
        assertNotNull(files); assertEquals("Seekable PDF files must be unlinked immediately", 0, files.length);
    }

    @Test public void invalidSignaturesNeverCreateSeekableScratchFiles() throws Exception {
        File cache = RuntimeEnvironment.getApplication().getCacheDir();
        for (String source : new String[] {"not a PDF", "", "%PD"}) {
            try { NativePdfDocument.open(cache, new NativeFileData("application/pdf", source.getBytes(java.nio.charset.StandardCharsets.UTF_8))).close(); fail("Invalid PDF accepted"); }
            catch (IOException | RuntimeException expected) { noScratch(cache); }
        }
    }

    private void layout(View view) {
        view.measure(View.MeasureSpec.makeMeasureSpec(390, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(760, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, 390, 760);
    }

    private void drag(View view, float start, float end) {
        long time = SystemClock.uptimeMillis();
        MotionEvent down = MotionEvent.obtain(time, time, MotionEvent.ACTION_DOWN, 180, start, 0);
        MotionEvent move = MotionEvent.obtain(time, time + 40, MotionEvent.ACTION_MOVE, 180, end, 0);
        MotionEvent cancel = MotionEvent.obtain(time, time + 80, MotionEvent.ACTION_CANCEL, 180, end, 0);
        try { view.dispatchTouchEvent(down); view.dispatchTouchEvent(move); view.dispatchTouchEvent(cancel); }
        finally { down.recycle(); move.recycle(); cancel.recycle(); }
    }

    private void drain(ArrayDeque<Runnable> worker, View view) {
        for (int i = 0; i < 20; i++) {
            while (!worker.isEmpty()) worker.remove().run();
            shadowOf(android.os.Looper.getMainLooper()).idle(); layout(view);
            if (worker.isEmpty()) return;
        }
        fail("PDF rendering failed to settle");
    }

    @Test public void filePanelLoadsChatScopedPdfAndSupportsNavigationZoomAndReuse() throws Exception {
        RuntimeEnvironment.setQualifiers("w390dp-h760dp-mdpi");
        ArrayDeque<Runnable> worker = new ArrayDeque<>(); List<String> requests = new ArrayList<>(); NativeFileData bytes = fixture();
        Document document = new Document();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "zh-CN");
            NativeFilePreview panel = new NativeFilePreview(ui, "project", "chat", null,
                    (owner, path) -> new NativePdfPreview(owner, worker::add, target -> { requests.add(target); return bytes; }, path, (cache, data) -> document),
                    target -> {}, target -> {}, file -> {}, () -> {});
            controller.get().setContentView(panel);
            JSONObject file = metadata(); panel.bind(file, NativeFileTarget.parse(file.getString("path")), "", "workspace");
            layout(panel);
            int initialHeight = panel.pdf.page.getHeight();
            assertTrue(initialHeight > 0);
            drain(worker, panel);
            NativePdfPreview pdf = panel.pdf; assertNotNull(pdf);
            assertEquals(1, requests.size());
            assertEquals(NativeFileData.filePath("project", "chat", file.getString("path"), "download"), requests.get(0));
            assertEquals(View.GONE, panel.scroll.getVisibility()); assertEquals(View.GONE, pdf.status.getVisibility());
            assertEquals("Loading must not resize the PDF viewport", initialHeight, pdf.page.getHeight());
            View viewport = (View) pdf.page.getParent();
            assertEquals("The viewport must directly follow the toolbar", pdf.getChildAt(0).getBottom(), viewport.getTop());
            assertEquals("No status row may precede the document", 0, pdf.page.getTop());
            assertEquals(viewport.getHeight(), pdf.page.getHeight());
            assertEquals("1 / 2", pdf.position.getText().toString()); assertFalse(pdf.previous.isEnabled()); assertTrue(pdf.next.isEnabled());
            assertEquals(2, pdf.page.cachedPages());
            assertEquals(ui.t("Fit width"), pdf.fit.getContentDescription());
            panel.bind(file, NativeFileTarget.parse(file.getString("path")), "", "workspace"); drain(worker, panel);
            assertSame(pdf, panel.pdf); assertEquals(1, requests.size());
            pdf.next.performClick(); drain(worker, panel);
            assertEquals("2 / 2", pdf.position.getText().toString()); assertFalse(pdf.next.isEnabled());
            assertEquals("PDF 第 2 页，共 2 页", pdf.page.getContentDescription());
            pdf.zoomIn.performClick(); drain(worker, panel); assertEquals(1.25f, pdf.page.zoom(), .001);
            pdf.fit.performClick(); drain(worker, panel); assertEquals(1, pdf.page.zoom(), .001);
            pdf.previous.performClick(); drain(worker, panel); assertEquals("1 / 2", pdf.position.getText().toString());
            panel.bind(new JSONObject().put("name", "notes.txt").put("path", "notes.txt").put("kind", "text").put("content", "notes"), NativeFileTarget.parse("notes.txt"), "");
            drain(worker, panel); assertTrue(pdf.closed()); assertNull(panel.pdf); assertEquals(View.VISIBLE, panel.scroll.getVisibility());
            assertTrue(document.closed);
            noScratch(controller.get().getCacheDir());
        }
    }

    @Test public void verticalDraggingShowsBothPagesAndKeepsToolbarInSyncWithoutResettingZoom() throws Exception {
        RuntimeEnvironment.setQualifiers("w390dp-h760dp-mdpi");
        ArrayDeque<Runnable> worker = new ArrayDeque<>(); Document document = new Document(); NativeFileData bytes = fixture();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePdfPreview pdf = new NativePdfPreview(ui, worker::add, path -> bytes, NativeFileData.filePath("project", "chat", "a.pdf", "download"), (cache, data) -> document);
            controller.get().setContentView(pdf); layout(pdf); drain(worker, pdf);
            drag(pdf.page, 500, 200); drain(worker, pdf);
            assertTrue(pdf.page.scrollOffset() > 0); assertEquals("1 / 2", pdf.position.getText().toString());
            Bitmap screenshot = Bitmap.createBitmap(pdf.page.getWidth(), pdf.page.getHeight(), Bitmap.Config.ARGB_8888);
            pdf.page.draw(new Canvas(screenshot));
            // Both page colours are present in a single viewport after one drag.
            assertEquals(0xffffeeee, screenshot.getPixel(100, 30));
            assertEquals(0xffeeeeff, screenshot.getPixel(100, 400)); screenshot.recycle();
            drag(pdf.page, 500, 180); drain(worker, pdf);
            assertEquals("2 / 2", pdf.position.getText().toString()); assertTrue(pdf.previous.isEnabled()); assertFalse(pdf.next.isEnabled());
            pdf.zoomIn.performClick(); drain(worker, pdf); float zoom = pdf.page.zoom();
            pdf.previous.performClick(); drain(worker, pdf); assertEquals("1 / 2", pdf.position.getText().toString());
            assertEquals("Jumping to a page must preserve zoom", zoom, pdf.page.zoom(), .001);
            pdf.next.performClick(); drain(worker, pdf); assertEquals("2 / 2", pdf.position.getText().toString());
            pdf.close(); drain(worker, pdf);
        }
    }

    @Test public void longPdfLazilyRendersNearbyPagesAndDropsStaleCompletions() throws Exception {
        RuntimeEnvironment.setQualifiers("w390dp-h760dp-mdpi");
        ArrayDeque<Runnable> worker = new ArrayDeque<>(); NativeFileData bytes = fixture();
        Document document = new Document() { @Override int pageCount() { return 80; } };
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePdfPreview pdf = new NativePdfPreview(ui, worker::add, path -> bytes, NativeFileData.filePath("project", "chat", "a.pdf", "download"), (cache, data) -> document);
            controller.get().setContentView(pdf); layout(pdf); drain(worker, pdf);
            assertTrue(document.renders.size() <= NativePdfPages.PREFETCH_PAGE_BUDGET);
            pdf.page.goTo(30); assertEquals(1, worker.size());
            worker.remove().run(); // Page 31 completes on the worker but has not reached the UI.
            pdf.page.goTo(60); shadowOf(android.os.Looper.getMainLooper()).idle(); drain(worker, pdf);
            assertEquals("61 / 80", pdf.position.getText().toString());
            assertTrue(pdf.page.ready()); assertTrue(pdf.page.cachedPages() <= NativePdfPages.PREFETCH_PAGE_BUDGET);
            assertTrue("Jumping must not render intermediate pages", document.renders.size() < 10);
            pdf.page.zoomBy(4); drain(worker, pdf);
            assertEquals(4, pdf.page.zoom(), .001); assertTrue(pdf.page.cachedPages() <= NativePdfPages.PREFETCH_PAGE_BUDGET);
            pdf.close(); drain(worker, pdf); assertEquals(0, pdf.page.cachedPages()); assertTrue(document.closed);
        }
    }

    @Test public void shortLandscapePagesAllRenderWhenSeveralAreVisibleTogether() throws Exception {
        RuntimeEnvironment.setQualifiers("w390dp-h760dp-mdpi");
        ArrayDeque<Runnable> worker = new ArrayDeque<>(); NativeFileData bytes = fixture();
        Document document = new Document() {
            @Override int pageCount() { return 80; }
            @Override Size pageSize(int index) { return new Size(480, 40); }
        };
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePdfPreview pdf = new NativePdfPreview(ui, worker::add, path -> bytes, NativeFileData.filePath("project", "chat", "a.pdf", "download"), (cache, data) -> document);
            controller.get().setContentView(pdf); layout(pdf); drain(worker, pdf);
            assertTrue("Every visible page must render even when more than three fit onscreen", pdf.page.cachedPages() > 3);
            assertEquals(pdf.page.renderTargets().length, pdf.page.cachedPages());
            assertTrue("Offscreen pages must not be rendered", document.renders.size() < document.pageCount());
            pdf.close(); drain(worker, pdf);
        }
    }

    @Test public void neighbouringPageFailureDoesNotBreakScrollingOrTheCurrentPage() throws Exception {
        ArrayDeque<Runnable> worker = new ArrayDeque<>(); NativeFileData bytes = fixture();
        Document document = new Document() {
            @Override Page render(int index, int width) throws IOException {
                if (index == 1) throw new IOException("Damaged page");
                return super.render(index, width);
            }
        };
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePdfPreview pdf = new NativePdfPreview(ui, worker::add, path -> bytes, NativeFileData.filePath("project", "chat", "a.pdf", "download"), (cache, data) -> document);
            controller.get().setContentView(pdf); layout(pdf); drain(worker, pdf);
            int loadedHeight = pdf.page.getHeight();
            assertEquals(View.GONE, pdf.status.getVisibility());
            pdf.next.performClick(); drain(worker, pdf); assertEquals("2 / 2", pdf.position.getText().toString());
            assertEquals("An error message must not resize the document", loadedHeight, pdf.page.getHeight());
            assertEquals(View.VISIBLE, pdf.status.getVisibility());
            assertEquals(ui.t("This PDF cannot be previewed. Download it to open locally."), pdf.status.getText().toString());
            pdf.previous.performClick(); drain(worker, pdf); assertEquals(View.GONE, pdf.status.getVisibility());
            assertEquals(loadedHeight, pdf.page.getHeight());
            pdf.close(); drain(worker, pdf);
        }
    }

    @Test public void closingBeforeLoadOrBeforeItsCompletionCannotShowLatePdfPages() throws Exception {
        ArrayDeque<Runnable> worker = new ArrayDeque<>(); NativeFileData bytes = fixture(); List<String> requests = new ArrayList<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePdfPreview closed = new NativePdfPreview(ui, worker::add, path -> { requests.add(path); return bytes; }, NativeFileData.filePath("project", "chat", "a.pdf", "download"));
            closed.close(); drain(worker, closed); assertTrue(requests.isEmpty());
            Document document = new Document();
            NativePdfPreview late = new NativePdfPreview(ui, worker::add, path -> bytes, NativeFileData.filePath("project", "chat", "b.pdf", "download"), (cache, data) -> document);
            layout(late); worker.remove().run(); late.close(); drain(worker, late);
            assertEquals("", late.position.getText().toString()); assertFalse(late.next.isEnabled());
            assertTrue(document.closed);
            noScratch(controller.get().getCacheDir());
        }
    }

    @Test public void damagedPdfKeepsADownloadableFallbackAndPdfDetectionUsesMediaType() throws Exception {
        assertEquals("pdf", NativeFileModel.previewKind(metadata()));
        assertEquals("", NativeFileModel.previewKind(new JSONObject().put("name", "fake.pdf").put("kind", "binary").put("media_type", "application/octet-stream")));
        ArrayDeque<Runnable> worker = new ArrayDeque<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), true);
            NativePdfPreview pdf = new NativePdfPreview(ui, worker::add, path -> new NativeFileData("application/octet-stream", new byte[] {1, 2}), NativeFileData.filePath("project", "", "bad.pdf", "download"));
            layout(pdf); drain(worker, pdf);
            assertEquals(ui.t("This PDF cannot be previewed. Download it to open locally."), pdf.status.getText().toString());
            assertFalse(pdf.next.isEnabled()); pdf.close(); drain(worker, pdf);
            noScratch(controller.get().getCacheDir());
        }
    }
}
