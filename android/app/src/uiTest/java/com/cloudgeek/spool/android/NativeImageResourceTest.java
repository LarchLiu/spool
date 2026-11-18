package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.os.Looper;
import android.view.View;
import android.widget.ImageView;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeImageResourceTest {
    @Test public void savedSourcesDriveBothThumbnailAndFullImageWithoutOpeningRemovedPaths() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            String source = "/api/chats/chat/tool-image?ref=" + "a".repeat(64) + "&index=0";
            NativeImageResource resource = NativeImageResource.transcript("project", "chat", "/removed.png", source, "picture");
            Bitmap bitmap = Bitmap.createBitmap(4, 2, Bitmap.Config.ARGB_8888);
            ByteArrayOutputStream encoded = new ByteArrayOutputStream(); bitmap.compress(Bitmap.CompressFormat.PNG, 100, encoded); bitmap.recycle();
            NativeFileData data = new NativeFileData("image/png", encoded.toByteArray());
            List<String> reads = new ArrayList<>(); List<Runnable> jobs = new ArrayList<>();
            NativeImages.Source transport = path -> { reads.add(path); return data; };
            NativeImages images = new NativeImages(jobs::add, transport);
            ImageView thumbnail = new ImageView(controller.get()), preview = new ImageView(controller.get());
            resource.load(images, thumbnail, 120, error -> assertNull(error));
            resource.load(images, preview, 512, error -> assertNull(error));
            for (Runnable job : jobs) job.run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNotNull(thumbnail.getDrawable()); assertNotNull(preview.getDrawable());
            assertSame(data, resource.read(transport)); assertEquals(List.of(source, source, source), reads);
            assertEquals(source, resource.source);
            NativeImageResource fallback = NativeImageResource.transcript("project", "chat", "/removed.png", "/api/chats/other/attachments?id=id", "picture");
            assertEquals(NativeFileData.filePath("project", "chat", "/removed.png", "image"), fallback.source);
            assertNull(NativeImageResource.source("unsafe", "https://example.com/track.png"));
            String metadataURL = "/api/chats/chat/file?image=1&path=%2Fremoved.png&workspace=%2Fworktree";
            assertNotNull(NativeImageResource.source("archived", metadataURL));
            assertFalse(NativeFileData.allowedPath(metadataURL + "&path=neighbor.png"));
            assertFalse(NativeFileData.allowedPath(metadataURL + "&redirect=1"));
            JSONObject file = new JSONObject().put("path", "/removed.png").put("preview_url", metadataURL);
            assertEquals(metadataURL, NativeFileData.imageAction("project", "chat", file, "image", "/worktree"));
            file.put("preview_url", "/api/chats/other/file?path=private.png&image=1");
            assertEquals(NativeFileData.filePath("project", "chat", "/removed.png", "image", "/worktree"), NativeFileData.imageAction("project", "chat", file, "image", "/worktree"));
            images.clear();
        }
    }

    @Test public void previewReportsTheFailureCauseAndCanRetry() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeImagePreview.Content content = new NativeImagePreview.Content(ui, "image", () -> {}, () -> {});
            int[] retried = {0}; content.reload = () -> retried[0]++;
            for (int status : new int[]{404, 403, 400, 503}) {
                IOException failure = assertThrows(IOException.class, () -> NativeFileData.response(new JSONObject().put("status", status)));
                content.loaded(failure);
                assertEquals(NativeImageFailure.message(status), content.status.getText().toString());
                assertEquals(View.VISIBLE, content.retry.getVisibility());
                content.retry.performClick();
                assertEquals(View.GONE, content.retry.getVisibility());
                assertEquals("Loading file…", content.status.getText().toString());
            }
            assertEquals(4, retried[0]);
            content.loaded(new IOException("network"));
            assertEquals("Image could not be loaded. Try again.", content.status.getText().toString());
        }
    }
}
