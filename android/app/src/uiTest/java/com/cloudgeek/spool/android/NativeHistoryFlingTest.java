package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.view.View;
import android.widget.LinearLayout;
import androidx.core.widget.NestedScrollView;
import java.time.Duration;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadows.ShadowSystemClock;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeHistoryFlingTest {
    private void layout(NestedScrollView scroll) {
        scroll.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        scroll.layout(0, 0, 360, 800);
    }

    private NestedScrollView document(Activity activity) {
        NestedScrollView scroll = new NestedScrollView(activity);
        LinearLayout content = new LinearLayout(activity); content.setOrientation(LinearLayout.VERTICAL);
        content.addView(new View(activity), new LinearLayout.LayoutParams(-1, 3000));
        content.addView(new View(activity), new LinearLayout.LayoutParams(-1, 19000));
        scroll.addView(content); layout(scroll); scroll.scrollTo(0, 6000);
        return scroll;
    }

    @Test public void nativeFlingKeepsMovingFromTheCorrectedDocumentPosition() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NestedScrollView scroll = new NestedScrollView(controller.get());
            LinearLayout content = new LinearLayout(controller.get()); content.setOrientation(LinearLayout.VERTICAL);
            View prefix = new View(controller.get()), body = new View(controller.get());
            content.addView(prefix, new LinearLayout.LayoutParams(-1, 1000));
            content.addView(body, new LinearLayout.LayoutParams(-1, 19000));
            scroll.addView(content); controller.get().setContentView(scroll); layout(scroll);
            scroll.scrollTo(0, 3000); scroll.fling(4000);
            ShadowSystemClock.advanceBy(Duration.ofMillis(32)); scroll.computeScroll();
            int first = scroll.getScrollY();
            assertTrue("The fling must advance before testing rebasing", first > 3000);
            prefix.getLayoutParams().height += 2000; prefix.requestLayout(); layout(scroll);
            scroll.scrollTo(0, first + 2000);
            ShadowSystemClock.advanceBy(Duration.ofMillis(16)); scroll.computeScroll();
            assertTrue("A height correction above the reader must not be undone by the next fling frame: "
                    + scroll.getScrollY() + " < " + (first + 2000), scroll.getScrollY() >= first + 2000);
        }
    }

    @Test public void heightCorrectionsPreserveTheFlingTrajectoryInBothDirectionsWithoutRestartingIt() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (int velocity : new int[] {-4000, 4000}) for (int correction : new int[] {-2000, 2000})
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NestedScrollView reference = document(controller.get()), corrected = document(controller.get());
                reference.fling(velocity); corrected.fling(velocity);
                ShadowSystemClock.advanceBy(Duration.ofMillis(32)); reference.computeScroll(); corrected.computeScroll();
                assertEquals(reference.getScrollY(), corrected.getScrollY());
                LinearLayout content = (LinearLayout) corrected.getChildAt(0);
                View prefix = content.getChildAt(0); prefix.getLayoutParams().height += correction;
                prefix.requestLayout(); layout(corrected);
                corrected.scrollTo(0, corrected.getScrollY() + correction);
                for (int frame = 0; frame < 8; frame++) {
                    ShadowSystemClock.advanceBy(Duration.ofMillis(16)); reference.computeScroll(); corrected.computeScroll();
                    assertEquals("velocity=" + velocity + " correction=" + correction + " frame=" + frame,
                            correction, corrected.getScrollY() - reference.getScrollY());
                }
            }
    }
}
