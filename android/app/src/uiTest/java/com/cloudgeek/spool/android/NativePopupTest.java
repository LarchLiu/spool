package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.PopupWindow;
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
public class NativePopupTest {
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    private void draw(View view, int width, int height) {
        layout(view, width, height);
        Bitmap image = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(image));
        image.recycle();
    }

    private FrameLayout page(Activity activity, NativeUi ui, View anchor) {
        FrameLayout page = new FrameLayout(activity);
        FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(ui.dp(80), ui.dp(32));
        params.leftMargin = ui.dp(120); params.topMargin = ui.dp(300);
        page.addView(anchor, params);
        activity.setContentView(page);
        layout(page, ui.dp(360), ui.dp(800));
        return page;
    }

    private NativePopover select(NativeUi ui, View anchor) {
        NativePopover menu = new NativePopover(ui, 200, 10, 5);
        for (String label : new String[] {"English", "Chinese", "Japanese"})
            menu.selectOption(label, label.equals("English"), menu::dismiss);
        menu.showSelect(anchor, 0);
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        return menu;
    }

    @Test public void shadowRoomDoesNotMoveTheMenuAndClipsOnlyAtTheViewport() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativePopupFrame frame = new NativePopupFrame(ui, new View(ui.context), 17, ui.borderStrong, 12, 40, ui.shadow);
                Rect viewport = new Rect(ui.dp(12), ui.dp(24), ui.dp(348), ui.dp(776));
                for (int[] request : new int[][] {{60, 100, 210, 80}, {-30, -20, 210, 80}, {330, 760, 210, 80}, {12, 24, 500, 900}}) {
                    Rect window = frame.place(viewport, ui.dp(request[0]), ui.dp(request[1]), ui.dp(request[2]), ui.dp(request[3]));
                    draw(frame, window.width(), window.height());
                    int width = Math.min(ui.dp(request[2]), viewport.width()), height = Math.min(ui.dp(request[3]), viewport.height());
                    int x = Math.max(viewport.left, Math.min(ui.dp(request[0]), viewport.right - width));
                    int y = Math.max(viewport.top, Math.min(ui.dp(request[1]), viewport.bottom - height));
                    assertEquals(x, window.left + frame.surface.getLeft()); assertEquals(y, window.top + frame.surface.getTop());
                    assertEquals(width, frame.surface.getWidth()); assertEquals(height, frame.surface.getHeight());
                    assertTrue(viewport.contains(window));
                    int spread = (int) Math.ceil(60 * ui.context.getResources().getDisplayMetrics().density) + 2;
                    assertEquals(Math.max(viewport.left, x - spread), window.left);
                    assertEquals(Math.max(viewport.top, y - spread + ui.dp(12)), window.top);
                    assertEquals(Math.min(viewport.right, x + width + spread), window.right);
                    assertEquals(Math.min(viewport.bottom, y + height + spread + ui.dp(12)), window.bottom);
                    assertTrue(frame.hitsPanel(frame.panel.exactCenterX(), frame.panel.exactCenterY()));
                    assertFalse(frame.hitsPanel(frame.panel.left, frame.panel.top));
                    assertFalse(frame.hitsPanel(frame.panel.right, frame.panel.exactCenterY()));
                    assertEquals(0f, frame.surface.getElevation(), 0f);
                }
            }
        }
    }

    @Test public void movingReusesTheShadowButResizeAndDetachInvalidateIt() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativePopupFrame frame = new NativePopupFrame(ui, new View(ui.context), 12, ui.line, 8, 30, ui.shadow);
            controller.get().setContentView(frame);
            Rect viewport = new Rect(0, 0, 360, 800);
            Rect window = frame.place(viewport, 70, 160, 210, 80);
            draw(frame, window.width(), window.height());
            Bitmap first = ReflectionHelpers.getField(frame.surface, "shadow");
            assertNotNull(first);
            window = frame.place(viewport, 80, 260, 210, 80);
            draw(frame, window.width(), window.height());
            assertSame(first, ReflectionHelpers.getField(frame.surface, "shadow"));
            window = frame.place(viewport, 80, 260, 210, 140);
            draw(frame, window.width(), window.height());
            assertNotSame(first, ReflectionHelpers.getField(frame.surface, "shadow"));
            controller.get().setContentView(new View(ui.context));
            assertNull(ReflectionHelpers.getField(frame.surface, "shadow"));
        }
    }

    @Test public void popupHaloAndRoundedCornersDismissThroughTheActualWindowAndReturnFocus() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                View anchor = ui.button("Language", false, () -> {});
                anchor.setFocusableInTouchMode(true);
                page(controller.get(), ui, anchor);
                for (boolean corner : new boolean[] {false, true}) {
                    NativePopover menu = select(ui, anchor);
                    PopupWindow window = ReflectionHelpers.getField(menu, "window");
                    NativePopupFrame frame = (NativePopupFrame) window.getContentView();
                    View decor = frame.getRootView();
                    assertNotSame("Dispatch through PopupDecorView, where the touch interceptor lives", frame, decor);
                    assertEquals(0f, window.getElevation(), 0f);
                    assertTrue(frame.panel.left > 0);
                    int[] origin = new int[2], root = new int[2];
                    frame.getLocationOnScreen(origin); decor.getLocationOnScreen(root);
                    float x = origin[0] - root[0] + (corner ? frame.panel.left : frame.panel.left / 2f);
                    float y = origin[1] - root[1] + (corner ? frame.panel.top : frame.panel.exactCenterY());
                    MotionEvent event = MotionEvent.obtain(0, 0, MotionEvent.ACTION_DOWN, x, y, 0);
                    try { assertTrue(decor.dispatchTouchEvent(event)); } finally { event.recycle(); }
                    assertFalse(menu.isShowing()); assertNull(ui.activePopover);
                    assertTrue(anchor.hasFocus());
                    assertNull(ReflectionHelpers.getField(menu, "observer"));
                }
            }
        }
    }

    @Test public void selectKeyboardStillNavigatesAndRestoresFocusWithExpandedWindow() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            View anchor = ui.button("Language", false, () -> {}); anchor.setFocusableInTouchMode(true);
            page(controller.get(), ui, anchor);
            NativePopover menu = select(ui, anchor);
            assertTrue(menu.content.getChildAt(0).hasFocus());
            assertTrue(menu.content.getChildAt(0).dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_UP)));
            assertTrue(menu.content.getChildAt(2).hasFocus());
            assertTrue(menu.content.getChildAt(2).dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_TAB)));
            assertFalse(menu.isShowing()); assertTrue(anchor.hasFocus());
        }
    }

    @Test public void everyMenuUsesItsOriginalWebRadiusBorderAndShadowProfile() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    View anchor = ui.button("Menu", false, () -> {});
                    page(controller.get(), ui, anchor);
                    for (int mode = 0; mode < 5; mode++) {
                        NativePopover menu = mode == 0 ? NativePopover.model(ui) : mode == 1 ? NativePopover.permission(ui) : new NativePopover(ui, 210, 12, 5);
                        menu.selectOption("English", true, menu::dismiss);
                        if (mode < 2) menu.show(anchor, true);
                        else if (mode == 2) menu.showOnline(anchor);
                        else if (mode == 3) menu.showOperators(anchor);
                        else menu.showSelect(anchor, 0);
                        NativeSurface surface = (NativeSurface) menu.panel();
                        float scale = ui.context.getResources().getDisplayMetrics().density;
                        assertEquals(new int[] {17, 13, 12, 12, 10}[mode] * scale, (float) ReflectionHelpers.getField(surface, "radius"), 0f);
                        assertEquals(mode == 2 || mode == 3 ? ui.line : ui.borderStrong, (int) ReflectionHelpers.getField(surface, "border"));
                        assertEquals((mode < 2 ? 12 : 8) * scale, (float) ReflectionHelpers.getField(surface, "offsetY"), 0f);
                        assertEquals(new int[] {20, 20, 15, 15, 14}[mode] * scale, (float) ReflectionHelpers.getField(surface, "sigma"), 0f);
                        Paint shadow = ReflectionHelpers.getField(surface, "shadowPaint");
                        assertEquals(mode == 4 ? 0x33000000 : ui.shadow, (int) ReflectionHelpers.getField(shadow.getColorFilter(), "mColor"));
                        assertEquals(0f, surface.getElevation(), 0f);
                        PopupWindow window = ReflectionHelpers.getField(menu, "window");
                        assertEquals(0f, window.getElevation(), 0f);
                        menu.dismiss();
                    }
                }
            }
        }
    }

    @Test public void modelAndPermissionProfilesPreserveWebWidthAndFollowTheAnchorUntilDetach() {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                View anchor = ui.button("Menu", false, () -> {});
                FrameLayout page = page(controller.get(), ui, anchor);
                for (boolean permission : new boolean[] {false, true}) {
                    NativePopover menu = permission ? NativePopover.permission(ui) : NativePopover.model(ui);
                    menu.content.addView(new View(ui.context), new android.widget.LinearLayout.LayoutParams(-1, ui.dp(80)));
                    menu.show(anchor, true);
                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                    NativePopupFrame frame = ReflectionHelpers.getField(menu, "frame");
                    assertEquals(ui.dp(permission ? 312 : 290), frame.panel.width());
                    assertEquals(ui.dp(permission ? 9 : 1), menu.content.getPaddingLeft());
                    assertNotNull(ReflectionHelpers.getField(menu, "observer"));
                    int oldTop = frame.window.top + frame.panel.top;
                    FrameLayout.LayoutParams params = (FrameLayout.LayoutParams) anchor.getLayoutParams();
                    params.topMargin += ui.dp(20); anchor.setLayoutParams(params);
                    layout(page, ui.dp(360), ui.dp(800));
                    page.getViewTreeObserver().dispatchOnGlobalLayout();
                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                    assertEquals(oldTop + ui.dp(20), frame.window.top + frame.panel.top);
                    page.removeView(anchor);
                    assertFalse(menu.isShowing()); assertNull(ui.activePopover);
                    assertNull(ReflectionHelpers.getField(menu, "observer"));
                    page.addView(anchor, params);
                    layout(page, ui.dp(360), ui.dp(800));
                }
            }
        }
    }
}
