package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.app.Dialog;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.drawable.BitmapDrawable;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.WindowManager;
import android.widget.LinearLayout;
import java.io.File;
import java.io.FileOutputStream;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeModalTest {
    private void layout(View view, int width, int height, int heightMode) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, heightMode));
        view.layout(0, 0, width, view.getMeasuredHeight());
    }

    private void stage(Dialog dialog, NativeUi ui, int height) {
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        layout(dialog.getWindow().getDecorView(), ui.dp(360), ui.dp(height), View.MeasureSpec.AT_MOST);
    }

    private void touch(Dialog dialog, NativeModalFrame frame, float x1, float y1, float x2, float y2, boolean cancel) {
        View decor = dialog.getWindow().getDecorView();
        int[] origin = new int[2], root = new int[2];
        frame.getLocationOnScreen(origin); decor.getLocationOnScreen(root);
        for (int action : new int[] {MotionEvent.ACTION_DOWN, cancel ? MotionEvent.ACTION_CANCEL : MotionEvent.ACTION_UP}) {
            boolean down = action == MotionEvent.ACTION_DOWN;
            MotionEvent event = MotionEvent.obtain(0, down ? 0 : 16, action,
                    origin[0] - root[0] + (down ? x1 : x2), origin[1] - root[1] + (down ? y1 : y2), 0);
            try { decor.dispatchTouchEvent(event); } finally { event.recycle(); }
        }
    }

    private void escape(Dialog dialog) {
        dialog.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ESCAPE));
        dialog.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_ESCAPE));
    }

    @Test public void actualDialogsUseOriginalProfilesWithoutPlatformElevationOrDuplicateImageShadow() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeSettings settings = new NativeSettings(ui, "system", "system", "test", (t, l) -> {}, () -> {});
                NativeDialog avatar = new NativeDialog(ui, "Avatar", "Close");
                avatar.body.addView(ui.text("Choose an avatar", 12, ui.text));
                NativeGroupPicker group = new NativeGroupPicker(ui, null, (n, m, c) -> {}, g -> {});
                NativeImagePreview image = new NativeImagePreview(ui, "image.png", () -> {});
                Dialog[] dialogs = {settings, avatar, group, image};
                NativeModalFrame[] frames = {settings.modal, avatar.modal, group.modal, image.modal};
                NativeModalFrame.Profile[] profiles = NativeModalFrame.Profile.values();
                for (int i = 0; i < dialogs.length; i++) {
                    Dialog dialog = dialogs[i]; NativeModalFrame frame = frames[i];
                    assertSame(profiles[i], frame.profile);
                    dialog.show(); stage(dialog, ui, 800);
                    WindowManager.LayoutParams attributes = dialog.getWindow().getAttributes();
                    assertEquals(WindowManager.LayoutParams.MATCH_PARENT, attributes.width);
                    assertEquals(profiles[i].dim, attributes.dimAmount, .00001f);
                    assertTrue((attributes.flags & WindowManager.LayoutParams.FLAG_DIM_BEHIND) != 0);
                    assertEquals(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE, attributes.softInputMode & WindowManager.LayoutParams.SOFT_INPUT_MASK_ADJUST);
                    assertEquals(0, attributes.windowAnimations);
                    assertEquals(0f, dialog.getWindow().getDecorView().getElevation(), 0f);
                    assertEquals(0f, frame.surface.getElevation(), 0f);
                    assertEquals(ui.dp(profiles[i].radius), (float) ReflectionHelpers.getField(frame.surface, "radius"), 0f);
                    assertEquals(ui.dp(profiles[i].offset), (float) ReflectionHelpers.getField(frame.surface, "offsetY"), 0f);
                    assertEquals(ui.dp(profiles[i].blur) / 2f, (float) ReflectionHelpers.getField(frame.surface, "sigma"), 0f);
                    Paint shadow = ReflectionHelpers.getField(frame.surface, "shadowPaint");
                    assertEquals(profiles[i].shadow, (int) ReflectionHelpers.getField(shadow.getColorFilter(), "mColor"));
                    assertEquals(i == 3 ? Math.round(ui.dp(360) * .94f) : ui.dp(360 - profiles[i].reserveX), frame.surface.getWidth());
                    assertEquals(frame.getWidth(), frame.panel.left + frame.panel.right, .001f);
                    assertEquals(frame.getHeight(), frame.panel.top + frame.panel.bottom, .001f);
                    if (i == 3) {
                        assertSame(image.content, frame.surface);
                        assertEquals(new Rect(), frame.surface.shadowOutsets());
                        if (android.os.Build.VERSION.SDK_INT >= 31) {
                            assertTrue((attributes.flags & WindowManager.LayoutParams.FLAG_BLUR_BEHIND) != 0);
                            assertEquals(ui.dp(6), attributes.getBlurBehindRadius());
                        }
                    }
                    dialog.dismiss();
                }
            }
        }
    }

    @Test public void settingsOutsideRectangleDismissesButCornerAndInterruptedDragsDoNot() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false); AtomicInteger closed = new AtomicInteger();
                NativeSettings dialog = new NativeSettings(ui, "system", "system", "test", (t, l) -> {}, closed::incrementAndGet);
                dialog.show(); stage(dialog, ui, 800);
                assertTrue(dialog.content.close.hasFocus());
                NativeModalFrame frame = dialog.modal;
                float center = frame.panel.centerY();
                touch(dialog, frame, frame.panel.left, frame.panel.top, frame.panel.left, frame.panel.top, false);
                assertTrue("Web uses the panel rectangle, not the rounded outline, for dismissal", dialog.isShowing());
                touch(dialog, frame, 0, center, frame.panel.centerX(), center, false);
                assertTrue(dialog.isShowing());
                touch(dialog, frame, 0, center, 0, center, true);
                assertTrue(dialog.isShowing());
                touch(dialog, frame, 0, center, 0, center, false);
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertFalse(dialog.isShowing()); assertEquals(1, closed.get());
            }
        }
    }

    @Test public void avatarSavingBlocksOutsideEscapeAndCloseUntilUnlocked() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeDialog dialog = new NativeDialog(ui, "Avatar", "Close");
            dialog.body.addView(ui.text("Choose an avatar", 12, ui.text)); dialog.show(); stage(dialog, ui, 800);
            dialog.lock(true);
            touch(dialog, dialog.modal, 0, dialog.modal.panel.centerY(), 0, dialog.modal.panel.centerY(), false);
            escape(dialog); assertTrue(dialog.isShowing());
            NativeUi.IconButton close = ReflectionHelpers.getField(dialog, "close");
            assertFalse(close.isEnabled());
            int[] button = new int[2], origin = new int[2]; close.getLocationOnScreen(button); dialog.modal.getLocationOnScreen(origin);
            float x = button[0] - origin[0] + close.getWidth() / 2f, y = button[1] - origin[1] + close.getHeight() / 2f;
            touch(dialog, dialog.modal, x, y, x, y, false); assertTrue(dialog.isShowing());
            dialog.lock(false); escape(dialog);
            assertFalse(dialog.isShowing());
        }
    }

    @Test public void groupOutsideRemainsOpenWhileImageOutsideAndEscapeDismiss() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeGroupPicker group = new NativeGroupPicker(ui, null, (n, m, c) -> {}, g -> {});
            group.show(); stage(group, ui, 800); assertTrue(group.name.hasFocus());
            touch(group, group.modal, 0, group.modal.panel.centerY(), 0, group.modal.panel.centerY(), false);
            assertTrue(group.isShowing()); escape(group); assertFalse(group.isShowing());
            NativeImagePreview image = new NativeImagePreview(ui, "image.png", () -> {});
            image.show(); stage(image, ui, 800); assertTrue(image.content.close.hasFocus());
            touch(image, image.modal, 0, image.modal.panel.centerY(), 0, image.modal.panel.centerY(), false);
            assertFalse(image.isShowing());
        }
    }

    @Test public void tallBodiesAndImagesRespectResizedWindowRatherThanDisplayHeight() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeSettings settings = new NativeSettings(ui, "system", "system", "test", (t, l) -> {}, () -> {});
                settings.content.additions.addView(ui.space(1000)); settings.show();
                NativeImagePreview image = new NativeImagePreview(ui, "tall.png", null);
                Bitmap raster = Bitmap.createBitmap(ui.dp(300), ui.dp(1800), Bitmap.Config.ARGB_8888);
                image.content.image.setImageDrawable(new BitmapDrawable(ui.context.getResources(), raster));
                image.content.loaded(null); image.show();
                for (int height : new int[] {800, 420, 260, 800}) {
                    for (NativeModalFrame frame : new NativeModalFrame[] {settings.modal, image.modal}) {
                        layout(frame, ui.dp(360), ui.dp(height), View.MeasureSpec.AT_MOST);
                        int maximum = Math.min(ui.dp(height - frame.profile.reserveY), Math.round(ui.dp(height) * frame.profile.heightRatio));
                        assertTrue(frame.panel.height() <= maximum);
                        assertTrue(frame.getHeight() <= ui.dp(height));
                        assertTrue(frame.panel.top >= 0 && frame.panel.bottom <= frame.getHeight());
                        assertEquals(frame.getHeight(), frame.panel.top + frame.panel.bottom, .001f);
                    }
                    assertTrue(image.content.image.getBottom() <= image.content.getHeight() - ui.dp(1));
                    assertTrue(image.content.image.getHeight() > 0);
                    assertEquals((float) raster.getWidth() / raster.getHeight(), (float) image.content.image.getWidth() / image.content.image.getHeight(), .01f);
                }
                for (int width : new int[] {320, 360, 640, 1280}) {
                    for (NativeModalFrame frame : new NativeModalFrame[] {settings.modal, image.modal}) {
                        layout(frame, ui.dp(width), ui.dp(420), View.MeasureSpec.AT_MOST);
                        float scale = ui.context.getResources().getDisplayMetrics().density;
                        float maximumWidth = Math.min(frame.profile.width * scale, Math.min(ui.dp(width) - frame.profile.reserveX * scale, ui.dp(width) * frame.profile.widthRatio));
                        assertEquals(maximumWidth, frame.panel.width(), .0001f);
                        assertEquals(frame.getWidth(), frame.panel.left + frame.panel.right, .001f);
                        assertTrue(frame.panel.left >= 0 && frame.panel.right <= frame.getWidth());
                    }
                }
                image.dismiss(); settings.dismiss(); raster.recycle();
            }
        }
    }

    @Test public void imageBorderAndOutsideHitRectangleRetainFractionalViewportWidthAtEveryDensity() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            for (int width : new int[] {320, 360, 412}) {
                RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + density);
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), false);
                    NativeImagePreview dialog = new NativeImagePreview(ui, "image.png", null);
                    AtomicInteger canceled = new AtomicInteger(); dialog.setOnCancelListener(d -> canceled.incrementAndGet());
                    dialog.show(); Shadows.shadowOf(Looper.getMainLooper()).idle();
                    layout(dialog.getWindow().getDecorView(), ui.dp(width), ui.dp(800), View.MeasureSpec.AT_MOST);
                    NativeModalFrame frame = dialog.modal;
                    float expected = ui.dp(width) * .94f;
                    // Subtracting two float coordinates can lose an ULP; check the stored extent directly.
                    assertEquals(expected, (float) ReflectionHelpers.getField(frame, "borderWidth"), 0f);
                    assertEquals(frame.panel.left + expected, frame.panel.right, 0f);
                    android.graphics.RectF outer = ReflectionHelpers.getField(frame.surface, "outer");
                    float scale = ui.context.getResources().getDisplayMetrics().density;
                    assertEquals(Math.round(frame.panel.left / scale) * scale, frame.surface.getX() + outer.left, .0001f);
                    assertEquals(Math.round(frame.panel.right / scale) * scale, frame.surface.getX() + outer.right, .0001f);
                    assertEquals(frame.panel.left, frame.surface.getX(), .0001f);
                    assertEquals((ui.dp(width) - expected) / 2f, frame.panel.left, .0001f);
                    assertEquals(1f, frame.surface.getScaleX(), 0f);
                    touch(dialog, frame, frame.panel.right + .05f, frame.panel.centerY(), frame.panel.right + .05f, frame.panel.centerY(), false);
                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                    assertFalse(dialog.isShowing());
                    assertEquals("The backdrop cancels; it must not fall through to image-content dismissal", 1, canceled.get());
                }
            }
        }
    }

    @Test @Config(sdk = 35) public void modalSurfaceReferencesUseProductionFramesAndAllOriginalProfiles() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    for (NativeModalFrame.Profile profile : NativeModalFrame.Profile.values()) {
                        LinearLayout body = ui.column(); body.addView(ui.space(238));
                        NativeModalFrame frame = new NativeModalFrame(ui, profile, body, null);
                        controller.get().setContentView(frame);
                        Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888);
                        Canvas canvas = new Canvas(image); canvas.drawColor(ui.elevated);
                        Paint dim = new Paint(); dim.setColor(Math.round(profile.dim * 255) << 24); canvas.drawRect(0, 0, width, 800, dim);
                        layout(frame, width, 800, View.MeasureSpec.EXACTLY); frame.draw(canvas);
                        String filename = "native-modal-" + profile.name().toLowerCase(java.util.Locale.ROOT) + "-surface-" + width + (dark ? "-dark.png" : "-light.png");
                        try (FileOutputStream output = new FileOutputStream(new File(directory, filename))) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                        org.json.JSONObject bounds = new org.json.JSONObject().put("left", frame.panel.left).put("top", frame.panel.top)
                                .put("width", frame.panel.width()).put("height", frame.panel.height()).put("radius", profile.radius);
                        java.nio.file.Files.writeString(new File(directory, filename.replace(".png", ".json")).toPath(), bounds.toString(2));
                        image.recycle();
                    }
                }
            }
        }
    }

    @Test @Config(sdk = 35) public void fractionalImageSurfaceReferencesRetainDenseProductionPaintAndLogicalGeometry() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            for (int width : new int[] {320, 360, 412}) {
                RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-" + density);
                for (boolean dark : new boolean[] {false, true}) {
                    try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                        NativeUi ui = new NativeUi(controller.get(), dark);
                        float scale = ui.context.getResources().getDisplayMetrics().density;
                        LinearLayout body = ui.column(); body.addView(ui.space(238));
                        NativeModalFrame frame = new NativeModalFrame(ui, NativeModalFrame.Profile.IMAGE, body, null);
                        controller.get().setContentView(frame);
                        int physicalWidth = ui.dp(width), physicalHeight = ui.dp(800);
                        Bitmap image = Bitmap.createBitmap(physicalWidth, physicalHeight, Bitmap.Config.ARGB_8888);
                        Canvas canvas = new Canvas(image); canvas.drawColor(ui.elevated);
                        Paint dim = new Paint(); dim.setColor(Math.round(.72f * 255) << 24); canvas.drawRect(0, 0, physicalWidth, physicalHeight, dim);
                        layout(frame, physicalWidth, physicalHeight, View.MeasureSpec.EXACTLY); frame.draw(canvas);
                        String dpr = scale == Math.round(scale) ? Integer.toString(Math.round(scale)) : Float.toString(scale);
                        String name = "native-modal-image-surface-" + width + (dark ? "-dark" : "-light") + "-dpr" + dpr;
                        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                        org.json.JSONObject bounds = new org.json.JSONObject().put("left", frame.panel.left).put("top", frame.panel.top)
                                .put("width", frame.panel.width()).put("height", frame.panel.height()).put("radius", 14 * scale);
                        java.nio.file.Files.writeString(new File(directory, name + ".json").toPath(), bounds.toString(2));
                        image.recycle();
                    }
                }
            }
        }
    }
}
