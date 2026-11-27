package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.content.ClipData;
import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.pm.ProviderInfo;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;
import android.view.DragEvent;
import android.view.View;
import java.io.File;
import java.nio.file.Files;
import java.util.List;
import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.shadows.ShadowContentResolver;
import org.robolectric.shadows.ShadowToast;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeAttachmentDropTest {
    @Rule public TemporaryFolder temporary = new TemporaryFolder();
    private static final Uri SOURCE = Uri.parse("content://attachment-drop/notes.md");

    private void provider(MainActivityGoalTest.Harness h, byte[] bytes) throws Exception {
        File source = temporary.newFile();
        Files.write(source.toPath(), bytes);
        ContentProvider provider = new ContentProvider() {
            public boolean onCreate() { return true; }
            public String getType(Uri uri) { return "text/markdown"; }
            public Cursor query(Uri uri, String[] projection, String selection, String[] args, String sort) {
                MatrixCursor cursor = new MatrixCursor(new String[] {OpenableColumns.DISPLAY_NAME});
                cursor.addRow(new Object[] {"notes.md"}); return cursor;
            }
            public ParcelFileDescriptor openFile(Uri uri, String mode) throws java.io.FileNotFoundException {
                return ParcelFileDescriptor.open(source, ParcelFileDescriptor.MODE_READ_ONLY);
            }
            public Uri insert(Uri uri, ContentValues values) { return null; }
            public int delete(Uri uri, String selection, String[] args) { return 0; }
            public int update(Uri uri, ContentValues values, String selection, String[] args) { return 0; }
        };
        ProviderInfo info = new ProviderInfo(); info.authority = "attachment-drop";
        provider.attachInfo(h.activity, info);
        ShadowContentResolver.registerProviderInternal(info.authority, provider);
    }

    private DragEvent event(int action, ClipData data) {
        DragEvent event = ReflectionHelpers.callConstructor(DragEvent.class);
        ReflectionHelpers.setField(event, "mAction", action);
        ReflectionHelpers.setField(event, "mClipData", data);
        ReflectionHelpers.setField(event, "mClipDescription", data == null ? null : data.getDescription());
        return event;
    }

    private ClipData documents(int count) {
        ClipData data = new ClipData("Documents", new String[] {"text/uri-list", "text/markdown"}, new ClipData.Item(SOURCE));
        for (int i = 1; i < count; i++) data.addItem(new ClipData.Item(SOURCE));
        return data;
    }

    private void drop(MainActivityGoalTest.Harness h, ClipData data) {
        drop(h, data, true);
    }

    private void drop(MainActivityGoalTest.Harness h, ClipData data, boolean accepted) {
        NativeMarkwonHistoryTest.layout(h.get("root"));
        DragEvent started = event(DragEvent.ACTION_DRAG_STARTED, data);
        ReflectionHelpers.setField(started, "mClipData", null);
        h.composer().box.dispatchDragEvent(started);
        h.composer().box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENTERED, null));
        DragEvent dropped = event(DragEvent.ACTION_DROP, data);
        ReflectionHelpers.setField(dropped, "mX", h.composer().input.getLeft() + h.composer().input.getWidth() / 2f);
        ReflectionHelpers.setField(dropped, "mY", h.composer().input.getTop() + h.composer().input.getHeight() / 2f);
        assertEquals(accepted, h.composer().box.dispatchDragEvent(dropped));
        assertNull(h.composer().box.getForeground());
        View history = h.get("markwonHistory");
        if (history == null) history = h.get("history");
        assertEquals(1f, history.getAlpha(), 0.001f);
        h.composer().box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENDED, null));
    }

    @Test public void textDocumentsWithoutUriMimeStillBecomeAttachments() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            byte[] bytes = "name,value\n文件,42".getBytes(java.nio.charset.StandardCharsets.UTF_8);
            provider(h, bytes);
            h.composer().input.setText("Keep this draft");
            for (String type : new String[] {"text/plain", "text/csv", "text/markdown", "text/uri-list"}) {
                drop(h, new ClipData("Document", new String[] {type}, new ClipData.Item(SOURCE)));
                h.io.run();
            }
            assertEquals("Keep this draft", h.composer().input.getText().toString());
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            List<?> files = ReflectionHelpers.getField(draft, "files");
            assertEquals(4, files.size());
            for (Object file : files) assertArrayEquals(bytes, ReflectionHelpers.getField(file, "data"));
        }
    }

    @Test public void documentsUseExistingDraftCardsAndPreserveTheirBytesAndText() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            byte[] bytes = "# Notes\n保留原始内容".getBytes(java.nio.charset.StandardCharsets.UTF_8);
            provider(h, bytes);
            h.composer().input.setText("Keep this draft");
            drop(h, documents(2));
            h.io.run();
            assertEquals("Keep this draft", h.composer().input.getText().toString());
            assertEquals(2, h.composer().attachments.getChildCount());
            assertEquals(View.VISIBLE, h.composer().attachmentScroll.getVisibility());
            Object draft = ReflectionHelpers.callInstanceMethod(h.activity, "draft");
            List<?> files = ReflectionHelpers.getField(draft, "files");
            assertEquals("notes.md", ReflectionHelpers.getField(files.get(0), "name"));
            assertArrayEquals(bytes, ReflectionHelpers.getField(files.get(0), "data"));
            drop(h, documents(1)); h.io.run();
            assertEquals(3, h.composer().attachments.getChildCount());
        }
    }

    @Test public void hoverEndsCleanlyAndTextDragsKeepTheEditorBehavior() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            ClipData data = documents(1);
            NativeComposerBox box = h.composer().box;
            View history = h.get("markwonHistory");
            if (history == null) history = h.get("history");
            box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_STARTED, data));
            box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENTERED, data));
            assertNotNull(box.getForeground());
            assertEquals(1f, history.getAlpha(), 0.001f);
            box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_EXITED, data)); assertNull(box.getForeground());
            assertEquals(1f, history.getAlpha(), 0.001f);
            box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENTERED, data)); assertNotNull(box.getForeground());
            box.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENDED, null)); assertNull(box.getForeground());
            assertEquals(1f, history.getAlpha(), 0.001f);
            ClipData text = ClipData.newPlainText("Text", "normal text");
            assertFalse(box.fileDrop.handles(event(DragEvent.ACTION_DRAG_STARTED, text)));
            assertFalse(box.fileDrop.handles(event(DragEvent.ACTION_DROP, text)));
            assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void enteringThePageAdvertisesTheComposerBeforeReachingIt() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            NativeComposerBox box = h.composer().box;
            View page = (View) h.composer().getParent();
            View history = h.get("markwonHistory");
            if (history == null) history = h.get("history");
            ClipData data = documents(1);
            page.dispatchDragEvent(event(DragEvent.ACTION_DRAG_STARTED, data));
            page.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENTERED, null));
            page.dispatchDragEvent(event(DragEvent.ACTION_DRAG_LOCATION, null));
            assertNotNull(box.getForeground());
            assertEquals(1f, history.getAlpha(), 0.001f);
            page.dispatchDragEvent(event(DragEvent.ACTION_DRAG_EXITED, null));
            assertNull(box.getForeground());
            assertEquals(1f, history.getAlpha(), 0.001f);
            page.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENTERED, null));
            assertFalse(page.dispatchDragEvent(event(DragEvent.ACTION_DROP, data)));
            page.dispatchDragEvent(event(DragEvent.ACTION_DRAG_ENDED, null));
            assertNull(box.getForeground());
            assertEquals(1f, history.getAlpha(), 0.001f);
            assertTrue(h.io.jobs.isEmpty());
        }
    }

    @Test public void textAndUrlDropsKeepTheEditorInsertionPath() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            for (ClipData data : new ClipData[] {
                    ClipData.newPlainText("Text", "normal text"),
                    ClipData.newRawUri("Link", Uri.parse("https://example.com/notes.md"))}) {
                h.composer().input.setText("");
                drop(h, data);
                assertFalse(h.composer().input.getText().toString().isEmpty());
                assertTrue(h.io.jobs.isEmpty());
                assertEquals(0, h.composer().attachments.getChildCount());
            }
            assertEquals("https://example.com/notes.md", h.composer().input.getText().toString());
        }
    }

    @Test public void queueEditingRejectsDropsAndDiscardsFilesAlreadyBeingRead() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            provider(h, new byte[] {1, 2, 3});
            h.composer().input.setText("Keep this draft");
            drop(h, documents(1));
            h.set("delivering", true);
            h.set("deliveryGeneration", h.<Integer>get("generation"));
            h.set("deliveryAuth", h.<Integer>get("authEpoch"));
            h.set("editingQueue", true);
            h.call("updateComposer");
            assertFalse(h.composer().input.isEnabled());
            assertFalse(h.composer().add.isEnabled());
            drop(h, documents(1), false);
            // Only the original read remains; the disabled drop queued no work.
            h.io.run();
            assertEquals(0, h.composer().attachments.getChildCount());
            assertEquals("Keep this draft", h.composer().input.getText().toString());
            h.set("delivering", false);
            h.set("editingQueue", false);
            h.call("updateComposer");
            assertTrue(h.composer().add.isEnabled());
            drop(h, documents(1)); h.io.run();
            assertEquals(1, h.composer().attachments.getChildCount());
        }
    }

    @Test public void readonlyAndBusyDropsDoNotReadFiles() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.<org.json.JSONObject>get("chat").put("can_control", false);
            drop(h, documents(1), false); assertTrue(h.io.jobs.isEmpty());
            h.<org.json.JSONObject>get("chat").put("can_control", true);
            h.set("busy", true); drop(h, documents(1), false); assertTrue(h.io.jobs.isEmpty());
            h.set("busy", false);
            assertTrue(h.io.jobs.isEmpty());
            assertEquals(0, h.composer().attachments.getChildCount());
        }
    }

    @Test public void limitsAndChangedSelectionsNeverAttachPartialOrStaleBatches() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            drop(h, documents(7)); assertTrue(h.io.jobs.isEmpty());
            assertEquals("Attach at most 6 files, up to 16 MB total", ShadowToast.getTextOfLatestToast());
            provider(h, new byte[4 * 1024 * 1024 + 1]);
            drop(h, documents(1)); h.io.run();
            assertEquals(0, h.composer().attachments.getChildCount());
            provider(h, new byte[] {1, 2, 3});
            drop(h, documents(1)); h.set("draftVersion", h.<Integer>get("draftVersion") + 1); h.io.run();
            assertEquals(0, h.composer().attachments.getChildCount());
        }
    }
}
