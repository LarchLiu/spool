package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.widget.LinearLayout;
import java.io.File;
import java.io.FileOutputStream;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, qualifiers = "mdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeSwitchTest {
    private Bitmap render(View view, NativeUi ui, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(width), View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(ui.dp(800), View.MeasureSpec.AT_MOST));
        view.layout(0, 0, view.getMeasuredWidth(), view.getMeasuredHeight());
        view.jumpDrawablesToCurrentState();
        Bitmap image = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(image);
        canvas.drawColor(ui.elevated);
        view.draw(canvas);
        return image;
    }

    private int pixels(Bitmap image, int color) {
        int count = 0;
        for (int y = 0; y < image.getHeight(); y++)
            for (int x = 0; x < image.getWidth(); x++)
                if (image.getPixel(x, y) == color) count++;
        return count;
    }

    @Test public void checkedTrackUsesTheFullAccentAndClicksStillToggleTheSetting() {
        for (boolean dark : new boolean[] {false, true}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark);
                NativeSwitch control = new NativeSwitch(ui, "Connect to this server");
                AtomicInteger changes = new AtomicInteger();
                control.setOnCheckedChangeListener((button, checked) -> changes.incrementAndGet());
                control.performClick();
                assertTrue(control.isChecked());
                assertEquals(1, changes.get());
                Bitmap checked = render(control, ui, 60);
                assertTrue("Enabled switches need a solid accent track in " + (dark ? "dark" : "light") + " mode",
                        pixels(checked, ui.primary) >= 40);
                checked.recycle();
                control.performClick();
                assertFalse(control.isChecked());
                assertEquals(2, changes.get());
                Bitmap unchecked = render(control, ui, 60);
                assertEquals(0, pixels(unchecked, ui.primary));
                assertTrue("Switched-off settings use the neutral track", pixels(unchecked, ui.track) >= 40);
                unchecked.recycle();
                control.setEnabled(false);
                assertEquals(.45f, control.getAlpha(), 0f);
                control.setEnabled(true);
                assertEquals(1f, control.getAlpha(), 0f);
            }
        }
    }

    @Test public void settingsShowDistinctOnOffAndUnavailableStates() throws Exception {
        for (boolean dark : new boolean[] {false, true}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, "zh-CN");
                LinearLayout content = ui.padded(25, 25);
                content.setBackgroundColor(ui.elevated);
                content.addView(ui.bold(ui.t("Settings"), 18));
                for (boolean enabled : new boolean[] {true, false}) {
                    for (boolean checked : new boolean[] {true, false}) {
                        LinearLayout row = ui.row();
                        row.addView(ui.text((enabled ? "可用" : "暂不可用") + " · " + (checked ? "已开启" : "已关闭"), 12, ui.text),
                                new LinearLayout.LayoutParams(0, -2, 1));
                        NativeSwitch control = new NativeSwitch(ui, "Setting");
                        control.setChecked(checked);
                        control.setEnabled(enabled);
                        row.addView(control);
                        content.addView(row);
                    }
                }
                Bitmap image = render(content, ui, 320);
                File directory = new File(System.getProperty("spool.ui.artifacts"), "settings-switch");
                assertTrue(directory.isDirectory() || directory.mkdirs());
                try (FileOutputStream output = new FileOutputStream(new File(directory,
                        "sdk-" + RuntimeEnvironment.getApiLevel() + "-" + (dark ? "dark" : "light") + ".png"))) {
                    assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                } finally { image.recycle(); }
            }
        }
    }
}
