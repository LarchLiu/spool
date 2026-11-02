package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import androidx.core.widget.NestedScrollView;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** The production Activity prepares offscreen turns between frames without changing what is attached. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityTranscriptPrepareTest {
    static JSONObject longChat(int turns) throws Exception {
        JSONArray list = new JSONArray(), parts = new JSONArray();
        for (int i = 0; i < turns; i++) {
            parts.put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Prompt " + i));
            parts.put(new JSONObject().put("role", "assistant").put("kind", "text").put("phase", "final_answer").put("text", "Answer " + i + "\n\nSecond paragraph " + i));
            list.put(new JSONObject().put("id", "long-" + i).put("user_id", "alice").put("backend", "codex").put("status", "completed").put("started_at", 1000).put("finished_at", 2000)
                    .put("part_start", i * 2).put("input_end", i * 2 + 1).put("part_end", i * 2 + 2));
        }
        return new JSONObject().put("id", "chat-one").put("title", "Title").put("project_id", "project-one").put("backend", "codex").put("can_control", true).put("updated_at", 20)
                .put("turns", list).put("status", "completed").put("transcript", new JSONObject().put("parts", parts));
    }
    static void layout(MainActivityGoalTest.Harness h) {
        View root = h.get("root"); root.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, 360, 800); root.getViewTreeObserver().dispatchOnPreDraw();
    }
    static void settle(MainActivityGoalTest.Harness h) { for (int i = 0; i < 8; i++) layout(h); }
    static <T extends View> List<T> views(View parent, Class<T> type) {
        List<T> result = new ArrayList<>();
        if (type.isInstance(parent)) result.add(type.cast(parent));
        if (parent instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) parent).getChildCount(); i++) result.addAll(views(((ViewGroup) parent).getChildAt(i), type));
        return result;
    }
    static long metric(NativeTranscriptViewport viewport, String key) throws Exception { return viewport.metrics().getLong(key); }

    @Test public void offscreenTurnsArePreparedBetweenFramesWithoutMountingOrReplacingAttachedDocuments() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value = longChat(100);
            h.set("chat", value); h.call("renderChat"); settle(h);
            LinearLayout content = h.get("historyContent"); NestedScrollView history = h.get("history");
            NativeTranscriptViewport viewport = h.get("transcriptViewport"); assertNotNull(viewport);
            assertEquals(100, content.getChildCount());
            int mountedUsers = views(content, NativeUserRow.class).size(); assertTrue(mountedUsers < 20);
            assertEquals(0, metric(viewport, "prepares"));
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(NativeTranscriptViewport.PREPARE_QUIET_MS + 100));
            assertTrue("Quiet time prepares the turns ahead of the viewport", metric(viewport, "prepares") > 0);
            assertTrue(metric(viewport, "prepares") <= 2 * NativeTranscriptViewport.PREPARE_AHEAD);
            settle(h);
            assertEquals("Prepared documents are not attached", mountedUsers, views(content, NativeUserRow.class).size());
            int preparedRows = 0;
            for (int i = 0; i < content.getChildCount(); i++) if (((NativeTranscriptViewport.Row) content.getChildAt(i)).prepared()) preparedRows++;
            assertEquals(metric(viewport, "prepares"), preparedRows);
            // Scrolling back by less than a row admits only the nearest prepared row, which attaches its document.
            NativeTranscriptViewport.Row nearest = null;
            for (int i = 0; i < content.getChildCount(); i++) if (((NativeTranscriptViewport.Row) content.getChildAt(i)).prepared()) nearest = (NativeTranscriptViewport.Row) content.getChildAt(i);
            assertNotNull(nearest); assertEquals(0, views(nearest, NativeUserRow.class).size());
            long deferrals = metric(viewport, "drawDeferrals");
            history.scrollTo(0, Math.max(0, nearest.getBottom() + history.getHeight() - 10)); settle(h);
            assertTrue(nearest.mounted()); assertEquals(1, views(nearest, NativeUserRow.class).size());
            assertTrue(views(nearest, NativeUserRow.class).get(0).isAttachedToWindow());
            assertEquals(1, metric(viewport, "preparedMounts"));
            assertEquals("Detached measurement must agree with the attached one", 0, metric(viewport, "heightMisses"));
            assertEquals("A prepared row enters without cancelling the frame", deferrals, metric(viewport, "drawDeferrals"));
            // Leaving and returning reuses the same documents as before.
            NativeUserRow last = views(content, NativeUserRow.class).get(views(content, NativeUserRow.class).size() - 1);
            history.scrollTo(0, 0); settle(h); assertFalse(last.isAttachedToWindow());
            history.scrollTo(0, content.getHeight()); settle(h);
            assertSame(last, views(content, NativeUserRow.class).get(views(content, NativeUserRow.class).size() - 1));
        }
    }

    @Test public void prependedPagesRebindOnlyTheNewTurnsAndDetachNothing() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value = longChat(40);
            h.set("chat", value); h.call("renderChat"); settle(h);
            LinearLayout content = h.get("historyContent"); NativeTranscriptViewport viewport = h.get("transcriptViewport");
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(NativeTranscriptViewport.PREPARE_QUIET_MS + 100)); settle(h);
            long prepared = metric(viewport, "prepares"); assertTrue(prepared > 0);
            NativeUserRow visible = views(content, NativeUserRow.class).get(0);
            AtomicInteger detached = new AtomicInteger();
            visible.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
                public void onViewAttachedToWindow(View view) {} public void onViewDetachedFromWindow(View view) { detached.incrementAndGet(); } });
            List<Object> renders = new ArrayList<>();
            for (int i = 0; i < content.getChildCount(); i++) renders.add(org.robolectric.util.ReflectionHelpers.getField(content.getChildAt(i), "render"));
            JSONObject older = longChat(3);
            for (int i = 0; i < 3; i++) older.getJSONArray("turns").getJSONObject(i).put("id", "older-" + i);
            NativeChatState.Merge merge = NativeChatState.prependTracked(value, older);
            assertEquals(java.util.Set.of("older-0", "older-1", "older-2"), merge.changedTurns);
            h.set("pendingRebinds", merge.changedTurns); h.set("chat", merge.chat); h.call("renderChat"); settle(h);
            assertEquals(43, content.getChildCount()); assertEquals(0, detached.get());
            for (int i = 0; i < 40; i++) assertSame("Untouched turns keep their closures", renders.get(i), org.robolectric.util.ReflectionHelpers.getField(content.getChildAt(i + 3), "render"));
            for (int i = 0; i < 3; i++) assertTrue(((NativeTranscriptViewport.Row) content.getChildAt(i)).bound());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(NativeTranscriptViewport.PREPARE_QUIET_MS + 100));
            assertTrue("New rows re-arm preparation", metric(viewport, "prepares") >= prepared);
        }
    }
}
