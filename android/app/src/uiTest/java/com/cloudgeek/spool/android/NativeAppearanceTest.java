package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.KeyEvent;
import android.view.KeyCharacterMap;
import android.view.View;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeAppearanceTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    @Test public void selectPositionAndLanguageResolutionMatchActualWebFunctions() throws Exception {
        JSONObject fixture = fixtures();
        JSONArray layouts = fixture.getJSONArray("selectLayouts");
        for (int i = 0; i < layouts.length(); i++) {
            JSONObject test = layouts.getJSONObject(i), anchor = test.getJSONObject("anchor"), viewport = test.getJSONObject("viewport"), expected = test.getJSONObject("expected");
            NativeSelectLayout layout = new NativeSelectLayout((float) anchor.getDouble("left"), (float) anchor.getDouble("top"), (float) anchor.getDouble("right"), (float) anchor.getDouble("bottom"), (float) anchor.getDouble("width"),
                    (float) viewport.getDouble("x"), (float) viewport.getDouble("y"), (float) viewport.getDouble("width"), (float) viewport.getDouble("height"), test.getInt("count"));
            assertEquals(expected.getDouble("left"), layout.left, .001);
            assertEquals(expected.getDouble("top"), layout.top, .001);
            assertEquals(expected.getDouble("width"), layout.width, .001);
            assertEquals(expected.getDouble("maxHeight"), layout.maximum, .001);
            assertEquals(expected.getString("side").equals("top"), layout.above);
        }
        JSONArray languages = fixture.getJSONArray("languages");
        for (int i = 0; i < languages.length(); i++) {
            JSONObject test = languages.getJSONObject(i);
            JSONArray list = test.getJSONArray("preferred");
            String[] preferred = new String[list.length()];
            for (int j = 0; j < preferred.length; j++) preferred[j] = list.getString(j);
            assertEquals(test.getString("expected"), NativePreferences.language(test.getString("preference"), preferred));
        }
        assertEquals("外观与语言", NativeText.translate("Appearance and language", "zh-CN"));
        assertEquals("外観と言語", NativeText.translate("Appearance and language", "ja"));
        assertEquals("現在のバージョン：0.2", NativeText.translate("Current version: {version}", "ja", "version", "0.2"));
    }

    @Test public void languageListMatchesWebAndSupportsKeyboardSelectionAndDisabledState() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        AtomicReference<String> chosen = new AtomicReference<>();
        AtomicInteger changes = new AtomicInteger(), closed = new AtomicInteger();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeSettings.Content content = new NativeSettings.Content(ui, "light", "system", "0.2", (theme, language) -> { chosen.set(language); changes.incrementAndGet(); }, closed::incrementAndGet);
            controller.get().setContentView(content);
            layout(content, 322);
            NativeSelect select = content.appearance.language;
            select.performClick();
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            assertNotNull(ui.activePopover);
            assertTrue(ui.activePopover.isShowing());
            assertEquals(4, ui.activePopover.content.getChildCount());
            assertEquals("Follow system", ui.activePopover.content.getChildAt(0).getContentDescription());
            assertEquals("简体中文", ui.activePopover.content.getChildAt(1).getContentDescription());
            assertEquals("English", ui.activePopover.content.getChildAt(2).getContentDescription());
            assertEquals("日本語", ui.activePopover.content.getChildAt(3).getContentDescription());
            View initial = ui.activePopover.content.getChildAt(0);
            assertTrue(initial.hasFocus());
            initial.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_UP));
            assertTrue(ui.activePopover.content.getChildAt(3).hasFocus());
            ui.activePopover.content.getChildAt(3).performClick();
            assertEquals("ja", chosen.get());
            assertEquals(1, changes.get());
            assertNull(ui.activePopover);
            select.performClick();
            ui.activePopover.content.getChildAt(0).performClick();
            assertEquals(1, changes.get());
            select.performClick();
            View current = ui.activePopover.content.getChildAt(0);
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            android.view.KeyCharacterMap keyboard = android.view.KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD);
            KeyEvent[] events = keyboard.getEvents(new char[] {'e'});
            assertNotNull(events);
            for (KeyEvent event : events) current.dispatchKeyEvent(event);
            assertTrue(ui.activePopover.content.getChildAt(2).hasFocus());
            ui.activePopover.content.getChildAt(2).dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ESCAPE));
            assertNull(ui.activePopover);
            content.appearance.saving(true);
            assertFalse(select.isEnabled());
            select.open(false);
            assertNull(ui.activePopover);
            for (int i = 0; i < 3; i++) assertFalse(content.appearance.themes.getChildAt(i).isEnabled());
            content.appearance.saving(false);
            assertTrue(select.isEnabled());
            content.close.performClick();
            assertEquals(1, closed.get());
        }
    }

    @Test public void everyInterfaceLocaleOffersOnlyChineseEnglishJapaneseAndFollowSystem() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (String locale : new String[] {"zh-CN", "en", "ja"}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                AtomicReference<String> chosen = new AtomicReference<>();
                NativeAppearanceSection appearance = new NativeAppearanceSection(ui, "system", locale,
                        (theme, language) -> chosen.set(language));
                controller.get().setContentView(appearance);
                layout(appearance, 322);
                appearance.language.performClick();
                org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
                assertNotNull(ui.activePopover);
                String[] labels = {ui.t("Follow system"), "简体中文", "English", "日本語"};
                assertEquals(labels.length, ui.activePopover.content.getChildCount());
                for (int i = 0; i < labels.length; i++)
                    assertEquals(labels[i], ui.activePopover.content.getChildAt(i).getContentDescription());
                ui.activePopover.content.getChildAt(0).performClick();
                assertEquals("system", chosen.get());
                assertNull(ui.activePopover);
            }
        }
        assertEquals("en", NativePreferences.language("system", new String[] {"fr-FR", "de-DE"}));
        assertEquals("ja", NativePreferences.language("system", new String[] {"fr-FR", "ja-JP", "en-US"}));
        assertEquals("zh-CN", NativePreferences.language("system", new String[] {"zh_Hant_TW", "en-US"}));
    }

    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(1000, View.MeasureSpec.AT_MOST));
        view.layout(0, 0, width, view.getMeasuredHeight());
    }

    @Test public void tsnetExtensionControlsAreBoundedAndSwitchBetweenConnectDisconnectAndEnrollment() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        AtomicReference<String> connected = new AtomicReference<>();
        AtomicInteger disconnected = new AtomicInteger(), enrolled = new AtomicInteger(), closed = new AtomicInteger();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeSettings dialog = new NativeSettings(ui, "system", "system", "0.2.0-native", (theme, language) -> {}, closed::incrementAndGet);
            NativeConnectionSettings settings = new NativeConnectionSettings(ui, "https://spool.example.ts.net", "Disposable fixture: no real node or session.", connected::set, disconnected::incrementAndGet, enrolled::incrementAndGet);
            dialog.content.additions.addView(ui.space(22));
            dialog.content.additions.addView(settings);
            settings.bind(false, false, false, "Tailnet: Disconnected\nNode path: Waiting for node probe\nHTTPS: Not checked");
            assertTrue(settings.server.isEnabled());
            assertEquals("Connect", settings.action.getText().toString());
            settings.action.performClick();
            assertEquals("https://spool.example.ts.net", connected.get());
            settings.login.performClick();
            assertEquals(0, enrolled.get());
            settings.bind(false, true, true, "Tailnet: Sign-in required");
            assertFalse(settings.server.isEnabled());
            assertEquals("Disconnect", settings.action.getText().toString());
            settings.action.performClick();
            assertEquals(1, disconnected.get());
            settings.login.performClick();
            assertEquals(1, enrolled.get());
            dialog.show();
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            assertEquals(android.view.WindowManager.LayoutParams.MATCH_PARENT, dialog.getWindow().getAttributes().width);
            assertEquals(7f / 15, dialog.getWindow().getAttributes().dimAmount, .001);
            View frame = dialog.modal;
            frame.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.AT_MOST));
            frame.layout(0, 0, 360, frame.getMeasuredHeight());
            View shell = dialog.modal.surface;
            assertEquals(322, shell.getWidth());
            assertTrue(shell.getHeight() <= 760);
            File directory = new File(System.getProperty("spool.ui.artifacts"));
            assertTrue(directory.isDirectory() || directory.mkdirs());
            Bitmap bitmap = Bitmap.createBitmap(shell.getWidth(), shell.getHeight(), Bitmap.Config.ARGB_8888);
            shell.draw(new Canvas(bitmap));
            try (FileOutputStream output = new FileOutputStream(new File(directory, "settings-tsnet-360-light.png"))) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
            bitmap.recycle();
            dialog.cancel();
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            assertEquals(1, closed.get());
            assertFalse(dialog.isShowing());
        }
    }

    private JSONObject box(View view, View root) throws Exception {
        int x = 0, y = 0;
        for (View current = view; current != root; current = (View) current.getParent()) { x += current.getLeft(); y += current.getTop(); }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }

    @Test public void appearanceRendersAtAllMobileWidthsInBothThemes() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320,360,412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false,true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    NativeSettings.Content content = new NativeSettings.Content(ui, dark ? "dark" : "light", "system", "0.2.0-native", (theme, language) -> {}, () -> {});
                    int dialogWidth = Math.min(430, width - 38);
                    layout(content, dialogWidth);
                    Bitmap bitmap = Bitmap.createBitmap(dialogWidth, content.getHeight(), Bitmap.Config.ARGB_8888);
                    content.draw(new Canvas(bitmap));
                    String suffix = width + (dark ? "-dark" : "-light");
                    try (FileOutputStream output = new FileOutputStream(new File(directory, "appearance-" + suffix + ".png"))) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                    bitmap.recycle();
                    JSONArray themes = new JSONArray();
                    for (int i = 0; i < 3; i++) themes.put(box(content.appearance.themes.getChildAt(i), content));
                    JSONObject geometry = new JSONObject().put("panel", box(content, content)).put("heading", box(content.heading, content))
                            .put("appearance", box(content.appearance, content)).put("themes", themes).put("language", box(content.appearance.language, content)).put("version", box(content.version, content));
                    java.nio.file.Files.writeString(new File(directory, "native-appearance-" + suffix + ".json").toPath(), geometry.toString(2));
                }
            }
        }
    }
}
