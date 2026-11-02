package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Rect;
import android.graphics.Insets;
import android.os.Build;
import android.view.View;
import android.view.WindowInsets;
import android.widget.FrameLayout;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
public class NativeWindowTest {
    @Test public void productionWindowShellKeepsSystemBarsAndImeOutsideContent() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            FrameLayout root = NativeWindow.content(controller.get(), ui);
            controller.get().setContentView(root);
            WindowInsets insets;
            if (Build.VERSION.SDK_INT >= 30) insets = new WindowInsets.Builder()
                    .setInsets(WindowInsets.Type.systemBars(), Insets.of(2, 24, 3, 20))
                    .setInsets(WindowInsets.Type.ime(), Insets.of(0, 0, 0, 240))
                    .setVisible(WindowInsets.Type.systemBars() | WindowInsets.Type.ime(), true).build();
            else insets = WindowInsets.class.getConstructor(Rect.class).newInstance(new Rect(2, 24, 3, 240));
            root.dispatchApplyWindowInsets(insets);
            assertEquals(2, root.getPaddingLeft()); assertEquals(24, root.getPaddingTop());
            assertEquals(3, root.getPaddingRight()); assertEquals(240, root.getPaddingBottom());
            assertTrue((controller.get().getWindow().getDecorView().getSystemUiVisibility() & View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR) != 0);
        }
    }
}
