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
    private JSONObject command(String name, String kind) {
        try { return new JSONObject().put("name", name).put("kind", kind).put("description", name); }
        catch (Exception error) { throw new AssertionError(error); }
    }

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
        composer.commands.catalog("codex", new JSONArray().put(command("goal", "prompt")));
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
            composer.commands.options.get(0).performClick(); assertEquals("/goal ", composer.input.getText().toString());
            assertTrue(composer.input.hasFocus()); assertFalse(composer.commands.menu.isShowing());
            composer.input.setText("ordinary draft"); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON);
            key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_ALT_ON); assertEquals(2, submits.get());
            composer.commands.bind(true, false, true); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON);
            composer.commands.bind(true, true, false); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_ALT_ON);
            composer.commands.bind(false, false, false); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON);
            assertEquals("Keyboard send cannot stop a turn or bypass writable/posting state", 2, submits.get());
            // While a turn runs, Alt+Enter queues the draft and Cmd/Ctrl+Enter steers it into the turn.
            AtomicInteger queued = new AtomicInteger(), steered = new AtomicInteger();
            composer.commands.queue = queued::incrementAndGet; composer.commands.steer = steered::incrementAndGet;
            composer.commands.bind(true, false, true);
            key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_ALT_ON); assertEquals(1, queued.get()); assertEquals(0, steered.get());
            key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_META_ON); key(composer, KeyEvent.KEYCODE_ENTER, KeyEvent.META_CTRL_ON);
            assertEquals(1, queued.get()); assertEquals(2, steered.get()); assertEquals(2, submits.get());
            // A hardware Enter sends; the touch keyboard's Enter stays a line break, as does Shift+Enter.
            composer.commands.bind(true, false, false);
            KeyEvent hardware = new KeyEvent(0, 0, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ENTER, 0, 0, 7, 0);
            assertTrue(composer.input.dispatchKeyEvent(hardware));
            composer.input.dispatchKeyEvent(KeyEvent.changeAction(hardware, KeyEvent.ACTION_UP)); idle();
            assertEquals(3, submits.get()); assertEquals("ordinary draft", composer.input.getText().toString());
        }
    }

    @Test public void suggestionsAreExactAndCompositionOrLifecycleCannotSubmitOrLeaveAPopup() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false); AtomicInteger submits = new AtomicInteger();
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, submits::incrementAndGet);
            LinearLayout page = page(controller.get(), ui, composer);
            open(composer, "/go");
            for (String value : new String[] {" /go", "/go ", "/go\n", "text /go", "/tmp/file"}) {
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
                open(composer, "/go");
                composer.input.setText("/goal fix tests");
                assertEquals(View.VISIBLE, composer.commands.hint.getVisibility());
                assertEquals(ui.t("/goal <condition> · /goal to view · /goal clear to remove"), composer.commands.hint.getText().toString());
                assertEquals(ui.t("Set a goal to keep pursuing"), composer.commands.options.get(0).detail.getText().toString());
                assertNotNull(composer.commands.options.get(0).symbol.getDrawable());
                assertTrue(composer.commands.options.get(0).createAccessibilityNodeInfo().isSelected());
                AtomicInteger dismissals = new AtomicInteger();
                String result = String.join("\n", java.util.Collections.nCopies(24, "Keep the original native session."));
                composer.commands.notice(result, dismissals::incrementAndGet);
                composer.commands.error(ui.t("Goal commands do not accept attachments"), () -> {});
                layout(page, 360, 800);
                assertEquals(ui.dp(180), composer.commands.notice.getHeight());
                assertTrue(composer.commands.notice.text.isTextSelectable());
                assertTrue(composer.commands.notice.row.getHeight() > composer.commands.notice.getHeight());
                assertEquals(ui.t("Dismiss"), composer.commands.notice.close.getContentDescription());
                composer.commands.notice.close.performClick(); assertEquals(1, dismissals.get());
                assertEquals(View.ACCESSIBILITY_LIVE_REGION_ASSERTIVE, composer.commands.error.getAccessibilityLiveRegion());
                composer.commands.notice("", () -> {}); composer.commands.error("", () -> {});
                assertNull(composer.commands.notice); assertNull(composer.commands.error);
                assertSame(composer.box, composer.getChildAt(0));
                composer.input.setText("ordinary draft"); assertEquals(View.GONE, composer.commands.hint.getVisibility());
            }
        }
    }

    @Test public void catalogSearchGroupsSkillsSupportsAliasesAndKeyboardSelectionWithoutMovingFocus() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
            page(controller.get(), ui, composer);
            JSONArray catalog = new JSONArray("[{name:'goal',kind:'prompt',description:'Set a goal'},{name:'code-review',kind:'prompt',aliases:['review'],description:'Inspect changes'},"
                    + "{name:'repo:check',displayName:'Project check',kind:'skill',description:'Verify tests',scope:'repo',pluginId:'checks'}]");
            composer.commands.catalog("claude", catalog); open(composer, "/");
            assertEquals(3, composer.commands.options.size());
            assertEquals("Code review", composer.commands.options.get(0).title.getText().toString());
            assertEquals("Project check", composer.commands.options.get(2).title.getText().toString());
            assertEquals("Project · From checks", composer.commands.options.get(2).source.getText().toString());
            key(composer, KeyEvent.KEYCODE_DPAD_UP, 0); assertTrue(composer.commands.options.get(2).isSelected());
            composer.input.setText("ordinary text"); open(composer, "/");
            assertTrue(composer.commands.options.get(0).isSelected()); assertFalse(composer.commands.options.get(2).isSelected());
            key(composer, KeyEvent.KEYCODE_DPAD_UP, 0);
            assertTrue(composer.input.hasFocus()); key(composer, KeyEvent.KEYCODE_ENTER, 0);
            assertEquals("/repo:check ", composer.input.getText().toString());
            open(composer, "/rev"); assertEquals(1, composer.commands.options.size()); key(composer, KeyEvent.KEYCODE_TAB, 0);
            assertEquals("/code-review ", composer.input.getText().toString());
            open(composer, "/verify"); assertEquals(1, composer.commands.options.size());
            assertEquals("Project check", composer.commands.options.get(0).title.getText().toString());
            open(composer, "/missing"); assertTrue(composer.commands.options.isEmpty());
            assertEquals("No matching agent commands", ((TextView) composer.commands.menu.content.getChildAt(0)).getText().toString());
            composer.commands.catalog("codex", new JSONArray("[{name:'plan',kind:'control'}]")); open(composer, "/");
            assertEquals(1, composer.commands.options.size()); assertEquals("Plan mode", composer.commands.options.get(0).title.getText().toString());
            assertTrue(composer.input.hasFocus());
        }
    }

    @Test public void parsingSearchAndAliasResolutionFollowActualWebBehavior() throws Exception {
        JSONObject fixture;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            fixture = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        java.util.List<NativeSlashCommand> catalog = NativeSlashCommand.catalog(fixture.getJSONArray("commandCatalog"));
        JSONArray cases = fixture.getJSONArray("agentCommandCases");
        for (int i = 0; i < cases.length(); i++) {
            JSONObject sample = cases.getJSONObject(i); String text = sample.getString("text");
            NativeSlashCommand.Input parsed = NativeSlashCommand.parse(text);
            if (sample.isNull("parsed")) assertNull(text, parsed);
            else {
                assertNotNull(text, parsed); assertEquals(text, sample.getJSONObject("parsed").getString("name"), parsed.name);
                assertEquals(text, sample.getJSONObject("parsed").getString("arguments"), parsed.arguments);
            }
            java.util.List<NativeSlashCommand> suggestions = NativeSlashCommand.suggestions(text, catalog);
            if (sample.isNull("suggestions")) assertNull(text, suggestions);
            else {
                assertNotNull(text, suggestions); JSONArray actual = new JSONArray();
                for (NativeSlashCommand command : suggestions) actual.put(command.name);
                assertEquals(text, sample.getJSONArray("suggestions").toString(), actual.toString());
            }
            NativeSlashCommand found = NativeSlashCommand.find(catalog, parsed == null ? "" : parsed.name);
            assertEquals(text, sample.isNull("resolved") ? null : sample.getString("resolved"), found == null ? null : found.name);
        }
    }

    @Test public void longCatalogScrollsTheKeyboardSelectionInsideTheBoundedMenu() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
            page(controller.get(), ui, composer); JSONArray commands = new JSONArray();
            for (int i = 0; i < 40; i++) commands.put(command("skill-" + String.format(java.util.Locale.ROOT, "%02d", i), "skill"));
            composer.commands.catalog("codex", commands); open(composer, "/");
            NativePopupFrame frame = ReflectionHelpers.getField(composer.commands.menu, "frame");
            layout(frame, frame.window.width(), frame.window.height()); assertTrue(frame.panel.height() <= ui.dp(320));
            key(composer, KeyEvent.KEYCODE_DPAD_UP, 0);
            android.widget.ScrollView scroll = ReflectionHelpers.getField(composer.commands.menu, "scroll");
            assertTrue(scroll.getScrollY() > 0); assertTrue(composer.input.hasFocus());
            key(composer, KeyEvent.KEYCODE_TAB, 0); assertEquals("/skill-39 ", composer.input.getText().toString());
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
                    assertEquals(composer.box.getWidth() - ui.dp(2), frame.panel.width());
                    assertEquals(anchor[0] + ui.dp(1), frame.window.left + frame.panel.left);
                    assertEquals(anchor[1] + ui.dp(1) - frame.panel.height() - ui.dp(8), frame.window.top + frame.panel.top);
                    assertTrue(composer.input.hasFocus()); assertEquals("/go", composer.input.getText().toString());
                    assertTrue(composer.commands.options.get(0).detail.getWidth() >= 0);
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
        JSONArray cases, catalog;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            JSONObject fixtures = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
            cases = fixtures.getJSONArray("commandCases"); catalog = fixtures.getJSONArray("commandCatalog");
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
                        composer.commands.catalog("codex", catalog);
                        composer.input.setText(item.getString("draft")); composer.commands.bind(true, false, false);
                        String notice = item.has("noticeKey") ? ui.t(item.getString("noticeKey")) : item.optString("notice");
                        composer.commands.notice(notice, () -> {});
                        composer.commands.error(item.has("errorKey") ? ui.t(item.getString("errorKey")) : "", () -> {});
                        layout(page, width, 800);
                        if (item.optBoolean("menu")) composer.input.requestFocus();
                        idle(); layout(page, width, 800);
                        JSONObject result = new JSONObject().put("id", id).put("composer", box(page, composer, 0, 0))
                                .put("surface", box(page, composer.box, 0, 0)).put("input", box(page, composer.input, 0, 0))
                                .put("bottom", box(page, composer.bottom, 0, 0));
                        Bitmap image = Bitmap.createBitmap(width, 800, Bitmap.Config.ARGB_8888); Canvas canvas = new Canvas(image); page.draw(canvas);
                        if (item.optBoolean("menu")) {
                            assertTrue(composer.commands.menu.isShowing());
                            NativePopupFrame frame = ReflectionHelpers.getField(composer.commands.menu, "frame");
                            layout(frame, frame.window.width(), frame.window.height());
                            int x = frame.window.left, y = frame.window.top;
                            result.put("menu", box(frame, frame.surface, x, y));
                            JSONArray rows = new JSONArray();
                            for (NativeSlashCommandMenu row : composer.commands.options) {
                                JSONObject entry = new JSONObject().put("option", box(frame, row, x, y)).put("symbol", box(frame, row.symbol, x, y))
                                        .put("title", text(frame, row.title, x, y)).put("detail", text(frame, row.detail, x, y));
                                if (row.source.getParent() != null) entry.put("source", text(frame, row.source, x, y));
                                rows.put(entry);
                            }
                            result.put("rows", rows);
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
