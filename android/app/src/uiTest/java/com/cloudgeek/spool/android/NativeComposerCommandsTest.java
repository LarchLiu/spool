package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Rect;
import android.os.Looper;
import android.text.Spannable;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.BaseInputConnection;
import android.widget.LinearLayout;
import android.widget.PopupWindow;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;
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
public class NativeComposerCommandsTest {
    private void layout(View view, int width, int height) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
    }

    private void idle() { Shadows.shadowOf(Looper.getMainLooper()).idle(); }

    private LinearLayout page(Activity activity, NativeUi ui, NativeUi.Composer composer) {
        LinearLayout page = ui.workspace();
        page.setFocusableInTouchMode(true);
        page.addView(new View(ui.context), new LinearLayout.LayoutParams(-1, 0, 1));
        page.addView(composer);
        activity.setContentView(page);
        layout(page, ui.dp(360), ui.dp(800));
        page.requestFocus(); idle();
        return page;
    }

    private void open(NativeUi.Composer composer, String value) {
        composer.commands.bind(true, false, false);
        composer.input.requestFocus(); composer.input.setText(value); idle();
        assertTrue(composer.commands.menu.isShowing());
    }

    private void key(NativeUi.Composer composer, int key, int meta) {
        long time = android.os.SystemClock.uptimeMillis();
        assertTrue(composer.input.dispatchKeyEvent(new KeyEvent(time, time, KeyEvent.ACTION_DOWN, key, 0, meta)));
        assertTrue(composer.input.dispatchKeyEvent(new KeyEvent(time, time, KeyEvent.ACTION_UP, key, 0, meta)));
        idle();
    }

    @Test public void suggestionsKeepTheEditorAndImeFocusAndHonorWebKeyboardRules() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            AtomicInteger submits = new AtomicInteger();
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, submits::incrementAndGet);
            page(controller.get(), ui, composer);
            for (String value : new String[] {"/", "/g", "/go", "/goa", "/goal"}) {
                open(composer, value);
                PopupWindow popup = ReflectionHelpers.getField(composer.commands.menu, "window");
                assertFalse(popup.isFocusable()); assertEquals(PopupWindow.INPUT_METHOD_NEEDED, popup.getInputMethodMode());
                assertTrue(composer.input.hasFocus());
                key(composer, KeyEvent.KEYCODE_DPAD_DOWN, 0); key(composer, KeyEvent.KEYCODE_DPAD_UP, 0);
                assertEquals(value, composer.input.getText().toString());
                key(composer, KeyEvent.KEYCODE_TAB, 0);
                assertEquals("/goal ", composer.input.getText().toString());
                assertEquals(6, composer.input.getSelectionStart()); assertTrue(composer.input.hasFocus());
                assertFalse(composer.commands.menu.isShowing());
            }
            open(composer, "/go"); key(composer, KeyEvent.KEYCODE_ENTER, 0);
            assertEquals("/goal ", composer.input.getText().toString()); assertEquals(0, submits.get());
            open(composer, "/go");
            composer.input.setSelection(composer.input.length());
            composer.input.dispatchKeyEvent(new KeyEvent(0, 0, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ENTER, 0, KeyEvent.META_SHIFT_ON));
            composer.input.dispatchKeyEvent(new KeyEvent(0, 0, KeyEvent.ACTION_UP, KeyEvent.KEYCODE_ENTER, 0, KeyEvent.META_SHIFT_ON));
            assertEquals("/go\n", composer.input.getText().toString()); assertEquals(0, submits.get());
            open(composer, "/go"); key(composer, KeyEvent.KEYCODE_ESCAPE, 0);
            assertFalse(composer.commands.menu.isShowing()); composer.commands.bind(true, false, false);
            assertFalse("Rebinding must not undo Escape", composer.commands.menu.isShowing());
            composer.input.setText("/g"); idle(); assertTrue(composer.commands.menu.isShowing());
            composer.commands.option.performClick(); assertEquals("/goal ", composer.input.getText().toString());
            assertTrue(composer.input.hasFocus()); assertFalse(composer.commands.menu.isShowing());
            composer.input.setText("ordinary draft"); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON);
            key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_ALT_ON); assertEquals(2, submits.get());
            composer.commands.bind(true, false, true); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON);
            composer.commands.bind(true, true, false); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_ALT_ON);
            composer.commands.bind(false, false, false); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON);
            assertEquals("Keyboard send cannot stop a turn or bypass writable/posting state", 2, submits.get());
        }
    }

    @Test public void suggestionsAreExactAndCompositionOrLifecycleCannotSubmitOrLeaveAPopup() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); AtomicInteger submits = new AtomicInteger();
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, submits::incrementAndGet);
            LinearLayout page = page(controller.get(), ui, composer);
            open(composer, "/go");
            for (String value : new String[] {" /go", "/go ", "/go\n", "/Goal", "/goalx", "text /go"}) {
                composer.input.setText(value); idle(); assertFalse(composer.commands.menu.isShowing());
            }
            open(composer, "/go");
            android.view.inputmethod.InputConnection connection = composer.input.onCreateInputConnection(new android.view.inputmethod.EditorInfo());
            connection.setComposingRegion(0, 3);
            assertTrue(BaseInputConnection.getComposingSpanStart(composer.input.getText()) >= 0);
            Object listeners = ReflectionHelpers.getField(composer.input, "mListenerInfo");
            View.OnKeyListener listener = ReflectionHelpers.getField(listeners, "mOnKeyListener");
            for (int key : new int[] {KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_TAB, KeyEvent.KEYCODE_ESCAPE})
                assertFalse(listener.onKey(composer.input, key, new KeyEvent(0, 0, KeyEvent.ACTION_DOWN, key, 0, KeyEvent.META_META_ON)));
            assertEquals("/go", composer.input.getText().toString()); assertEquals(0, submits.get());
            BaseInputConnection.removeComposingSpans((Spannable) composer.input.getText());
            composer.commands.bind(true, true, false); assertFalse(composer.commands.menu.isShowing());
            composer.commands.bind(true, false, true); assertFalse(composer.commands.menu.isShowing());
            composer.commands.bind(false, false, false); assertFalse(composer.commands.menu.isShowing());
            open(composer, "/go"); page.requestFocus(); idle(); assertFalse(composer.commands.menu.isShowing());
            open(composer, "/go");
            NativePopover other = NativePopover.model(ui); other.content.addView(ui.text("Other menu", 12, ui.text));
            other.show(composer.model, true); assertFalse(composer.commands.menu.isShowing()); other.dismiss();
            composer.input.setText("/g"); idle(); assertTrue(composer.commands.menu.isShowing());
            page.removeView(composer); idle(); assertFalse(composer.commands.menu.isShowing()); assertNull(ui.activePopover);
        }
    }

    @Test public void hintsNoticesAndErrorsRetainSharedTranslationsSelectionScrollingAndDismissal() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String locale : new String[] {"en", "zh-CN", "ja"}) {
                NativeUi ui = new NativeUi(controller.get(), false, locale); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
                LinearLayout page = page(controller.get(), ui, composer);
                composer.input.setText("/goal fix tests");
                assertEquals(View.VISIBLE, composer.commands.hint.getVisibility());
                assertEquals(ui.t("/goal <condition> · /goal to view · /goal clear to remove"), composer.commands.hint.getText().toString());
                assertEquals(ui.t("Set a native agent goal"), composer.commands.option.detail.getText().toString());
                assertNotNull(composer.commands.option.symbol.getDrawable());
                assertTrue(composer.commands.option.createAccessibilityNodeInfo().isSelected());
                AtomicInteger dismissals = new AtomicInteger();
                String result = String.join("\n", java.util.Collections.nCopies(24, "Keep the original native session."));
                composer.commands.notice(result, dismissals::incrementAndGet);
                composer.commands.error(ui.t("Goal commands do not accept attachments"));
                layout(page, 360, 800);
                assertEquals(ui.dp(180), composer.commands.notice.getHeight());
                assertTrue(composer.commands.notice.text.isTextSelectable());
                assertTrue(composer.commands.notice.row.getHeight() > composer.commands.notice.getHeight());
                assertEquals(ui.t("Dismiss"), composer.commands.notice.close.getContentDescription());
                composer.commands.notice.close.performClick(); assertEquals(1, dismissals.get());
                assertEquals(View.ACCESSIBILITY_LIVE_REGION_ASSERTIVE, composer.commands.error.getAccessibilityLiveRegion());
                composer.commands.notice("", () -> {}); composer.commands.error("");
                assertNull(composer.commands.notice); assertNull(composer.commands.error);
                assertSame(composer.box, composer.getChildAt(0));
                composer.input.setText("ordinary draft"); assertEquals(View.GONE, composer.commands.hint.getVisibility());
            }
        }
    }

    @Test public void nonFocusableCommandPopupFollowsTheSameComposerThroughSafeWindowResizes() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w900dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, "ja"); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
                LinearLayout page = page(controller.get(), ui, composer); open(composer, "/go");
                for (int[] size : new int[][] {{360, 800}, {220, 500}, {900, 500}, {360, 560}}) {
                    layout(page, ui.dp(size[0]), ui.dp(size[1]));
                    page.getViewTreeObserver().dispatchOnGlobalLayout(); idle();
                    NativePopupFrame frame = ReflectionHelpers.getField(composer.commands.menu, "frame");
                    layout(frame, frame.window.width(), frame.window.height());
                    int[] anchor = new int[2]; composer.box.getLocationOnScreen(anchor);
                    assertEquals(Math.min(ui.dp(390), composer.box.getWidth() - ui.dp(2)), frame.panel.width());
                    assertEquals(anchor[0] + ui.dp(1), frame.window.left + frame.panel.left);
                    assertEquals(anchor[1] + ui.dp(1) - frame.panel.height() - ui.dp(8), frame.window.top + frame.panel.top);
                    assertTrue(composer.input.hasFocus()); assertEquals("/go", composer.input.getText().toString());
                    assertTrue(composer.commands.option.copy.getRight() <= composer.commands.option.shortcut.getLeft());
                }
                page.removeView(composer); idle();
                assertFalse(composer.commands.menu.isShowing()); assertNull(ui.activePopover);
                assertNull(ReflectionHelpers.getField(composer.commands.menu, "observer"));
            }
        }
    }

    private JSONObject box(ViewGroup root, View view, int x, int y) throws Exception {
        Rect rect = new Rect(0, 0, view.getWidth(), view.getHeight()); root.offsetDescendantRectToMyCoords(view, rect);
        return new JSONObject().put("x", rect.left + x).put("y", rect.top + y).put("width", rect.width()).put("height", rect.height());
    }
    private JSONObject text(ViewGroup root, TextView view, int x, int y) throws Exception {
        return box(root, view, x, y).put("text", view.getText().toString()).put("color", view.getCurrentTextColor());
    }

    @Test @Config(sdk = 35) public void productionCommandViewsRenderAllWebFixturesAtThreeWidthsThemesAndLocales() throws Exception {
        renderFixtures("native");
    }

    @Test @Config(sdk = 26) public void minimumApiCommandViewsHaveIndependentInspectableGeometry() throws Exception {
        renderFixtures("native-api26");
    }

    private void renderFixtures(String prefix) throws Exception {
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("commandCases");
        }
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
                JSONArray metrics = new JSONArray();
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject item = cases.getJSONObject(i); String id = item.getString("id");
                    try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                        NativeUi ui = new NativeUi(controller.get(), dark, locale); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                        NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
                        composer.model.bind("gpt-6.1-codex", "codex", "Choose model"); composer.permission.bind("Ask for approval", "shield-question", "Permissions");
                        LinearLayout page = page(controller.get(), ui, composer);
                        composer.input.setText(item.getString("draft")); composer.commands.bind(true, false, false);
                        String notice = item.has("noticeKey") ? ui.t(item.getString("noticeKey")) : item.optString("notice");
                        composer.commands.notice(notice, () -> {});
                        composer.commands.error(item.has("errorKey") ? ui.t(item.getString("errorKey")) : "");
                        layout(page, width, 800);
                        if (id.equals("menu")) composer.input.requestFocus();
                        idle(); layout(page, width, 800);
                        JSONObject result = new JSONObject().put("id", id).put("composer", box(page, composer, 0, 0))
                                .put("surface", box(page, composer.box, 0, 0)).put("input", box(page, composer.input, 0, 0))
                                .put("bottom", box(page, composer.bottom, 0, 0));
                        Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888); Canvas canvas = new Canvas(image); page.draw(canvas);
                        if (id.equals("menu")) {
                            assertTrue(composer.commands.menu.isShowing());
                            NativePopupFrame frame = ReflectionHelpers.getField(composer.commands.menu, "frame");
                            layout(frame, frame.window.width(), frame.window.height());
                            int x = frame.window.left, y = frame.window.top;
                            result.put("menu", box(frame, frame.surface, x, y)).put("option", box(frame, composer.commands.option, x, y))
                                    .put("symbol", box(frame, composer.commands.option.symbol, x, y)).put("title", text(frame, composer.commands.option.title, x, y))
                                    .put("detail", text(frame, composer.commands.option.detail, x, y)).put("shortcut", text(frame, composer.commands.option.shortcut, x, y));
                            canvas.save(); canvas.translate(x, y); frame.draw(canvas); canvas.restore();
                        }
                        if (composer.commands.hint.getVisibility() == View.VISIBLE) result.put("hint", text(page, composer.commands.hint, 0, 0));
                        if (composer.commands.notice != null) result.put("notice", box(page, composer.commands.notice, 0, 0))
                                .put("noticeText", text(page, composer.commands.notice.text, 0, 0)).put("close", box(page, composer.commands.notice.close, 0, 0));
                        if (composer.commands.error != null) result.put("error", text(page, composer.commands.error, 0, 0));
                        metrics.put(result);
                        try (FileOutputStream output = new FileOutputStream(new File(directory, prefix + "-commands-" + width + (dark ? "-dark-" : "-light-") + locale + "-" + id + ".png"))) {
                            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                        }
                        image.recycle(); composer.commands.menu.dismiss();
                    }
                }
                Files.writeString(new File(directory, prefix + "-commands-" + width + (dark ? "-dark-" : "-light-") + locale + ".json").toPath(), metrics.toString(2));
            }
        }
    }
}
