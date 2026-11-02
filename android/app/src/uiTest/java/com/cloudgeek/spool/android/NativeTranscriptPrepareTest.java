package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.os.Looper;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.core.widget.NestedScrollView;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowSystemClock;

/** Offscreen rows are built and measured between frames: never inside a scroll frame, never attached early. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeTranscriptPrepareTest {
    private static final class Fixture implements AutoCloseable {
        final ActivityController<Activity> controller; final NativeUi ui; final NestedScrollView scroll; final LinearLayout content;
        final NativeHistoryScroll.State state; final NativeHistoryScroll behavior; final NativeTranscriptViewport viewport;
        final AtomicInteger created = new AtomicInteger(), attached = new AtomicInteger();
        final java.util.List<String> attachedTexts = new java.util.ArrayList<>();
        Fixture(int rows, int height) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
            controller = Robolectric.buildActivity(Activity.class).setup();
            ui = new NativeUi(controller.get(), false);
            scroll = new NestedScrollView(controller.get()); content = ui.column(); scroll.addView(content); controller.get().setContentView(scroll);
            state = new NativeHistoryScroll.State(); state.following = false;
            behavior = new NativeHistoryScroll(scroll, content, state, 90);
            viewport = new NativeTranscriptViewport(scroll, content, behavior, state);
            for (int i = 0; i < rows; i++) {
                final int index = i; NativeTranscriptViewport.Row row = new NativeTranscriptViewport.Row(ui); row.partCount = 3;
                row.bind(children -> children.use("body", "body", () -> {
                    created.incrementAndGet();
                    TextView text = ui.text("Turn " + index, 14, ui.text); text.setMinHeight(height); text.setTextIsSelectable(true);
                    text.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener() {
                        public void onViewAttachedToWindow(View view) { attached.incrementAndGet(); attachedTexts.add(((TextView) view).getText().toString()); }
                        public void onViewDetachedFromWindow(View view) {}
                    });
                    return text;
                }));
                content.addView(row, new LinearLayout.LayoutParams(-1, -2));
            }
        }
        void frame() {
            scroll.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
            scroll.layout(0, 0, 360, 800); scroll.getViewTreeObserver().dispatchOnPreDraw();
        }
        void settle() { for (int i = 0; i < 8; i++) frame(); }
        NativeTranscriptViewport.Row row(int index) { return (NativeTranscriptViewport.Row) content.getChildAt(index); }
        /** The prepared row nearest to the viewport from above, or null. */
        NativeTranscriptViewport.Row nearestPreparedAbove() {
            NativeTranscriptViewport.Row found = null;
            for (int i = 0; i < content.getChildCount(); i++) if (row(i).prepared() && row(i).getBottom() <= scroll.getScrollY()) found = row(i);
            return found;
        }
        long metric(String key) { try { return viewport.metrics().getLong(key); } catch (org.json.JSONException error) { throw new AssertionError(error); } }
        @Override public void close() { viewport.dispose(); behavior.dispose(); controller.close(); }
    }
    private static void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }
    private static void idleFor(long millis) { Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(millis)); }

    @Test public void preparedRowsEnterTheViewportAtTheirRealHeightWithoutCancellingTheFrame() {
        try (Fixture f = new Fixture(100, 300)) {
            f.settle(); f.scroll.scrollTo(0, 16000); f.settle(); f.scroll.scrollTo(0, 8000); f.settle();
            assertEquals(0, f.metric("prepares"));
            assertEquals("Preparation is bounded by its budget: " + f.viewport.metrics(), 2, f.viewport.prepareNow(2));
            assertEquals(2, f.metric("prepares")); assertEquals(2, f.metric("preparedRows"));
            f.settle();
            NativeTranscriptViewport.Row prepared = f.nearestPreparedAbove();
            assertNotNull("Rows ahead of the viewport are prepared first", prepared);
            assertFalse(prepared.mounted());
            assertEquals("A prepared row already lays out at its real height, not the placeholder", 300, prepared.getHeight());
            long deferrals = f.metric("drawDeferrals"), inPlace = f.metric("inPlaceLayouts");
            // Scroll just far enough that only this prepared row crosses into the overscan band.
            f.scroll.scrollTo(0, prepared.getBottom() + f.scroll.getHeight() - 10); f.settle();
            assertTrue(prepared.mounted());
            assertEquals("Turn", ((TextView) prepared.getChildAt(0)).getText().toString().substring(0, 4));
            assertEquals("Entering at a known height must not cancel the scroll frame", deferrals, f.metric("drawDeferrals"));
            assertTrue(f.metric("inPlaceLayouts") > inPlace);
            assertEquals(1, f.metric("preparedMounts")); assertEquals(0, f.metric("stalePrepares"));
            assertEquals("The detached stage must measure like the attached row", 0, f.metric("heightMisses"));
        }
    }

    @Test public void offscreenPreparationNeverAttachesAndTheQuietGateWaitsForTheScrollerToStop() {
        try (Fixture f = new Fixture(100, 300)) {
            f.settle(); f.scroll.scrollTo(0, 16000); f.settle(); f.scroll.scrollTo(0, 8000); f.settle();
            int before = f.attached.get(), built = f.created.get();
            assertEquals(1, f.viewport.prepareNow(1));
            assertEquals("Building offscreen must not attach anything to the window", before, f.attached.get());
            assertEquals(built + 1, f.created.get());
            NativeTranscriptViewport.Row prepared = f.nearestPreparedAbove(); assertNotNull(prepared);
            // The scroll frame re-arms the quiet window; nothing is prepared until it elapses.
            f.scroll.scrollTo(0, 7900); f.frame(); idle();
            assertEquals(1, f.metric("prepares"));
            f.scroll.fling(-4000);
            for (int i = 0; i < 6; i++) { ShadowSystemClock.advanceBy(Duration.ofMillis(16)); f.scroll.computeScroll(); f.frame(); }
            idle(); assertEquals("No preparation while fling frames keep arriving", 1, f.metric("prepares"));
            // Applying real heights in quiet time may run a correction frame, but prepared documents stay detached.
            int settled = f.attachedTexts.size();
            idleFor(NativeTranscriptViewport.PREPARE_QUIET_MS + 50);
            assertTrue("Preparation resumes once the scroller has been quiet", f.metric("prepares") > 1);
            assertTrue(f.metric("prepares") <= 1 + 2 * NativeTranscriptViewport.PREPARE_AHEAD);
            java.util.List<String> later = f.attachedTexts.subList(settled, f.attachedTexts.size());
            for (int i = 0; i < f.content.getChildCount(); i++)
                if (f.row(i).prepared()) assertFalse("Prepared row " + i + " was attached by preparation", later.contains("Turn " + i));
        }
    }

    @Test public void dataChangesWhilePreparedAreRefreshedAtMount() {
        try (Fixture f = new Fixture(60, 300)) {
            f.settle(); f.scroll.scrollTo(0, 12000); f.settle(); f.scroll.scrollTo(0, 6000); f.settle();
            assertEquals(1, f.viewport.prepareNow(1)); f.settle();
            NativeTranscriptViewport.Row prepared = f.nearestPreparedAbove(); assertNotNull(prepared);
            prepared.bind(children -> children.use("body", "changed", () -> { TextView text = f.ui.text("Rebound offscreen", 14, f.ui.text); text.setMinHeight(300); return text; }));
            assertTrue(prepared.prepared()); assertTrue(prepared.stale());
            f.scroll.scrollTo(0, prepared.getBottom() + f.scroll.getHeight() - 10); f.settle();
            assertTrue(prepared.mounted());
            assertEquals("Rebound offscreen", ((TextView) prepared.getChildAt(0)).getText().toString());
            assertEquals(1, f.metric("stalePrepares")); assertEquals(1, f.metric("preparedMounts"));
        }
    }

    @Test public void preparedRowsShareTheWarmCacheBudgetAndDisposeClearsThem() throws Exception {
        try (Fixture f = new Fixture(100, 300)) {
            f.settle(); f.scroll.scrollTo(0, 8000); f.settle();
            int prepared = f.viewport.prepareNow(10);
            assertTrue(prepared > 0); assertTrue("Lookahead is bounded", prepared <= 2 * NativeTranscriptViewport.PREPARE_AHEAD);
            assertEquals(prepared, f.metric("preparedRows"));
            assertTrue(f.viewport.metrics().getInt("cachedRows") <= 12); assertTrue(f.viewport.metrics().getInt("cachedViews") <= 768);
            assertTrue(f.viewport.metrics().getInt("cachedCharacters") <= 262144); assertTrue(f.viewport.metrics().getInt("cachedParts") <= 2048);
            assertTrue(f.viewport.metrics().getInt("cachedRows") >= prepared);
            f.viewport.dispose();
            assertEquals(0, f.metric("preparedRows")); assertEquals(0, f.viewport.metrics().getInt("cachedRows"));
            assertEquals(0, f.viewport.metrics().getInt("cachedViews"));
            assertEquals(prepared, f.metric("preparedEvictions"));
            for (int i = 0; i < f.content.getChildCount(); i++) assertFalse(f.row(i).prepared());
        }
    }

    @Test public void giantRowsAreNeverPreparedOffscreen() {
        try (Fixture f = new Fixture(60, 300)) {
            for (int i = 0; i < f.content.getChildCount(); i++) f.row(i).partCount = 10000;
            f.settle(); f.scroll.scrollTo(0, 6000); f.settle();
            int built = f.created.get();
            assertEquals(0, f.viewport.prepareNow(3)); idleFor(400);
            assertEquals(0, f.metric("prepares")); assertEquals(built, f.created.get());
        }
    }
}
