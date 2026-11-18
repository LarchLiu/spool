package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.animation.ValueAnimator;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeChatStatusTest {
    private Bitmap draw(NativeChatStatus status) {
        status.measure(View.MeasureSpec.makeMeasureSpec(12, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(12, View.MeasureSpec.EXACTLY));
        status.layout(0, 0, 12, 12);
        Bitmap image = Bitmap.createBitmap(12, 12, Bitmap.Config.ARGB_8888);
        status.draw(new Canvas(image)); return image;
    }

    @Test public void nineDotsFadeInSymmetricWavesAndStopWhenHiddenOrDetached() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark);
            NativeChatStatus status = new NativeChatStatus(ui);
            assertNull(ReflectionHelpers.getField(status, "beat"));
            LinearLayout page = ui.column(); page.addView(status); controller.get().setContentView(page);
            ValueAnimator beat = ReflectionHelpers.getField(status, "beat"); assertNotNull(beat);
            assertEquals(1100, beat.getDuration());
            beat.setCurrentPlayTime(0); Bitmap start = draw(status);
            int[] centers = {1, 6, 10};
            for (int y : centers) for (int x : centers) assertTrue("All nine dots are visible", Color.alpha(start.getPixel(x, y)) > 0);
            assertEquals("The gaps stay transparent", 0, Color.alpha(start.getPixel(3, 3)));
            assertEquals(Color.alpha(start.getPixel(1, 1)), Color.alpha(start.getPixel(10, 10)));
            assertEquals(Color.alpha(start.getPixel(6, 1)), Color.alpha(start.getPixel(6, 10)));
            beat.setCurrentPlayTime(550); Bitmap middle = draw(status);
            assertTrue(Color.alpha(start.getPixel(6, 6)) > Color.alpha(middle.getPixel(6, 6)));
            assertTrue(Color.alpha(start.getPixel(1, 1)) < Color.alpha(middle.getPixel(1, 1)));
            start.recycle(); middle.recycle();
            page.setVisibility(View.GONE); assertNull(ReflectionHelpers.getField(status, "beat")); assertFalse(beat.isRunning());
            page.setVisibility(View.VISIBLE); assertNotNull(ReflectionHelpers.getField(status, "beat"));
            page.removeView(status); assertNull(ReflectionHelpers.getField(status, "beat"));
            page.addView(status); assertNotNull(ReflectionHelpers.getField(status, "beat"));
        }
    }

    @Test public void disabledSystemAnimationsLeaveAStaticAccessibleGrid() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        float previous = ReflectionHelpers.callStaticMethod(ValueAnimator.class, "getDurationScale");
        ReflectionHelpers.callStaticMethod(ValueAnimator.class, "setDurationScale", ReflectionHelpers.ClassParameter.from(float.class, 0f));
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "zh-CN");
            NativeChatStatus status = new NativeChatStatus(ui); controller.get().setContentView(status);
            assertNull(ReflectionHelpers.getField(status, "beat"));
            assertEquals(ui.t("Working"), status.getContentDescription());
            assertEquals(View.ACCESSIBILITY_LIVE_REGION_POLITE, status.getAccessibilityLiveRegion());
            Bitmap image = draw(status);
            assertEquals(Math.round(255 * .65f), Color.alpha(image.getPixel(6, 6)));
            assertEquals(Color.alpha(image.getPixel(6, 6)), Color.alpha(image.getPixel(1, 1)));
            image.recycle();
        } finally {
            ReflectionHelpers.callStaticMethod(ValueAnimator.class, "setDurationScale", ReflectionHelpers.ClassParameter.from(float.class, previous));
        }
    }

    private List<NativeChatStatus> indicators(View root) {
        List<NativeChatStatus> result = new ArrayList<>();
        if (root instanceof NativeChatStatus) result.add((NativeChatStatus) root);
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++)
            result.addAll(indicators(((ViewGroup) root).getChildAt(i)));
        return result;
    }

    private void show(MainActivityGoalTest.Harness h, JSONObject chat, boolean markwon) throws Exception {
        h.set("chat", chat); h.call("renderChat");
        if (markwon) {
            NativeMarkwonHistory history = h.get("markwonHistory"); NativeMarkwonHistoryTest.await(history);
            history.scrollToPosition(history.blocks() - 1); NativeMarkwonHistoryTest.await(history);
        } else {
            View root = h.get("root");
            root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
            root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
        }
    }

    @Test public void everyTranscriptRendererKeepsTheIndicatorUntilTheTurnActuallyFinishes() throws Exception {
        for (int mode = 0; mode < 3; mode++) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true, mode == 2)) {
            JSONArray parts = new JSONArray().put(new JSONObject("{role:'user',kind:'text',text:'Request'}"));
            if (mode == 1) for (int i = 0; i < 257; i++) parts.put(new JSONObject("{role:'assistant',kind:'thinking',text:'Thinking'}"));
            parts.put(new JSONObject("{role:'assistant',kind:'text',phase:'final_answer',text:'Streaming answer'}"));
            JSONObject turn = new JSONObject("{id:'turn',user_id:'alice',backend:'codex',status:'running',part_start:0,input_end:1,started_at:1000}")
                    .put("part_end", parts.length());
            JSONObject chat = h.chat("chat-one").put("status", "running").put("project_id", "project-one")
                    .put("turns", new JSONArray().put(turn)).put("transcript", new JSONObject().put("parts", parts));
            show(h, chat, mode == 2);
            List<NativeChatStatus> current = indicators(h.get("root")); assertEquals("Renderer " + mode, 1, current.size());
            NativeChatStatus status = current.get(0);
            assertNotNull(ReflectionHelpers.getField(status, "beat"));
            parts.getJSONObject(parts.length() - 1).put("text", "More streamed text"); show(h, chat, mode == 2);
            assertSame(status, indicators(h.get("root")).get(0));
            for (String finished : new String[] {"completed", "stopped", "failed"}) {
                turn.put("status", finished).put("finished_at", 2000); chat.put("status", finished); show(h, chat, mode == 2);
                assertTrue(indicators(h.get("root")).isEmpty()); assertNull(ReflectionHelpers.getField(status, "beat"));
                turn.put("status", "running").put("finished_at", 0); chat.put("status", "running"); show(h, chat, mode == 2);
                assertEquals(1, indicators(h.get("root")).size()); status = indicators(h.get("root")).get(0);
            }
        }
    }
}
