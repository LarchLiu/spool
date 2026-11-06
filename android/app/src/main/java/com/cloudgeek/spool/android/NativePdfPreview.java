package com.cloudgeek.spool.android;

import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.concurrent.Executor;

/** Authenticated PDF loading and off-main-thread page rendering; stale completions never bind. */
final class NativePdfPreview extends LinearLayout implements AutoCloseable {
    interface Factory { NativePdfPreview create(NativeUi ui, String relativeAPIPath); }
    interface Open { NativePdfDocument open(java.io.File cache, NativeFileData file) throws Exception; }
    final NativeUi.IconButton previous, next, zoomIn, zoomOut, fit;
    final TextView position, status;
    final NativePdfPages page;
    private final NativeUi ui;
    private final Executor worker;
    private final NativeImages.Source source;
    private final String path;
    private final Open opener;
    private final Handler main = new Handler(Looper.getMainLooper());
    private NativePdfDocument document;
    private volatile boolean closed;
    private volatile int request;
    private int count;
    private boolean rendering;
    private final android.util.SparseBooleanArray failedPages = new android.util.SparseBooleanArray();

    NativePdfPreview(NativeUi ui, Executor worker, NativeImages.Source source, String path) {
        this(ui, worker, source, path, NativePdfDocument::open);
    }

    NativePdfPreview(NativeUi ui, Executor worker, NativeImages.Source source, String path, Open opener) {
        super(ui.context);
        if (!NativeFileData.allowedPath(path)) throw new IllegalArgumentException("Invalid PDF API path");
        this.ui = ui; this.worker = worker; this.source = source; this.path = path;
        this.opener = opener;
        setOrientation(VERTICAL);
        setContentDescription(ui.t("PDF preview"));
        page = new NativePdfPages(ui, () -> { controls(); render(); });
        LinearLayout toolbar = ui.row(); toolbar.setPadding(ui.dp(8), ui.dp(3), ui.dp(8), ui.dp(3));
        previous = ui.icon("chevron-left", 15, ui.t("Previous page"), () -> page.goTo(page.currentPage() - 1));
        next = ui.icon("chevron-right", 15, ui.t("Next page"), () -> page.goTo(page.currentPage() + 1));
        position = ui.text("", 12, ui.text); position.setGravity(android.view.Gravity.CENTER);
        toolbar.addView(previous); toolbar.addView(position, new LayoutParams(0, -2, 1)); toolbar.addView(next);
        zoomOut = ui.icon("minus", 15, ui.t("Zoom out"), () -> page.zoomBy(1 / 1.25f));
        fit = ui.icon("fit", 15, ui.t("Fit width"), () -> page.fit());
        zoomIn = ui.icon("plus", 15, ui.t("Zoom in"), () -> page.zoomBy(1.25f));
        toolbar.addView(zoomOut); toolbar.addView(fit); toolbar.addView(zoomIn); addView(toolbar);
        FrameLayout viewport = new FrameLayout(ui.context);
        viewport.addView(page, new FrameLayout.LayoutParams(-1, -1));
        status = NativeFilePreview.empty(ui, ui.t("Loading file…"));
        status.setGravity(Gravity.CENTER); status.setBackgroundColor(ui.soft);
        status.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        viewport.addView(status, new FrameLayout.LayoutParams(-1, -2, Gravity.CENTER));
        addView(viewport, new LayoutParams(-1, 0, 1)); controls();
        load();
    }

    boolean closed() { return closed; }

    private void load() {
        int pending = ++request;
        worker.execute(() -> {
            NativePdfDocument loaded = null;
            try {
                if (closed) return;
                loaded = opener.open(ui.context.getCacheDir(), source.load(path));
                int pages = loaded.pageCount();
                if (pages <= 0) throw new java.io.IOException("PDF has no pages");
                NativePdfDocument.Size[] sizes = new NativePdfDocument.Size[pages];
                for (int i = 0; i < pages; i++) {
                    if (closed) { loaded.close(); return; }
                    sizes[i] = loaded.pageSize(i);
                    if (sizes[i].width <= 0 || sizes[i].height <= 0) throw new java.io.IOException("Invalid PDF page size");
                }
                if (closed) { loaded.close(); return; }
                NativePdfDocument value = loaded;
                main.post(() -> {
                    if (closed || pending != request) { release(value); return; }
                    document = value; count = pages; page.document(sizes);
                });
            } catch (Exception error) {
                if (loaded != null) loaded.close();
                main.post(() -> { if (!closed && pending == request) failure(); });
            }
        });
    }

    private void controls() {
        int index = page.currentPage();
        previous.setEnabled(document != null && index > 0); next.setEnabled(document != null && index + 1 < count);
        zoomOut.setEnabled(document != null && page != null && page.zoom() > 1);
        zoomIn.setEnabled(document != null && page != null && page.zoom() < 4);
        fit.setEnabled(document != null);
        position.setText(count == 0 ? "" : (index + 1) + " / " + count);
        if (count > 0) page.setContentDescription(ui.t("PDF page {page} of {count}", "page", index + 1, "count", count));
    }

    private void render() {
        if (closed || document == null || page.getWidth() == 0 || page.getHeight() == 0) return;
        int[] targets = page.renderTargets(); page.retain(targets);
        updateStatus();
        // Keep only one native render in flight; scrolling coalesces into the newest visible range.
        if (rendering) return;
        int width = page.requestedWidth(), missing = -1;
        for (int target : targets) if (!failedPages.get(target) && page.needs(target, width)) { missing = target; break; }
        if (missing < 0) return;
        rendering = true;
        int pending = request, requestedPage = missing;
        NativePdfDocument captured = document;
        worker.execute(() -> {
            if (closed || pending != request) return;
            try {
                NativePdfDocument.Page bitmap = captured.render(requestedPage, width);
                main.post(() -> {
                    if (closed || pending != request) { bitmap.bitmap.recycle(); return; }
                    rendering = false;
                    boolean wanted = false;
                    for (int target : page.renderTargets()) if (target == requestedPage) { wanted = true; break; }
                    if (wanted) page.page(requestedPage, bitmap, width); else bitmap.bitmap.recycle();
                    render();
                });
            } catch (Exception error) {
                main.post(() -> {
                    if (closed || pending != request) return;
                    rendering = false; failedPages.put(requestedPage, true); render();
                });
            }
        });
    }

    private void updateStatus() {
        if (failedPages.get(page.currentPage())) { failure(); return; }
        status.setText(ui.t("Loading file…")); status.setTextColor(ui.muted);
        // Overlay messages so loading and errors never change the document viewport or leave a gap.
        status.setVisibility(page.ready() ? View.GONE : View.VISIBLE);
    }

    private void failure() { status.setText(ui.t("This PDF cannot be previewed. Download it to open locally.")); status.setTextColor(ui.error); status.setVisibility(View.VISIBLE); }

    @Override public void close() {
        if (closed) return;
        closed = true; request++;
        page.clear();
        NativePdfDocument captured = document; document = null;
        count = 0;
        if (captured != null) release(captured);
        controls();
    }
    private void release(NativePdfDocument value) {
        try { worker.execute(value::close); }
        catch (java.util.concurrent.RejectedExecutionException stopped) { value.close(); }
    }
    @Override protected void onDetachedFromWindow() { close(); super.onDetachedFromWindow(); }
}
