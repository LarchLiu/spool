package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Intent;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.Looper;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;
import android.view.View;
import android.view.ViewGroup;
import java.io.File;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.file.Files;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;
import org.json.JSONObject;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowContentResolver;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeDesktopFilesTest {
    @Rule public TemporaryFolder temporary = new TemporaryFolder();
    private static final Uri SOURCE = Uri.parse("content://desktop-documents/source"), TARGET = Uri.parse("content://desktop-documents/target");
    private static JSONObject json(String value) {
        try { return new JSONObject(value); } catch (Exception error) { throw new AssertionError(error); }
    }
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private static final class Queue implements Executor {
        final ArrayDeque<Runnable> tasks = new ArrayDeque<>();
        public void execute(Runnable task) { tasks.add(task); }
        void drain() { while (!tasks.isEmpty()) { tasks.remove().run(); idle(); } }
    }
    private static final class Transport implements NativeDesktopFiles.Transport {
        final List<String> listed = new ArrayList<>();
        String uploadedPath, uploadedName, downloadedPath;
        byte[] uploaded;
        int cancellations;
        Runnable duringList = () -> {};
        boolean fail;
        public JSONObject list(String path) throws Exception {
            listed.add(path); duringList.run();
            if (fail) throw new java.io.IOException("fixture error");
            return json("{path:'" + path + "',entries:[{name:'报告 & notes.txt',dir:false,size:7},{name:'Photos',dir:true},{name:'../escape',dir:false}]}");
        }
        public void upload(String path, String name, InputStream input) throws Exception {
            uploadedPath = path; uploadedName = name; uploaded = input.readAllBytes();
        }
        public void download(String path, OutputStream output) throws Exception {
            downloadedPath = path; output.write(new byte[]{0, (byte) 255, 12, 34});
        }
        public void cancel() { cancellations++; }
        public void close() {}
    }
    private final class Provider extends ContentProvider {
        final File source, target;
        Provider() throws Exception { source = temporary.newFile("source.bin"); target = temporary.newFile("target.bin"); }
        public boolean onCreate() { return true; }
        public String getType(Uri uri) { return "application/octet-stream"; }
        public Cursor query(Uri uri, String[] projection, String selection, String[] args, String sort) {
            MatrixCursor cursor = new MatrixCursor(new String[]{OpenableColumns.DISPLAY_NAME}); cursor.addRow(new Object[]{"中文 & file.bin"}); return cursor;
        }
        public ParcelFileDescriptor openFile(Uri uri, String mode) throws java.io.FileNotFoundException {
            return ParcelFileDescriptor.open(uri.equals(SOURCE) ? source : target, ParcelFileDescriptor.parseMode(mode));
        }
        public Uri insert(Uri uri, ContentValues values) { return null; }
        public int delete(Uri uri, String selection, String[] args) { return 0; }
        public int update(Uri uri, ContentValues values, String selection, String[] args) { return 0; }
    }
    private static View labelled(View view, String text) {
        if (text.equals(view.getContentDescription())) return view;
        if (view instanceof ViewGroup group) for (int i = 0; i < group.getChildCount(); i++) {
            View found = labelled(group.getChildAt(i), text); if (found != null) return found;
        }
        return null;
    }

    @Test public void browseParentRefreshAndCapabilitiesUseTheSharedRoot() {
        try (var activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), false, "zh-CN"); Queue io = new Queue(); Transport transport = new Transport();
            NativeDesktopDocuments documents = new NativeDesktopDocuments(activity.get(), waiting -> {});
            NativeDesktopFiles files = new NativeDesktopFiles(ui, ui.context, io, () -> transport, documents, () -> {});
            try {
                assertFalse(files.available()); files.open(); assertTrue(io.tasks.isEmpty());
                files.configure(json("{upload:true,download:true,dir:'Downloads'}"), false);
                files.open(); io.drain();
                assertEquals(List.of("Downloads"), transport.listed); assertEquals(2, files.list.getAdapter().getCount());
                assertTrue(files.list.getAdapter().getView(0, null, files.list).performClick()); io.drain();
                assertEquals("Downloads/Photos", transport.listed.get(1));
                files.up.performClick(); io.drain(); assertEquals("Downloads", transport.listed.get(2));
                files.refresh.performClick(); io.drain(); assertEquals("Downloads", transport.listed.get(3));
                files.configure(json("{upload:true,download:true}"), true);
                assertEquals(View.GONE, files.upload.getVisibility()); assertTrue(files.available());
                files.configure(json("{upload:true,download:false}"), false);
                View row = files.list.getAdapter().getView(1, null, files.list);
                assertNull(labelled(row, "Download file: 报告 & notes.txt"));
                files.configure(json("{upload:false,download:false}"), false);
                assertFalse(files.available()); assertEquals(View.GONE, files.getVisibility());
            } finally { files.close(); documents.close(); }
        }
    }

    @Test public void systemDocumentsStreamUploadsAndFolderZipDownloadsWithoutClosingTheView() throws Exception {
        try (var activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), false); Queue io = new Queue(); Transport transport = new Transport();
            List<Boolean> picker = new ArrayList<>(); Provider provider = new Provider();
            android.content.pm.ProviderInfo info = new android.content.pm.ProviderInfo(); info.authority = "desktop-documents";
            provider.attachInfo(activity.get(), info);
            ShadowContentResolver.registerProviderInternal("desktop-documents", provider);
            Files.write(provider.source.toPath(), new byte[]{0, 1, (byte) 255, 23});
            NativeDesktopDocuments documents = new NativeDesktopDocuments(activity.get(), picker::add);
            NativeDesktopFiles files = new NativeDesktopFiles(ui, ui.context, io, () -> transport, documents, () -> {});
            try {
                files.configure(json("{upload:true,download:true,dir:'Downloads'}"), false); files.open(); io.drain();
                files.upload.performClick(); assertTrue(documents.waiting()); assertFalse(files.refresh.isEnabled());
                Intent upload = Shadows.shadowOf(activity.get()).getNextStartedActivityForResult().intent;
                assertEquals(Intent.ACTION_OPEN_DOCUMENT, upload.getAction());
                documents.result(documents.requestCode, Activity.RESULT_OK, new Intent().setData(SOURCE)); io.drain();
                assertArrayEquals(Files.readAllBytes(provider.source.toPath()), transport.uploaded);
                assertEquals("中文 & file.bin", transport.uploadedName); assertEquals("Downloads", transport.uploadedPath);
                assertEquals(List.of(true, false), picker); assertTrue(files.refresh.isEnabled());
                View row = files.list.getAdapter().getView(0, null, files.list);
                assertTrue(labelled(row, "Download folder as ZIP: Photos").performClick());
                Intent download = Shadows.shadowOf(activity.get()).getNextStartedActivityForResult().intent;
                assertEquals(Intent.ACTION_CREATE_DOCUMENT, download.getAction()); assertEquals("Photos.zip", download.getStringExtra(Intent.EXTRA_TITLE));
                assertEquals("application/zip", download.getType());
                documents.result(documents.requestCode, Activity.RESULT_OK, new Intent().setData(TARGET)); io.drain();
                assertEquals("Downloads/Photos", transport.downloadedPath);
                assertArrayEquals(new byte[]{0, (byte) 255, 12, 34}, Files.readAllBytes(provider.target.toPath()));
                assertEquals(List.of(true, false, true, false), picker);
                files.upload.performClick(); documents.result(documents.requestCode, Activity.RESULT_CANCELED, null); io.drain();
                assertTrue(files.refresh.isEnabled()); assertFalse(documents.waiting());
            } finally { files.close(); documents.close(); }
        }
    }

    @Test public void cancellationCloseAndErrorsDiscardLateResultsAndPermitRetry() {
        try (var activity = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(activity.get(), false); Queue io = new Queue(); Transport transport = new Transport();
            NativeDesktopDocuments documents = new NativeDesktopDocuments(activity.get(), waiting -> {});
            NativeDesktopFiles files = new NativeDesktopFiles(ui, ui.context, io, () -> transport, documents, () -> {});
            files.configure(json("{upload:true,download:true}"), false);
            try {
                transport.duringList = files::cancel;
                files.open(); io.drain(); assertEquals(0, files.list.getAdapter().getCount()); assertEquals(1, transport.cancellations);
                transport.duringList = () -> {}; transport.fail = true;
                files.refresh.performClick(); io.drain(); assertTrue(files.refresh.isEnabled());
                transport.fail = false; files.refresh.performClick(); io.drain(); assertEquals(2, files.list.getAdapter().getCount());
                files.upload.performClick(); assertTrue(documents.waiting());
                documents.close(); files.close(); documents.result(documents.requestCode, Activity.RESULT_OK, new Intent().setData(SOURCE)); io.drain();
                assertNull(transport.uploaded); assertFalse(files.available());
            } finally { files.close(); documents.close(); }
        }
    }

    @Test public void traversalAndUnsafeDocumentNamesAreRejected() {
        for (String path : new String[]{"../outside", "/absolute", "a/../b", "a//b", "a\\b", "a/", "a\nb"}) assertFalse(path, NativeDesktopFiles.validPath(path));
        assertTrue(NativeDesktopFiles.validPath("")); assertTrue(NativeDesktopFiles.validPath("中文 & ?/Photos"));
        for (String name : new String[]{"", ".", "..", "a/b", "a\\b", "a\u0000b", "a\nb", "中".repeat(86)}) assertFalse(name, NativeDesktopFiles.validName(name));
    }
}
