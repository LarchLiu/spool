package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.widget.LinearLayout;
import androidx.core.widget.NestedScrollView;
import android.widget.TextView;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeTranscriptViewportTest {
    private void settle(NestedScrollView scroll) {
        for (int i = 0; i < 8; i++) {
            scroll.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
            scroll.layout(0, 0, 360, 800); scroll.getViewTreeObserver().dispatchOnPreDraw();
        }
    }
    private int mounted(LinearLayout content) {
        int count = 0;
        for (int i = 0; i < content.getChildCount(); i++) if (((NativeTranscriptViewport.Row) content.getChildAt(i)).mounted()) count++;
        return count;
    }

    @Test public void thousandTurnHistoryMountsOnlyViewportAndRestoresRowsWithoutDroppingAnyData() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NestedScrollView scroll = new NestedScrollView(controller.get()); LinearLayout content = ui.column(); scroll.addView(content); controller.get().setContentView(scroll);
            NativeHistoryScroll.State state = new NativeHistoryScroll.State(); state.select("large");
            NativeHistoryScroll behavior = new NativeHistoryScroll(scroll, content, state, 90);
            NativeTranscriptViewport viewport = new NativeTranscriptViewport(scroll, content, behavior, state);
            AtomicInteger created = new AtomicInteger();
            for (int i = 0; i < 1000; i++) {
                final int index = i; NativeTranscriptViewport.Row row = new NativeTranscriptViewport.Row(ui);
                row.bind(children -> children.use("body", "body", () -> {
                    created.incrementAndGet(); TextView text = ui.text("Turn " + index, 14, ui.text); text.setMinHeight(240); text.setTextIsSelectable(true); return text;
                })); content.addView(row, new LinearLayout.LayoutParams(-1, -2));
            }
            behavior.changing(null); behavior.changed(); settle(scroll);
            assertEquals(1000, content.getChildCount()); assertTrue("created=" + created, created.get() < 20);
            assertTrue(mounted(content) < 16); assertTrue(((NativeTranscriptViewport.Row) content.getChildAt(999)).mounted());
            scroll.scrollTo(0, 0); settle(scroll);
            NativeTranscriptViewport.Row first = (NativeTranscriptViewport.Row) content.getChildAt(0);
            assertTrue(first.mounted()); assertEquals("Turn 0", ((TextView) first.getChildAt(0)).getText().toString());
            assertFalse(((NativeTranscriptViewport.Row) content.getChildAt(999)).mounted());
            TextView selected = (TextView) first.getChildAt(0); Object document = selected.getText(); Selection.setSelection((Spannable) document, 1, 4);
            scroll.scrollTo(0, 90000); settle(scroll);
            assertTrue("Explicit selection pins its document", first.mounted()); assertSame(document, selected.getText()); assertEquals(4, selected.getSelectionEnd());
            Selection.setSelection((Spannable) document, 0); selected.clearFocus(); settle(scroll); assertFalse(first.mounted());
            assertTrue(mounted(content) < 16); scroll.scrollTo(0, 0); settle(scroll); assertTrue(first.mounted());
            first.bind(children -> children.use("body", "changed", () -> ui.text("Updated first turn", 14, ui.text)));
            settle(scroll); assertEquals("Updated first turn", ((TextView) first.getChildAt(0)).getText().toString());
            viewport.dispose(); behavior.dispose();
        }
    }

    @Test public void nearbyDetachedDocumentsAreReusedButCacheIsBoundedAndClearedOnDispose() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NestedScrollView scroll = new NestedScrollView(controller.get()); LinearLayout content = ui.column(); scroll.addView(content); controller.get().setContentView(scroll);
            NativeHistoryScroll.State state = new NativeHistoryScroll.State(); state.following = false;
            NativeHistoryScroll behavior = new NativeHistoryScroll(scroll, content, state, 90);
            NativeTranscriptViewport viewport = new NativeTranscriptViewport(scroll, content, behavior, state);
            for (int i = 0; i < 100; i++) {
                final int index = i; NativeTranscriptViewport.Row row = new NativeTranscriptViewport.Row(ui); row.partCount = 200;
                row.bind(children -> children.use("body", index, () -> {
                    TextView text = ui.text("Turn " + index, 14, ui.text); text.setMinHeight(240); return text;
                })); content.addView(row);
            }
            settle(scroll);
            NativeTranscriptViewport.Row first = (NativeTranscriptViewport.Row) content.getChildAt(0);
            View original = first.getChildAt(0);
            scroll.scrollTo(0, 2000); settle(scroll); assertFalse(first.mounted());
            long deferred = viewport.metrics().getLong("drawDeferrals");
            scroll.scrollTo(0, 0); settle(scroll); assertSame("Reversing scroll must not rebuild the same document", original, first.getChildAt(0));
            assertEquals("Known-height remounts must not cancel a scroll frame", deferred, viewport.metrics().getLong("drawDeferrals"));
            assertTrue(viewport.metrics().getLong("cacheHits") > 0);
            scroll.scrollTo(0, 2000); settle(scroll);
            first.bind(children -> children.use("body", "updated", () -> { TextView text = ui.text("Updated offscreen", 14, ui.text); text.setMinHeight(240); return text; }));
            scroll.scrollTo(0, 0); settle(scroll); assertEquals("Updated offscreen", ((TextView) first.getChildAt(0)).getText().toString());
            for (int y = 0; y < 18000; y += 1600) { scroll.scrollTo(0, y); settle(scroll); }
            assertTrue(viewport.metrics().getInt("cachedRows") <= 12); assertTrue(viewport.metrics().getInt("cachedViews") <= 768);
            assertTrue(viewport.metrics().getInt("cachedCharacters") <= 262144); assertTrue(viewport.metrics().getInt("cachedParts") <= 2048);
            assertTrue("Only viewport rows remain attached", mounted(content) < 16);
            viewport.dispose(); behavior.dispose(); assertEquals(0, viewport.metrics().getInt("cachedRows"));
            assertEquals(0, viewport.metrics().getInt("cachedViews")); assertEquals(0, viewport.metrics().getInt("cachedCharacters")); assertEquals(0, viewport.metrics().getInt("cachedParts"));
        }
    }

    @Test public void giantRowsDoNotEnterTheWarmDocumentCache() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NestedScrollView scroll = new NestedScrollView(controller.get()); LinearLayout content = ui.column(); scroll.addView(content); controller.get().setContentView(scroll);
            NativeHistoryScroll.State state = new NativeHistoryScroll.State(); state.following = false;
            NativeHistoryScroll behavior = new NativeHistoryScroll(scroll, content, state, 90);
            NativeTranscriptViewport viewport = new NativeTranscriptViewport(scroll, content, behavior, state);
            AtomicInteger created = new AtomicInteger();
            NativeTranscriptViewport.Row first = new NativeTranscriptViewport.Row(ui); first.partCount = 10000;
            first.bind(children -> children.use("body", "body", () -> { created.incrementAndGet(); View body = new View(ui.context); body.setMinimumHeight(240); return body; })); content.addView(first);
            View spacer = new View(ui.context); spacer.setMinimumHeight(12000); content.addView(spacer);
            settle(scroll); scroll.scrollTo(0, 6000); settle(scroll); assertFalse(first.mounted());
            assertEquals(0, viewport.metrics().getInt("cachedRows"));
            scroll.scrollTo(0, 0); settle(scroll); assertEquals(2, created.get());
            viewport.dispose(); behavior.dispose();
        }
    }
}
