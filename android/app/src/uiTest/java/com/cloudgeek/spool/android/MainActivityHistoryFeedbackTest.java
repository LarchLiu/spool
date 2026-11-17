package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityHistoryFeedbackTest {
    private JSONObject page() throws Exception {
        return new JSONObject("{id:'chat-one',project_id:'project-one',backend:'codex',can_control:true,status:'failed',"
                + "turns:[{id:'turn',backend:'codex',status:'failed',started_at:1000,finished_at:2000,part_start:0,part_end:1,error:'Agent failure'}],"
                + "transcript:{parts:[{role:'user',kind:'text',text:'Review the screenshot'}]}}");
    }

    private void show(MainActivityGoalTest.Harness h, JSONObject page, boolean markwon) throws Exception {
        h.set("chat", page); h.call("renderChat");
        NativeMarkwonHistoryTest.layout(h.get("root"));
        if (markwon) NativeMarkwonHistoryTest.await(h.get("markwonHistory"));
        Shadows.shadowOf(Looper.getMainLooper()).idle();
    }

    private <T> List<T> views(View root, Class<T> type) {
        List<T> result = new ArrayList<>();
        if (type.isInstance(root)) result.add(type.cast(root));
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++)
            result.addAll(views(((ViewGroup) root).getChildAt(i), type));
        return result;
    }

    @Test public void dismissedErrorsStayHiddenThroughSnapshotsAndNewFailuresReappearInBothRenderers() throws Exception {
        for (boolean markwon : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, markwon)) {
            JSONObject page = page(), turn = page.getJSONArray("turns").getJSONObject(0);
            show(h, page, markwon);
            NativeTurnError error = views(h.get("root"), NativeTurnError.class).get(0);
            assertEquals("Dismiss", error.close.getContentDescription()); assertEquals(View.VISIBLE, error.getVisibility());
            error.close.performClick(); assertEquals(View.GONE, error.getVisibility());
            show(h, new JSONObject(page.toString()), markwon);
            assertEquals(View.GONE, views(h.get("root"), NativeTurnError.class).get(0).getVisibility());
            turn.put("error", "Another failure"); show(h, page, markwon);
            error = views(h.get("root"), NativeTurnError.class).get(0);
            assertEquals("Another failure", error.text.getText().toString()); assertEquals(View.VISIBLE, error.getVisibility());
            error.close.performClick(); turn.put("finished_at", 3000); show(h, page, markwon);
            error = views(h.get("root"), NativeTurnError.class).get(0); assertEquals(View.VISIBLE, error.getVisibility());
            error.close.performClick(); turn.remove("error"); show(h, page, markwon);
            assertTrue(views(h.get("root"), NativeTurnError.class).isEmpty());
            turn.put("error", "Another failure"); show(h, page, markwon);
            error = views(h.get("root"), NativeTurnError.class).get(0); assertEquals(View.VISIBLE, error.getVisibility());
            error.close.performClick();
            page.getJSONArray("turns").put(new JSONObject(turn.toString()).put("id", "next-turn").put("part_start", 1).put("part_end", 1));
            show(h, page, markwon);
            assertEquals(1, views(h.get("root"), NativeTurnError.class).stream().filter(view -> view.getVisibility() == View.VISIBLE).count());
            turn.put("status", "stopped"); page.getJSONArray("turns").remove(1); show(h, page, markwon);
            assertTrue(views(h.get("root"), NativeTurnError.class).isEmpty());
        }
    }

    @Test public void nativeImagePathsLoadAuthenticatedThumbnailsAndPreviewsInBothRenderers() throws Exception {
        android.graphics.Bitmap bitmap = android.graphics.Bitmap.createBitmap(16, 8, android.graphics.Bitmap.Config.ARGB_8888);
        bitmap.eraseColor(0xff52704a);
        java.io.ByteArrayOutputStream encoded = new java.io.ByteArrayOutputStream();
        bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, encoded); bitmap.recycle();
        byte[] png = encoded.toByteArray();
        for (boolean markwon : new boolean[] {false, true}) for (String kind : new String[] {"context", "image"})
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, markwon)) {
                List<Runnable> jobs = new ArrayList<>(); List<String> requests = new ArrayList<>();
                h.set("fileImages", new NativeImages(jobs::add, path -> { requests.add(path); return new NativeFileData("image/png", png); }));
                JSONObject page = page(); page.getJSONArray("turns").getJSONObject(0).remove("error");
                page.getJSONObject("transcript").getJSONArray("parts").put(0, new JSONObject().put("role", "user").put("kind", kind)
                        .put("name", "codex-clipboard.PNG").put("file_path", "/fixture/剪贴板 screen.PNG").put("text", "[image]"));
                show(h, page, markwon);
                NativeAttachmentCard card = views(h.get("root"), NativeAttachmentCard.class).get(0);
                assertTrue(card.imageFile); assertNotNull(card.image); assertEquals(1, jobs.size());
                jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); assertNotNull(card.image.getDrawable());
                assertEquals(List.of("/api/chats/chat-one/file?path=" + android.net.Uri.encode("/fixture/剪贴板 screen.PNG") + "&image=1"), requests);
                card.open.performClick(); NativeImagePreview preview = h.get("imagePreview"); assertNotNull(preview);
                jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); assertNotNull(preview.content.image.getDrawable());
                assertEquals(requests.get(0), requests.get(1)); h.call("closeImagePreview");
                assertTrue("Viewing an image must not open a file browser", views(h.get("root"), NativeFileBrowser.class).isEmpty());
                h.set("chatID", "another-chat"); card.open.performClick(); assertNull(h.get("imagePreview"));
            }
    }
}
