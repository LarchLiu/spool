package com.cloudgeek.spool.android;

import android.app.Activity;
import android.content.ClipData;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import java.io.FilterInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;

/** One file-panel scope's SAF selection and uploads, retained across view rebuilds. */
final class NativeProjectUpload implements AutoCloseable {
    interface Transport extends AutoCloseable {
        void upload(String path, String name, InputStream input) throws Exception;
        @Override void close();
    }
    interface Factory { Transport open() throws Exception; }
    private static final long MAX_BYTES = 64L << 20;
    private static final AtomicInteger nextRequest = new AtomicInteger(32000);
    private final Activity activity;
    private volatile NativeUi ui;
    private NativeFileBrowser browser;
    private final NativeFileModel state;
    private final Executor io;
    private final Factory factory;
    private final BooleanSupplier current, allowed;
    private final Consumer<Exception> failed;
    private final Handler main = new Handler(Looper.getMainLooper());
    private Operation operation;
    private boolean closed, picking;
    private int requestCode;
    private String message = "";
    private boolean failedStatus;

    private static final class Operation {
        final int scope;
        final String directory, workspace, apiPath;
        volatile boolean cancelled;
        volatile Transport transport;
        int uploaded;
        Operation(NativeFileModel state, String directory) {
            scope = state.scope; this.directory = directory; apiPath = state.uploadPath(directory);
            workspace = state.workspace == null ? "" : state.workspace.optString("path");
        }
        void cancel() { cancelled = true; Transport handle = transport; if (handle != null) handle.close(); }
    }

    NativeProjectUpload(Activity activity, NativeUi ui, NativeFileBrowser browser, Executor io, Factory factory,
            BooleanSupplier current, BooleanSupplier allowed, Consumer<Exception> failed) {
        this.activity = activity; this.ui = ui; this.browser = browser; this.io = io; this.factory = factory;
        state = browser.state;
        this.current = current; this.allowed = allowed; this.failed = failed;
    }
    static boolean handles(int request) { return request >= 32000 && request < 48000; }
    boolean waiting() { return picking; }

    boolean rebind(NativeUi ui, NativeFileBrowser browser) {
        if (closed || browser.state != state || !current.getAsBoolean()) return false;
        this.ui = ui; this.browser = browser;
        if (operation != null && !valid(operation)) cancel();
        browser.uploadBusy = operation != null;
        browser.uploadStatus(message, failedStatus);
        return true;
    }

    private void status(String message, boolean failed) {
        this.message = message; failedStatus = failed;
        browser.uploadStatus(message, failed);
    }

    void pick(String directory) {
        if (closed || operation != null || !current.getAsBoolean() || !allowed.getAsBoolean() || !state.ready()) return;
        Operation selected = new Operation(state, NativeFileModel.key(directory));
        operation = selected; picking = true; browser.uploadBusy = true; status("", false);
        requestCode = nextRequest.getAndUpdate(value -> value == 47999 ? 32000 : value + 1);
        try {
            activity.startActivityForResult(new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
                    .setType("*/*").putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true), requestCode);
        } catch (RuntimeException error) { picking = false; finish(selected, error); }
    }

    void result(int request, int result, Intent data) {
        if (closed || request != requestCode || !picking) return;
        picking = false;
        Operation selected = operation;
        if (!valid(selected)) { cancel(); return; }
        if (result != Activity.RESULT_OK || data == null) { finish(selected, null); return; }
        if (!allowed.getAsBoolean()) { finish(selected, new IOException(ui.t("File upload permission is disabled"))); return; }
        List<Uri> documents = new ArrayList<>();
        ClipData clips = data.getClipData();
        if (clips != null) {
            for (int i = 0; i < clips.getItemCount(); i++) {
                Uri uri = clips.getItemAt(i).getUri(); if (!documents.contains(uri)) documents.add(uri);
            }
        } else if (data.getData() != null) documents.add(data.getData());
        next(selected, documents, 0);
    }

    private boolean valid(Operation selected) {
        return selected != null && selected == operation && !closed && !selected.cancelled && current.getAsBoolean()
                && state.ready() && selected.scope == state.scope
                && selected.workspace.equals(state.workspace == null ? "" : state.workspace.optString("path"));
    }

    private void next(Operation selected, List<Uri> documents, int index) {
        if (!valid(selected)) { cancel(); return; }
        if (index == documents.size()) { finish(selected, null); return; }
        if (!allowed.getAsBoolean()) { finish(selected, new IOException(ui.t("File upload permission is disabled"))); return; }
        Uri uri = documents.get(index);
        io.execute(() -> {
            try {
                if (selected.cancelled) return;
                if (uri == null || !"content".equals(uri.getScheme())) throw new IOException(ui.t("Invalid file upload"));
                String name = null;
                try (Cursor cursor = activity.getContentResolver().query(uri, new String[]{OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE}, null, null, null)) {
                    if (cursor != null && cursor.moveToFirst()) {
                        int nameColumn = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME), sizeColumn = cursor.getColumnIndex(OpenableColumns.SIZE);
                        if (nameColumn >= 0) name = cursor.getString(nameColumn);
                        if (sizeColumn >= 0 && !cursor.isNull(sizeColumn) && cursor.getLong(sizeColumn) > MAX_BYTES)
                            throw new IOException(ui.t("Each uploaded file must be at most 64 MB"));
                    }
                }
                if (!NativeDesktopFiles.validName(name)) throw new IOException(ui.t("Invalid upload filename"));
                String filename = name;
                main.post(() -> { if (valid(selected)) status(ui.t("Uploading {name}…", "name", filename), false); });
                if (selected.transport == null) {
                    selected.transport = factory.open();
                    if (selected.cancelled) { selected.transport.close(); return; }
                }
                try (InputStream document = activity.getContentResolver().openInputStream(uri)) {
                    if (document == null) throw new IOException(ui.t("Invalid file upload"));
                    // Unknown or inaccurate provider sizes still obey the server's limit.
                    InputStream bounded = new FilterInputStream(document) {
                        private long bytes;
                        @Override public int read(byte[] buffer, int offset, int length) throws IOException {
                            if (selected.cancelled) throw new IOException("Upload cancelled");
                            int count = super.read(buffer, offset, (int) Math.min(length, MAX_BYTES + 1 - bytes));
                            if (count > 0 && (bytes += count) > MAX_BYTES) throw new IOException(ui.t("Each uploaded file must be at most 64 MB"));
                            return count;
                        }
                        @Override public int read() throws IOException { byte[] one = new byte[1]; return read(one, 0, 1) < 0 ? -1 : one[0] & 255; }
                    };
                    if (selected.cancelled) return;
                    selected.transport.upload(selected.apiPath, filename, bounded);
                }
                main.post(() -> {
                    if (!valid(selected)) { if (operation == selected) cancel(); return; }
                    selected.uploaded++; browser.uploaded(selected.directory); next(selected, documents, index + 1);
                });
            } catch (Exception error) { main.post(() -> { if (valid(selected)) finish(selected, error); else if (operation == selected) cancel(); }); }
        });
    }

    private void finish(Operation selected, Exception error) {
        if (operation != selected) return;
        selected.cancel(); operation = null; picking = false; browser.uploadBusy = false;
        String message = selected.uploaded == 0 ? "" : ui.t("Uploaded {count} files to {path}", "count", selected.uploaded,
                "path", selected.directory.equals(".") ? "/" : selected.directory);
        if (error != null) message += (message.isEmpty() ? "" : "\n") + ui.t(error.getMessage() == null ? "Request failed" : error.getMessage());
        status(message, error != null);
        if (error != null) failed.accept(error);
    }
    void permissionsChanged() { if (!allowed.getAsBoolean() && operation != null) finish(operation, new IOException(ui.t("File upload permission is disabled"))); }
    void cancel() {
        if (operation != null) operation.cancel();
        operation = null; picking = false; browser.uploadBusy = false; status("", false);
    }
    @Override public void close() { closed = true; cancel(); main.removeCallbacksAndMessages(null); }
}
