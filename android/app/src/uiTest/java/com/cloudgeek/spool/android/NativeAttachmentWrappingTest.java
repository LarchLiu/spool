package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.view.ViewGroup;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeAttachmentWrappingTest {
    private <T> List<T> views(View root, Class<T> type) {
        List<T> result = new ArrayList<>();
        if (type.isInstance(root)) result.add(type.cast(root));
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++)
            result.addAll(views(((ViewGroup) root).getChildAt(i), type));
        return result;
    }

    private JSONObject page() throws Exception {
        JSONArray files = new JSONArray()
                .put(new JSONObject().put("id", "openapi").put("name", "wechat-pay.openapi.json").put("media_type", "application/json"))
                .put(new JSONObject().put("id", "guide").put("name", "wechat-pay-backend-guide.md").put("media_type", "text/plain"));
        JSONArray parts = new JSONArray().put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Review the payment integration"))
                .put(new JSONObject().put("role", "assistant").put("kind", "text").put("phase", "final_answer").put("text", "Both files are attached."));
        JSONObject turn = new JSONObject().put("id", "turn").put("user_id", "alice").put("backend", "codex").put("status", "completed")
                .put("part_start", 0).put("input_end", 1).put("part_end", 2).put("attachments", files);
        return new JSONObject().put("id", "chat-one").put("project_id", "project-one").put("backend", "codex").put("status", "completed")
                .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", parts));
    }

    private void layout(View root, int width) {
        root.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, width, 800); root.getViewTreeObserver().dispatchOnPreDraw();
    }

    private NativeUserRow show(MainActivityGoalTest.Harness h, JSONObject page, boolean recycler, int width) throws Exception {
        h.set("chat", page); h.call("renderChat");
        View root = h.get("root"); layout(root, width);
        if (recycler) NativeMarkwonHistoryTest.await(h.get("markwonHistory"));
        layout(root, width);
        return views(root, NativeUserRow.class).get(0);
    }

    private void assertCardsFit(NativeUserRow row, int count, String scene) {
        NativeAttachmentCards cards = row.attachments;
        assertEquals(count, cards.getChildCount());
        for (int i = 0; i < cards.getChildCount(); i++) {
            View card = cards.getChildAt(i);
            String detail = scene + ", card=" + i + ", cards=" + cards.getWidth() + "x" + cards.getHeight();
            assertTrue(detail, card.getWidth() > 0 && card.getHeight() > 0);
            assertTrue(detail + " left", card.getLeft() >= 0);
            assertTrue(detail + " right", card.getRight() <= cards.getWidth());
            assertTrue(detail + " bottom " + card.getBottom(), card.getBottom() <= cards.getHeight());
        }
        assertTrue(scene, cards.getBottom() <= row.content.getHeight());
    }

    @Test public void jsonAndMarkdownAttachmentsStayInsideTheirRowAcrossPhoneWidths() throws Exception {
        for (boolean recycler : new boolean[] {false, true}) for (int width : new int[] {320, 360, 412}) {
            try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, recycler)) {
                RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
                NativeUserRow row = show(h, page(), recycler, width);
                assertCardsFit(row, 2, "recycler=" + recycler + ", width=" + width);
                Path directory = Path.of(System.getProperty("spool.ui.artifacts"), "attachment-wrapping");
                Files.createDirectories(directory);
                Bitmap image = Bitmap.createBitmap(row.getWidth(), row.getHeight(), Bitmap.Config.ARGB_8888);
                row.draw(new Canvas(image));
                try (java.io.OutputStream output = Files.newOutputStream(directory.resolve("api" + android.os.Build.VERSION.SDK_INT + "-" + width + "-" + recycler + ".png"))) {
                    assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                } finally { image.recycle(); }
            }
        }
    }

    @Test public void snapshotUpdatesAndRecreatedCardsKeepTheirFullHeight() throws Exception {
        for (boolean recycler : new boolean[] {false, true}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, recycler)) {
            JSONObject page = page(), turn = page.getJSONArray("turns").getJSONObject(0);
            JSONArray files = turn.getJSONArray("attachments");
            turn.put("attachments", new JSONArray().put(files.get(0)));
            NativeUserRow row = show(h, page, recycler, 360);
            assertCardsFit(row, 1, "single attachment");
            turn.put("attachments", files);
            assertSame(row, show(h, page, recycler, 360));
            assertCardsFit(row, 2, "second attachment added");
            turn.put("attachments", new JSONArray()); show(h, page, recycler, 360);
            assertNull(row.attachments);
            turn.put("attachments", files);
            assertSame(row, show(h, page, recycler, 360));
            assertCardsFit(row, 2, "attachments recreated");
        }
    }
}
