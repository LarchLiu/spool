package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.animation.ValueAnimator;
import android.app.Activity;
import android.graphics.Insets;
import android.graphics.Rect;
import android.os.Build;
import android.os.Looper;
import android.view.DisplayCutout;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.WindowInsets;
import android.view.inputmethod.BaseInputConnection;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import java.io.File;
import java.io.FileOutputStream;
import java.time.Duration;
import java.util.List;
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
import org.robolectric.util.ReflectionHelpers.ClassParameter;
import org.json.JSONObject;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 29, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeViewportTest {
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    private WindowInsets insets(int left, int top, int right, int bottom, int keyboard, Rect cutout) throws Exception {
        if (Build.VERSION.SDK_INT >= 29) {
            WindowInsets.Builder builder = new WindowInsets.Builder();
            if (Build.VERSION.SDK_INT >= 30) {
                builder.setInsets(WindowInsets.Type.systemBars(), Insets.of(left, top, right, bottom))
                        .setInsets(WindowInsets.Type.ime(), Insets.of(0, 0, 0, keyboard))
                        .setVisible(WindowInsets.Type.systemBars(), true).setVisible(WindowInsets.Type.ime(), keyboard > 0);
            } else builder.setSystemWindowInsets(Insets.of(left, top, right, Math.max(bottom, keyboard)));
            if (cutout != null) {
                builder.setDisplayCutout(new DisplayCutout(cutout, List.of(new Rect(0, 0, cutout.left, 100))));
                if (Build.VERSION.SDK_INT >= 30) builder.setVisible(WindowInsets.Type.displayCutout(), true);
            }
            return builder.build();
        }
        return WindowInsets.class.getConstructor(Rect.class).newInstance(new Rect(left, top, right, Math.max(bottom, keyboard)));
    }

    private float durationScale() { return ReflectionHelpers.callStaticMethod(ValueAnimator.class, "getDurationScale"); }
    private void durationScale(float scale) { ReflectionHelpers.callStaticMethod(ValueAnimator.class, "setDurationScale", ClassParameter.from(float.class, scale)); }

    private void tap(View target, float x, float y) {
        for (int action : new int[] {MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP}) {
            MotionEvent event = MotionEvent.obtain(0, action == MotionEvent.ACTION_DOWN ? 0 : 16, action, x, y, 0);
            try { target.dispatchTouchEvent(event); } finally { event.recycle(); }
        }
        idle();
    }

    private JSONObject box(View view, View root) throws Exception {
        float x = 0, y = 0;
        for (View current = view; current != root; current = (View) current.getParent()) { x += current.getX(); y += current.getY(); }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }

    private JSONObject motion(NativeDrawer drawer) throws Exception { return new JSONObject().put("width", drawer.sheet.getWidth()).put("translation", drawer.sheet.getTranslationX()); }

    private void save(String name, JSONObject data) throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts")); assertTrue(directory.isDirectory() || directory.mkdirs());
        java.nio.file.Files.writeString(new File(directory, name + ".json").toPath(), data.toString(2));
    }

    @Test public void systemBarsCutoutsAndKeyboardMergeByMaximumWithoutAccumulatingPadding() throws Exception {
        RuntimeEnvironment.setQualifiers("w900dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            FrameLayout root = NativeWindow.content(controller.get(), ui); controller.get().setContentView(root);
            Rect cutout = Build.VERSION.SDK_INT >= 28 ? new Rect(56, 42, 18, 4) : null;
            for (int keyboard : new int[] {0, 240, 0, 380, 0}) {
                WindowInsets value = insets(2, 24, 3, 20, keyboard, cutout);
                root.dispatchApplyWindowInsets(value); root.dispatchApplyWindowInsets(value);
                assertEquals(cutout == null ? 2 : 56, root.getPaddingLeft());
                assertEquals(cutout == null ? 24 : 42, root.getPaddingTop());
                assertEquals(cutout == null ? 3 : 18, root.getPaddingRight());
                assertEquals(Math.max(20, keyboard), root.getPaddingBottom());
            }
            root.dispatchApplyWindowInsets(insets(0, 0, 0, 0, 0, null));
            assertEquals(0, root.getPaddingLeft() + root.getPaddingTop() + root.getPaddingRight() + root.getPaddingBottom());
            FrameLayout dark = NativeWindow.content(controller.get(), new NativeUi(controller.get(), true));
            assertEquals(0, controller.get().getWindow().getDecorView().getSystemUiVisibility() & (View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR | View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR));
            assertEquals(ui.surface, ((android.graphics.drawable.ColorDrawable) root.getBackground()).getColor());
            assertEquals(new NativeUi(controller.get(), true).surface, ((android.graphics.drawable.ColorDrawable) dark.getBackground()).getColor());
        }
    }

    @Test public void composedWorkspaceRetainsInputCompositionScrollAndBottomAlignmentThroughInsetsAndResize() throws Exception {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w900dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, "ja");
                FrameLayout root = NativeWindow.content(controller.get(), ui);
                LinearLayout page = ui.workspace();
                NativeUi.Header header = ui.new Header("New chat", "Mobile project", () -> {}, () -> {}, () -> {});
                page.addView(header); page.addView(ui.divider());
                ScrollView history = new ScrollView(ui.context); history.setFillViewport(true);
                View transcript = new View(ui.context); transcript.setMinimumHeight(ui.dp(3000)); history.addView(transcript, new ScrollView.LayoutParams(-1, ui.dp(3000)));
                page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
                NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
                page.addView(composer); root.addView(page, new FrameLayout.LayoutParams(-1, -1)); controller.get().setContentView(root);
                root.dispatchApplyWindowInsets(insets(0, ui.dp(24), 0, ui.dp(20), 0, null));
                layout(root, ui.dp(360), ui.dp(800)); composer.input.requestFocus(); idle();
                layout(root, ui.dp(360), ui.dp(800)); history.scrollTo(0, ui.dp(900));
                InputConnection connection = composer.input.onCreateInputConnection(new EditorInfo()); assertNotNull(connection);
                assertTrue(connection.setComposingText("日本語\n入力\nThree", 1));
                for (int[] viewport : new int[][] {{360, 800, 240}, {280, 600, 240}, {900, 500, 0}, {220, 500, 0}, {412, 800, 320}, {360, 800, 0}}) {
                    Rect cutout = Build.VERSION.SDK_INT >= 28 ? new Rect(ui.dp(10), ui.dp(30), ui.dp(5), 0) : null;
                    root.dispatchApplyWindowInsets(insets(ui.dp(2), ui.dp(24), ui.dp(3), ui.dp(20), ui.dp(viewport[2]), cutout));
                    layout(root, ui.dp(viewport[0]), ui.dp(viewport[1]));
                    assertEquals(root.getPaddingLeft(), page.getLeft()); assertEquals(root.getPaddingTop(), page.getTop());
                    assertEquals(root.getWidth() - root.getPaddingRight(), page.getRight());
                    assertEquals(root.getHeight() - root.getPaddingBottom(), page.getBottom());
                    assertEquals(0, header.getTop()); assertEquals(ui.dp(50) - ui.dp(1), header.getHeight());
                    assertEquals(page.getHeight(), composer.getBottom());
                    assertEquals(composer.getTop(), history.getBottom()); assertTrue(history.getHeight() > 0);
                    assertEquals(ui.dp(900), history.getScrollY());
                    assertEquals(0, BaseInputConnection.getComposingSpanStart(composer.input.getText()));
                    assertEquals(composer.input.length(), BaseInputConnection.getComposingSpanEnd(composer.input.getText()));
                    assertEquals(composer.input.length(), composer.input.getSelectionEnd());
                    assertEquals("日本語\n入力\nThree", composer.input.getText().toString());
                    assertEquals(composer.bottom.getWidth() - composer.bottom.getPaddingRight(), composer.send.getRight());
                    assertTrue(composer.model.getLeft() >= 0);
                    boolean phone = page.getWidth() <= ui.dp(600);
                    assertEquals(ui.dp(phone ? 16 : 14), composer.input.getTextSize(), .51f);
                    assertEquals(page.getWidth() > ui.dp(400), composer.permission.label.getVisibility() == View.VISIBLE);
                }
                assertTrue(connection.finishComposingText()); assertEquals(-1, BaseInputConnection.getComposingSpanStart(composer.input.getText()));
            }
        }
    }

    @Test public void bothDrawersUseLiveSafeWindowBoundsInsteadOfTheDisplayAndPreserveTheirChildren() throws Exception {
        RuntimeEnvironment.setQualifiers("w900dp-h800dp-mdpi");
        float saved = durationScale(); durationScale(0);
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            FrameLayout root = NativeWindow.content(controller.get(), ui); controller.get().setContentView(root);
            for (boolean right : new boolean[] {false, true}) {
                NativeDrawer drawer = new NativeDrawer(ui, right, () -> {});
                ScrollView scroll = new ScrollView(ui.context); View content = new View(ui.context);
                content.setMinimumHeight(3000);
                scroll.addView(content, new ScrollView.LayoutParams(-1, 3000)); drawer.sheet.addView(scroll, new LinearLayout.LayoutParams(-1, -1));
                root.addView(drawer, new FrameLayout.LayoutParams(-1, -1)); drawer.show();
                for (int[] viewport : new int[][] {{360, 800, 0}, {280, 600, 240}, {220, 500, 0}, {412, 800, 320}, {900, 500, 0}}) {
                    Rect cutout = Build.VERSION.SDK_INT >= 28 ? new Rect(56, 30, 18, 0) : null;
                    root.dispatchApplyWindowInsets(insets(2, 24, 3, 20, viewport[2], cutout)); layout(root, viewport[0], viewport[1]);
                    int available = viewport[0] - root.getPaddingLeft() - root.getPaddingRight();
                    assertEquals(Math.min(right ? 360 : 298, Math.round(available * (right ? .92f : .88f))), drawer.sheet.getWidth());
                    assertEquals(right ? drawer.getWidth() : drawer.sheet.getWidth(), drawer.sheet.getRight());
                    assertEquals(right ? drawer.getWidth() - drawer.sheet.getWidth() : 0, drawer.sheet.getLeft());
                    assertEquals(drawer.getHeight(), drawer.sheet.getHeight());
                    assertEquals(0f, drawer.sheet.getTranslationX(), 0f);
                    assertSame(scroll, drawer.sheet.getChildAt(0));
                    scroll.scrollTo(0, 500); assertEquals(500, scroll.getScrollY());
                }
                drawer.dismiss(); assertNull(drawer.getParent());
            }
        } finally { durationScale(saved); }
    }

    @Test public void openingClosingAndDetachKeepNormalizedMotionAndNeverRunAnOldRemovalCallback() throws Exception {
        RuntimeEnvironment.setQualifiers("w900dp-h800dp-mdpi");
        float saved = durationScale(); durationScale(1);
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); FrameLayout root = new FrameLayout(ui.context);
            controller.get().setContentView(root);
            JSONObject samples = new JSONObject();
            for (boolean right : new boolean[] {false, true}) {
                NativeDrawer drawer = new NativeDrawer(ui, right, () -> {}); root.addView(drawer, new FrameLayout.LayoutParams(-1, -1));
                layout(root, 360, 800); drawer.show();
                View scrim = drawer.getChildAt(0); assertEquals(1f, scrim.getAlpha(), 0f); assertEquals(View.VISIBLE, scrim.getVisibility());
                ValueAnimator motion = ReflectionHelpers.getField(drawer, "animation"); assertNotNull(motion);
                motion.setCurrentPlayTime(80); float hidden = ReflectionHelpers.getField(drawer, "hidden");
                JSONObject before = motion(drawer);
                assertTrue(hidden > 0 && hidden < 1); layout(root, 220, 500);
                JSONObject resized = motion(drawer);
                assertSame(motion, ReflectionHelpers.getField(drawer, "animation"));
                assertEquals((right ? 1 : -1) * drawer.sheet.getWidth() * 1.05f * hidden, drawer.sheet.getTranslationX(), .0001f);
                motion.end(); assertEquals(0f, drawer.sheet.getTranslationX(), 0f);
                drawer.dismiss(); ValueAnimator closing = ReflectionHelpers.getField(drawer, "animation"); assertNotNull(closing);
                assertEquals(View.INVISIBLE, scrim.getVisibility()); drawer.dismiss();
                assertSame(closing, ReflectionHelpers.getField(drawer, "animation"));
                closing.setCurrentPlayTime(80); hidden = ReflectionHelpers.getField(drawer, "hidden"); JSONObject closingBefore = motion(drawer); layout(root, 412, 800);
                samples.put(right ? "right" : "left", new JSONObject().put("opening", new JSONObject().put("before", before).put("resized", resized))
                        .put("closing", new JSONObject().put("before", closingBefore).put("resized", motion(drawer))));
                assertEquals((right ? 1 : -1) * drawer.sheet.getWidth() * 1.05f * hidden, drawer.sheet.getTranslationX(), .0001f);
                root.removeView(drawer); assertNull(ReflectionHelpers.getField(drawer, "animation"));
                root.addView(drawer); closing.end(); assertSame("A detached animation must not remove a reattached view", root, drawer.getParent()); root.removeView(drawer);
            }
            save("native-viewport-motion-api" + Build.VERSION.SDK_INT, samples);
        } finally { durationScale(saved); }
    }

    @Test public void scrimLabelsUseAllThreeLocalesAndBlankSheetTapsNeverFallThroughToClose() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        float saved = durationScale(); durationScale(0);
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String locale : new String[] {"zh-CN", "en", "ja"}) for (boolean right : new boolean[] {false, true}) {
                NativeUi ui = new NativeUi(controller.get(), false, locale); AtomicInteger close = new AtomicInteger();
                NativeDrawer drawer = new NativeDrawer(ui, right, close::incrementAndGet);
                controller.get().setContentView(drawer); drawer.show(); layout(drawer, 360, 800);
                View scrim = drawer.getChildAt(0); assertEquals(ui.t("Close sidebar"), scrim.getContentDescription());
                tap(drawer, drawer.sheet.getLeft() + drawer.sheet.getWidth() / 2f, 600); assertEquals(0, close.get());
                tap(drawer, right ? 0 : 359, 600); assertEquals(1, close.get());
                assertTrue(scrim.isFocusable());
                scrim.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_SPACE));
                scrim.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_SPACE));
                assertEquals(2, close.get());
                drawer.dismiss(); assertNull(drawer.getParent());
            }
        } finally { durationScale(saved); }
    }

    @Test @Config(sdk = 35) public void productionWorkspaceAndDrawerShellsRenderAcrossLiveViewportSizes() throws Exception {
        RuntimeEnvironment.setQualifiers("w900dp-h800dp-mdpi");
        float saved = durationScale(); durationScale(0);
        try {
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark); FrameLayout root = new FrameLayout(ui.context); root.setBackgroundColor(ui.surface);
                    LinearLayout page = ui.workspace(); NativeUi.Header header = ui.new Header("New chat", "Mobile project", () -> {}, () -> {}, () -> {});
                    page.addView(header); page.addView(ui.divider()); ScrollView history = new ScrollView(ui.context); history.setFillViewport(true);
                    View transcript = new View(ui.context); transcript.setMinimumHeight(3000); history.addView(transcript, new ScrollView.LayoutParams(-1, 3000));
                    page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
                    NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
                    composer.model.bind("gpt-6.1-codex", "codex", "Choose a model"); composer.permission.bind("Ask for approval", "shield-question", "Choose permission mode");
                    page.addView(composer); root.addView(page, new FrameLayout.LayoutParams(-1, -1)); controller.get().setContentView(root);
                    int[][] cases = {{360,800},{360,560},{280,360},{220,500},{900,500}};
                    String[] names = {"full","keyboard","split","narrow","wide"};
                    for (int i = 0; i < cases.length; i++) {
                        for (int side = 0; side < 3; side++) {
                            NativeDrawer drawer = side == 0 ? null : new NativeDrawer(ui, side == 2, () -> {});
                            if (drawer != null) { root.addView(drawer, new FrameLayout.LayoutParams(-1, -1)); drawer.show(); }
                            layout(root, cases[i][0], cases[i][1]);
                            JSONObject headerBox = box(header, root); headerBox.put("height", header.getHeight() + 1);
                            JSONObject geometry = new JSONObject().put("page", box(page, root)).put("header", headerBox).put("history", box(history, root))
                                    .put("composer", box(composer, root)).put("surface", box(composer.getChildAt(0), root)).put("input", box(composer.input, root))
                                    .put("model", box(composer.model, root)).put("send", box(composer.send, root)).put("permissionLabelVisible", composer.permission.label.getVisibility() == View.VISIBLE);
                            if (drawer != null) geometry.put("sheet", box(drawer.sheet, root)).put("scrim", box(drawer.getChildAt(0), root))
                                    .put("scrimColor", ((android.graphics.drawable.ColorDrawable) drawer.getChildAt(0).getBackground()).getColor());
                            String name = "viewport-" + names[i] + "-" + new String[] {"workspace","left","right"}[side] + (dark ? "-dark" : "-light");
                            save("native-" + name, geometry);
                            android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(root.getWidth(), root.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                            root.draw(new android.graphics.Canvas(image));
                            try (FileOutputStream output = new FileOutputStream(new File(System.getProperty("spool.ui.artifacts"), "native-" + name + ".png"))) {
                                assertTrue(image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output));
                            }
                            image.recycle(); if (drawer != null) root.removeView(drawer);
                        }
                    }
                }
            }
        } finally { durationScale(saved); }
    }
}
