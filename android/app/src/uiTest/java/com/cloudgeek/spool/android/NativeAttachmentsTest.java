package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.HorizontalScrollView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
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

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeAttachmentsTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    private byte[] picture() {
        Bitmap bitmap = Bitmap.createBitmap(640, 360, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(bitmap);
        canvas.drawColor(0xffe8ede1);
        Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
        paint.setColor(0xff304e3b);
        canvas.drawRect(0, 215, 640, 360, paint);
        paint.setColor(0xffb74e2d);
        canvas.drawCircle(465, 102, 54, paint);
        paint.setColor(0xff78936e);
        canvas.drawRoundRect(54, 105, 280, 295, 35, 35, paint);
        paint.setColor(0xfff8f9f5);
        canvas.drawRect(94, 145, 240, 155, paint);
        canvas.drawRect(94, 175, 205, 185, paint);
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, out));
        bitmap.recycle();
        return out.toByteArray();
    }

    @Test public void attachmentPathsAndDraftExcerptsMatchActualWebPreparation() throws Exception {
        JSONObject fixture = fixtures();
        JSONArray paths = fixture.getJSONArray("attachmentPaths");
        for (int i = 0; i < paths.length(); i++) {
            JSONObject test = paths.getJSONObject(i);
            String path = NativeFileData.attachmentPath(test.getString("chatID"), test.getString("fileID"));
            assertEquals(test.getString("expected"), path);
            assertTrue(NativeFileData.allowedPath(path));
        }
        JSONArray drafts = fixture.getJSONArray("attachmentDrafts");
        for (int i = 0; i < drafts.length(); i++) {
            JSONObject test = drafts.getJSONObject(i), expected = test.getJSONObject("expected");
            NativeFileData data = new NativeFileData(test.getString("mediaType"), android.util.Base64.decode(test.getString("data"), android.util.Base64.DEFAULT));
            assertEquals(expected.getString("mediaType"), data.mediaType);
            assertEquals(test.getString("name"), expected.getString("excerpt"), NativeFileData.excerpt(test.getString("name"), data.mediaType, data.bytes));
        }
        assertTrue(NativeFileData.code("NativeUi.TSX"));
        assertFalse(NativeFileData.code("notes.md"));
        assertTrue(NativeFileData.allowedPath("/api/chats/chat/tool-image?ref=" + "a".repeat(64) + "&index=0"));
        assertFalse(NativeFileData.allowedPath("/api/chats/chat/tool-image?ref=invalid&index=0"));
        assertFalse(NativeFileData.allowedPath("/api/chats/chat/tool-image?ref=" + "a".repeat(64) + "&index=0&redirect=1"));
        assertArrayEquals(new byte[] {1,2,3}, NativeFileData.inline("data:image/png;base64,AQID").bytes);
        assertEquals("image/png", NativeFileData.inline("data:image/png;base64,AQID").mediaType);
        assertFalse(NativeFileData.inlineImage("data:image/svg+xml;base64,AQID"));
        assertFalse(NativeFileData.inlineImage("https://example.com/image.png"));
        assertThrows(java.io.IOException.class, () -> NativeFileData.inline("data:image/png;base64,%%%"));
    }

    @Test public void imageLoadingCoalescesCachesScopesAndNeverRequestsAnArbitraryUrl() throws Exception {
        byte[] png = picture();
        List<Runnable> jobs = new ArrayList<>();
        AtomicInteger calls = new AtomicInteger(), completed = new AtomicInteger();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeImages images = new NativeImages(jobs::add, path -> { calls.incrementAndGet(); return new NativeFileData("image/png", png); });
            ImageView first = new ImageView(controller.get()), second = new ImageView(controller.get());
            String path = NativeFileData.attachmentPath("chat", "file");
            images.remote(first, path, 96, error -> { assertNull(error); completed.incrementAndGet(); });
            images.remote(second, path, 96, null);
            assertEquals(1, jobs.size());
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, calls.get());
            assertEquals(1, completed.get());
            assertNotNull(first.getDrawable());
            assertNotNull(second.getDrawable());
            images.remote(new ImageView(controller.get()), path, 96, null);
            assertEquals(0, jobs.size());
            for (String forbidden : new String[] {"https://example.com/a.png", "//example.com/a.png", "file:///image.png", "/api/avatars.svg", "/api/projects/x/file?path=a&redirect=https://example.com"}) {
                assertFalse(forbidden, NativeFileData.allowedPath(forbidden));
                assertThrows(IllegalArgumentException.class, () -> images.remote(first, forbidden, 96, null));
            }
            assertEquals(1, calls.get());
            String stale = NativeFileData.attachmentPath("chat", "stale");
            images.remote(first, stale, 96, error -> completed.incrementAndGet());
            images.clear();
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNull(first.getDrawable());
            assertEquals(1, completed.get());
            images.remote(first, path, 96, null);
            assertEquals(1, jobs.size());
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            images.clear();
        }
    }

    @Test public void rebindingAndFailedImageLoadsDoNotOverwriteAnotherCardOrPoisonRetries() throws Exception {
        byte[] png = picture();
        List<Runnable> jobs = new ArrayList<>();
        AtomicInteger calls = new AtomicInteger(), firstResult = new AtomicInteger();
        AtomicReference<Exception> failure = new AtomicReference<>();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeImages images = new NativeImages(jobs::add, path -> {
                if (calls.incrementAndGet() == 1) throw new java.io.IOException("Image unavailable");
                return new NativeFileData("image/png", png);
            });
            ImageView view = new ImageView(controller.get());
            String path = NativeFileData.attachmentPath("chat", "file");
            images.remote(view, path, 96, failure::set);
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNotNull(failure.get());
            assertNull(view.getDrawable());
            images.remote(view, path, 96, error -> firstResult.incrementAndGet());
            images.remote(view, NativeFileData.attachmentPath("other-chat", "other-file"), 96, null);
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(0, firstResult.get());
            assertNull(view.getDrawable());
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNotNull(view.getDrawable());
            images.clear();
        }
    }

    @Test public void imageJobsCaptureTheirTransportBeforeDispatchAndSkipQueuedRevokedSessions() throws Exception {
        byte[] png = picture();
        List<Runnable> jobs = new ArrayList<>();
        AtomicInteger oldReads = new AtomicInteger(), newReads = new AtomicInteger();
        AtomicReference<NativeImages.Source> current = new AtomicReference<>(path -> { oldReads.incrementAndGet(); return new NativeFileData("image/png", png); });
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeImages images = new NativeImages(jobs::add, current::get);
            ImageView image = new ImageView(controller.get());
            images.remote(image, NativeFileData.attachmentPath("chat", "first"), 96, null);
            current.set(path -> { newReads.incrementAndGet(); return new NativeFileData("image/png", png); });
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(1, oldReads.get());
            assertEquals(0, newReads.get());
            images.remote(image, NativeFileData.attachmentPath("chat", "revoked"), 96, null);
            images.clear();
            jobs.remove(0).run();
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertEquals(0, newReads.get());
            assertNull(image.getDrawable());
        }
    }

    @Test public void decodingPreservesAspectRatioAndBoundsMemoryBeforeAllocatingPixels() throws Exception {
        byte[] png = picture();
        Bitmap thumbnail = NativeImages.decode(new NativeFileData("image/png", png), 120);
        assertEquals(120, thumbnail.getWidth());
        assertEquals(68, thumbnail.getHeight());
        thumbnail.recycle();
        assertThrows(java.io.IOException.class, () -> NativeImages.decode(new NativeFileData("text/plain", png), 120));
        assertThrows(java.io.IOException.class, () -> NativeImages.decode(new NativeFileData("image/png", new byte[] {1,2,3}), 120));
        byte[] bomb = png.clone();
        java.nio.ByteBuffer.wrap(bomb, 16, 8).putInt(5001).putInt(5000);
        java.util.zip.CRC32 crc = new java.util.zip.CRC32();
        crc.update(bomb, 12, 17);
        java.nio.ByteBuffer.wrap(bomb, 29, 4).putInt((int) crc.getValue());
        assertThrows(java.io.IOException.class, () -> NativeImages.decode(new NativeFileData("image/png", bomb), 120));
        assertThrows(java.io.IOException.class, () -> new NativeFileData("image/png", new byte[NativeFileData.MAX_BYTES + 1]));
        assertThrows(java.io.IOException.class, () -> NativeFileData.response(new JSONObject("{status:404,body:'not found'}")));
        NativeFileData legacyText = NativeFileData.response(new JSONObject("{status:200,mediaType:'text/plain; charset=iso-8859-1',base64:true,body:'Y2Fm6Q0K'}"));
        assertArrayEquals(new byte[] {99,97,102,(byte)233,13,10}, legacyText.bytes);
    }

    @Test public void jpegExifOrientationMatchesBrowserImageOrientationWithoutFilesystemAccess() throws Exception {
        Bitmap original = Bitmap.createBitmap(40,20,Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(original);
        canvas.drawColor(android.graphics.Color.RED);
        Paint paint = new Paint(); paint.setColor(android.graphics.Color.BLUE);
        canvas.drawRect(20,0,40,20,paint);
        ByteArrayOutputStream encoded = new ByteArrayOutputStream();
        original.compress(Bitmap.CompressFormat.JPEG,100,encoded);
        original.recycle();
        byte[] jpeg = encoded.toByteArray();
        ByteArrayOutputStream oriented = new ByteArrayOutputStream();
        oriented.write(jpeg,0,2);
        oriented.write(new byte[] {(byte)255,(byte)225,0,34});
        java.nio.ByteBuffer exif = java.nio.ByteBuffer.allocate(32).order(java.nio.ByteOrder.LITTLE_ENDIAN);
        exif.put(new byte[] {69,120,105,102,0,0,73,73}).putShort((short)42).putInt(8).putShort((short)1)
                .putShort((short)274).putShort((short)3).putInt(1).putShort((short)6).putShort((short)0).putInt(0);
        oriented.write(exif.array());
        oriented.write(jpeg,2,jpeg.length-2);
        Bitmap bitmap = NativeImages.decode(new NativeFileData("image/jpeg",oriented.toByteArray()),100);
        assertEquals(20,bitmap.getWidth()); assertEquals(40,bitmap.getHeight());
        assertTrue(android.graphics.Color.red(bitmap.getPixel(10,5)) > 200);
        assertTrue(android.graphics.Color.blue(bitmap.getPixel(10,35)) > 200);
        bitmap.recycle();
    }

    private void draw(View view, int width, int maximum, String name) throws Exception {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(maximum, View.MeasureSpec.AT_MOST));
        view.layout(0, 0, width, view.getMeasuredHeight());
        Bitmap bitmap = Bitmap.createBitmap(width, view.getMeasuredHeight(), Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(bitmap));
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
        bitmap.recycle();
    }

    private JSONObject box(View view, View root) throws Exception {
        int x = 0, y = 0;
        for (View current = view; current != root; current = (View) current.getParent()) { x += current.getLeft(); y += current.getTop(); }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }

    private NativeAttachmentCards cards(NativeUi ui, JSONArray files, Bitmap picture, boolean wrap) throws Exception {
        NativeAttachmentCards cards = new NativeAttachmentCards(ui, wrap);
        if (wrap) cards.maximumFraction = .9f;
        for (int i = 0; i < files.length(); i++) {
            JSONObject file = files.getJSONObject(i);
            NativeAttachmentCard card = new NativeAttachmentCard(ui, file.getString("name"), file.getString("media_type"), file.optString("excerpt"), i == 0 ? picture : null, () -> {}, wrap ? null : () -> {});
            cards.addView(card);
        }
        return cards;
    }

    @Test public void attachmentAndImagePreviewLayoutsRenderAtEveryMobileWidthAndTheme() throws Exception {
        JSONArray files = fixtures().getJSONArray("attachmentFiles");
        byte[] png = picture();
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        java.nio.file.Files.write(new File(directory, "attachment-fixture.png").toPath(), png);
        for (int width : new int[] {320,360,412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false,true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    Bitmap picture = NativeImages.decode(new NativeFileData("image/png", png), 640);
                    String suffix = width + (dark ? "-dark" : "-light");
                    LinearLayout page = ui.padded(18,25);
                    page.setBackgroundColor(ui.elevated);
                    HorizontalScrollView scroll = new HorizontalScrollView(controller.get());
                    scroll.setHorizontalScrollBarEnabled(false);
                    NativeAttachmentCards drafts = cards(ui, files, picture, false);
                    scroll.addView(drafts);
                    page.addView(scroll, new LinearLayout.LayoutParams(-1,-2));
                    page.addView(ui.space(24));
                    LinearLayout history = ui.column();
                    history.setGravity(android.view.Gravity.END);
                    NativeAttachmentCards transcript = cards(ui, files, picture, true);
                    history.addView(transcript, new LinearLayout.LayoutParams(-2,-2));
                    page.addView(history, new LinearLayout.LayoutParams(-1,-2));
                    page.addView(ui.space(22));
                    draw(page, width, 1000, "attachments-" + suffix);
                    JSONArray draftBoxes = new JSONArray(), historyBoxes = new JSONArray();
                    for (int i = 0; i < drafts.getChildCount(); i++) draftBoxes.put(box(drafts.getChildAt(i),page));
                    for (int i = 0; i < transcript.getChildCount(); i++) {
                        View child = transcript.getChildAt(i);
                        assertTrue(child.getRight() <= transcript.getWidth());
                        assertTrue(child.getLeft() >= 0);
                        historyBoxes.put(box(child,page));
                    }
                    java.nio.file.Files.writeString(new File(directory,"native-attachments-" + suffix + ".json").toPath(),new JSONObject().put("draft",draftBoxes).put("history",historyBoxes).toString(2));
                    AtomicInteger closed = new AtomicInteger(), saved = new AtomicInteger();
                    NativeImagePreview.Content preview = new NativeImagePreview.Content(ui,"mobile-preview.png",saved::incrementAndGet,closed::incrementAndGet);
                    preview.image.setImageBitmap(picture);
                    preview.loaded(null);
                    draw(preview,Math.round(width*.94f),736,"image-preview-"+suffix);
                    java.nio.file.Files.writeString(new File(directory,"native-image-" + suffix + ".json").toPath(),new JSONObject().put("panel",box(preview,preview)).put("toolbar",box(preview.toolbar,preview)).put("image",box(preview.image,preview)).toString(2));
                    preview.download.performClick(); preview.close.performClick();
                    assertEquals(1,saved.get()); assertEquals(1,closed.get());
                    preview.loaded(new java.io.IOException("unavailable"));
                    assertEquals(View.VISIBLE,preview.status.getVisibility());
                    assertEquals(View.GONE,preview.image.getVisibility());
                    NativeAttachmentCard loading = new NativeAttachmentCard(ui,"missing.png","image/png","",null,()->{},null);
                    assertTrue(loading.imageFile);
                    assertNotNull(loading.image);
                    picture.recycle();
                }
            }
        }
    }
}
