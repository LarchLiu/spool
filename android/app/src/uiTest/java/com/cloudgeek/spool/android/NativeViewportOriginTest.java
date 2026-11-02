package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import androidx.core.widget.NestedScrollView;
import java.util.ArrayList;
import java.util.List;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;

/** Virtual rows must not re-snap CSS text when other rows change height or when a warm document cycles. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeViewportOriginTest {
    private void settle(NestedScrollView scroll) {
        for (int i = 0; i < 8; i++) {
            scroll.measure(View.MeasureSpec.makeMeasureSpec(1080, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(2000, View.MeasureSpec.EXACTLY));
            scroll.layout(0, 0, 1080, 2000); scroll.getViewTreeObserver().dispatchOnPreDraw();
        }
    }

    private void collect(View view, List<NativeTextView> out) {
        if (view instanceof NativeTextView && ((NativeTextView) view).cssPositioned) out.add((NativeTextView) view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) collect(((ViewGroup) view).getChildAt(i), out);
    }

    @Test public void warmDocumentsAreNotResnappedWhileDetachedOrOnRemountAndOtherRowsHeightsDoNotLeakIntoTheirOrigin() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-420dpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NestedScrollView scroll = new NestedScrollView(controller.get()); LinearLayout content = ui.column(); scroll.addView(content); controller.get().setContentView(scroll);
            NativeHistoryScroll.State state = new NativeHistoryScroll.State(); state.following = false;
            NativeHistoryScroll behavior = new NativeHistoryScroll(scroll, content, state, 90);
            NativeTranscriptViewport viewport = new NativeTranscriptViewport(scroll, content, behavior, state);
            String source = "Paragraph one with **bold** text.\n\nParagraph two.\n\n- item a\n- item b\n\nMore text here\n";
            for (int i = 0; i < 12; i++) {
                final int index = i; NativeTranscriptViewport.Row row = new NativeTranscriptViewport.Row(ui); row.partCount = 3;
                row.bind(children -> {
                    NativeUserRow user = (NativeUserRow) children.use("user", "user", () -> new NativeUserRow(ui, null, "Prompt " + index, 1000L, null, s -> {}));
                    user.bind(null, "Prompt " + index, 1000L, new ArrayList<>());
                    children.use("gap", "", () -> ui.space(22));
                    NativeMessage message = (NativeMessage) children.use("answer", "answer", () -> new NativeMessage(ui, source, false, null, 2000L, false, s -> {}));
                    message.bindSource(source);
                    children.use("turn-gap", "", () -> ui.space(38));
                });
                content.addView(row, new LinearLayout.LayoutParams(-1, -2));
            }
            settle(scroll);
            NativeTranscriptViewport.Row first = (NativeTranscriptViewport.Row) content.getChildAt(0);
            assertTrue(first.mounted());
            List<NativeTextView> texts = new ArrayList<>(); collect(first, texts);
            assertTrue("The answer must contain CSS-positioned text", texts.size() >= 5);
            List<Float> origins = new ArrayList<>();
            for (NativeTextView text : texts) origins.add(NativeUi.cssBlockOrigin(text));
            // A sibling row's height change (placeholder to real, or live growth) moves this row in
            // the document but must not change the origin its text snaps against.
            View spacer = new View(ui.context); spacer.setMinimumHeight(333); content.addView(spacer, 0);
            settle(scroll);
            for (int i = 0; i < texts.size(); i++) assertEquals(origins.get(i), NativeUi.cssBlockOrigin(texts.get(i)), 0f);
            content.removeView(spacer); settle(scroll);
            long aligns = NativeTextView.originAligns;
            int[] snaps = new int[texts.size()];
            for (int i = 0; i < texts.size(); i++) snaps[i] = texts.get(i).slowAligns;
            scroll.scrollTo(0, content.getHeight() - 2000); settle(scroll); assertFalse("the first row must release into the warm cache", first.mounted());
            for (NativeTextView text : texts) assertFalse(text.isAttachedToWindow());
            scroll.scrollTo(0, 0); settle(scroll); assertTrue(first.mounted());
            List<NativeTextView> again = new ArrayList<>(); collect(first, again);
            assertEquals("The warm cache must hand back the same documents", texts, again);
            for (int i = 0; i < texts.size(); i++) assertEquals(origins.get(i), NativeUi.cssBlockOrigin(texts.get(i)), 0f);
            long resnapped = 0;
            for (int i = 0; i < texts.size(); i++) if (texts.get(i).slowAligns != snaps[i]) resnapped++;
            assertEquals("Releasing and remounting an unchanged warm document must not rebuild its text layouts (" + (NativeTextView.originAligns - aligns) + " aligns in this cycle)", 0, resnapped);
            viewport.dispose(); behavior.dispose();
        }
    }
}
