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
        assertEquals("バージョン：0.2", NativeText.translate("Current version: {version}", "ja", "version", "0.2"));
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

    @Test public void serverTabsRenderAsSmallRoundedSegmentsOnOneSharedTrack() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark);
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
            profiles.active().name = "Home"; ServerProfiles.Server office = profiles.add("Office");
            NativeServerTabs tabs = new NativeServerTabs(ui, profiles, java.util.Map.of("legacy", "connected", office.id, "connecting"), id -> {}, true, () -> {});
            controller.get().setContentView(tabs); layout(tabs, 360);
            assertSame(tabs.track, tabs.scroll.getParent());
            assertTrue(tabs.tabs.getChildAt(0).isSelected()); assertFalse(tabs.tabs.getChildAt(1).isSelected());
            assertTrue(tabs.tabs.getChildAt(0).getLeft() > 0);
            assertTrue(tabs.tabs.getChildAt(0).getHeight() < tabs.tabs.getHeight());
            android.widget.Button name = (android.widget.Button) tabs.tabs.getChildAt(0);
            assertNotNull(name.getLayout());
            assertTrue("A visible tab must have room to draw its name: width=" + name.getWidth()
                    + ", text=" + name.getText() + ", layout=" + name.getLayout().getWidth(),
                    name.getLayout().getEllipsisCount(0) < name.getText().length());
            File directory = new File(System.getProperty("spool.ui.artifacts"));
            assertTrue(directory.isDirectory() || directory.mkdirs());
            Bitmap bitmap = Bitmap.createBitmap(360, tabs.getHeight(), Bitmap.Config.ARGB_8888);
            tabs.draw(new Canvas(bitmap));
            try (FileOutputStream output = new FileOutputStream(new File(directory, "server-tabs-" + (dark ? "dark" : "light") + ".png"))) { bitmap.compress(Bitmap.CompressFormat.PNG, 100, output); }
            bitmap.recycle();
        }
    }

    @Test public void serverTabFrameKeepsItsRoundedCornersAndBorderWhileTheTabsScroll() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (boolean dark : new boolean[] {false, true}) try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), dark);
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
            for (int i = 1; i <= 10; i++) profiles.add("Server " + i);
            profiles.selected = profiles.entries.get(4).id;
            NativeServerTabs tabs = new NativeServerTabs(ui, profiles, java.util.Map.of(profiles.selected, "connected"), id -> {}, true, () -> {});
            controller.get().setContentView(tabs); layout(tabs, 360);
            int overflow = tabs.tabs.getWidth() - tabs.scroll.getWidth();
            assertTrue(overflow > 0);
            for (int x : new int[] {0, overflow / 2, overflow}) {
                tabs.scroll.scrollTo(x, 0);
                Bitmap bitmap = Bitmap.createBitmap(360, tabs.getHeight(), Bitmap.Config.ARGB_8888);
                try {
                    tabs.draw(new Canvas(bitmap));
                    int left = tabs.getPaddingLeft(), top = tabs.getPaddingTop();
                    assertEquals("The frame's corner stays rounded at every scroll position", ui.sidebar, bitmap.getPixel(left, top));
                    assertEquals("The left frame border must stay fixed above scrolling content", ui.line, bitmap.getPixel(left, top + tabs.track.getHeight() / 2));
                    assertEquals("The right frame border must stay fixed above scrolling content", ui.line, bitmap.getPixel(360 - tabs.getPaddingRight() - 1, top + tabs.track.getHeight() / 2));
                    assertEquals(0, tabs.track.getScrollX());
                    if (x == overflow / 2) {
                        File directory = new File(System.getProperty("spool.ui.artifacts"));
                        assertTrue(directory.isDirectory() || directory.mkdirs());
                        try (FileOutputStream output = new FileOutputStream(new File(directory, "server-tabs-scrolled-" + (dark ? "dark" : "light") + ".png"))) { bitmap.compress(Bitmap.CompressFormat.PNG, 100, output); }
                    }
                } finally { bitmap.recycle(); }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void bothConnectionsKeepTheirAddressesAndRememberTheClickedEntry() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        AtomicReference<ConnectionPlan> saved = new AtomicReference<>();
        AtomicReference<ConnectionMode> selected = new AtomicReference<>();
        AtomicInteger stopped = new AtomicInteger(), enrolled = new AtomicInteger();
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "zh-CN");
            NativeSettings dialog = new NativeSettings(ui, "system", "zh-CN", "test", (a, b) -> {}, () -> {}, false);
            ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("", "", ConnectionMode.DIRECT));
            profiles.active().name = "Home";
            dialog.content.additions.addView(new NativeServerSettings(ui, profiles.active(), enabled -> {}, () -> {}));
            dialog.content.additions.addView(ui.space(16));
            NativeConnectionSettings settings = new NativeConnectionSettings(ui,
                    new ConnectionPlan("https://chat.example.com", "https://spool.example.ts.net", ConnectionMode.DIRECT, true),
                    saved::set, (plan, mode) -> { saved.set(plan); selected.set(mode); }, stopped::incrementAndGet, enrolled::incrementAndGet);
            dialog.content.additions.addView(settings);
            dialog.content.additions.addView(ui.space(24));
            android.widget.Button delete = ui.button(ui.t("Delete server"), false, () -> {}); delete.setTextColor(ui.error);
            dialog.content.additions.addView(delete, new android.widget.LinearLayout.LayoutParams(-1, -2));
            settings.bind(ConnectionMode.DIRECT, true, true, false, true, java.util.Map.of(), "HTTPS: Verified");
            dialog.show(); org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            layout(dialog.modal, 360);
            assertEquals(3, settings.entries.getChildCount());
            assertNotNull(settings.entries.getBackground());
            assertNull(settings.rows.get(ConnectionMode.DIRECT).getBackground());
            assertNull(settings.rows.get(ConnectionMode.TAILSCALE).getBackground());
            assertSame(settings.rows.get(ConnectionMode.TAILSCALE), settings.entries.getChildAt(2));
            assertTrue(settings.automatic.isChecked());
            assertTrue(settings.input(ConnectionMode.TAILSCALE).isEnabled());
            assertEquals("公网 HTTPS", settings.rows.get(ConnectionMode.DIRECT).title.getText().toString());
            assertEquals("Tailscale (tsnet)", settings.rows.get(ConnectionMode.TAILSCALE).title.getText().toString());
            assertSame(settings.rows.get(ConnectionMode.DIRECT), settings.entries.getChildAt(0));
            assertEquals("https://chat.example.com", settings.plan().direct);
            assertEquals("https://spool.example.ts.net", settings.plan().tailscale);
            settings.automatic.performClick(); assertFalse(settings.plan().autoSwitch);
            settings.rows.get(ConnectionMode.TAILSCALE).use.performClick();
            assertSame(ConnectionMode.TAILSCALE, selected.get());
            assertSame(ConnectionMode.TAILSCALE, settings.plan().selected);
            assertSame(ConnectionMode.TAILSCALE, saved.get().selected);
            assertSame("Choosing a network does not reorder the entries", settings.rows.get(ConnectionMode.DIRECT), settings.entries.getChildAt(0));
            assertEquals("断开连接", settings.rows.get(ConnectionMode.DIRECT).use.getContentDescription());
            assertTrue(settings.rows.get(ConnectionMode.DIRECT).use.getText() instanceof android.text.Spanned);
            NativeServerTabs.Dot dot = ((android.text.Spanned) settings.rows.get(ConnectionMode.DIRECT).use.getText()).getSpans(0, 1, NativeServerTabs.Dot.class)[0];
            assertEquals(NativeServerTabs.statusColor(ui, "connected"), dot.color);
            assertEquals("连接", settings.rows.get(ConnectionMode.TAILSCALE).use.getText().toString());
            settings.rows.get(ConnectionMode.DIRECT).use.performClick(); assertEquals(1, stopped.get());
            settings.login.performClick(); assertEquals(1, enrolled.get());
            assertSame(settings.rows.get(ConnectionMode.TAILSCALE).title.getParent(), settings.login.getParent());
            assertEquals("登录 Tailscale", settings.login.getContentDescription());
            settings.bind(ConnectionMode.DIRECT, true, true, true, false, java.util.Map.of(), "Checking");
            assertFalse(settings.automatic.isEnabled()); assertFalse(settings.login.isEnabled());
            assertTrue("A pending connection can be disconnected", settings.rows.get(ConnectionMode.DIRECT).use.isEnabled());
            assertFalse(settings.input(ConnectionMode.DIRECT).isEnabled());
            assertFalse(settings.rows.get(ConnectionMode.TAILSCALE).use.isEnabled());
            settings.bind(ConnectionMode.DIRECT, true, true, false, true, java.util.Map.of(), "HTTPS: Verified");
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            layout(dialog.modal, 360);
            File directory = new File(System.getProperty("spool.ui.artifacts"));
            assertTrue(directory.isDirectory() || directory.mkdirs());
            Bitmap bitmap = Bitmap.createBitmap(dialog.modal.surface.getWidth(), dialog.modal.surface.getHeight(), Bitmap.Config.ARGB_8888);
            dialog.modal.surface.draw(new Canvas(bitmap));
            try (FileOutputStream output = new FileOutputStream(new File(directory, "settings-connections-360-light.png"))) { bitmap.compress(Bitmap.CompressFormat.PNG, 100, output); }
            bitmap.recycle(); dialog.dismiss();
        }
    }

    @Test public void singleEntryCanConnectAndDisabledAutomaticSwitchingKeepsTheOtherEntryOptional() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            AtomicReference<ConnectionPlan> selected = new AtomicReference<>();
            NativeConnectionSettings settings = new NativeConnectionSettings(ui,
                    new ConnectionPlan("https://chat.example.com", "", ConnectionMode.DIRECT), plan -> {},
                    (plan, mode) -> selected.set(plan), () -> {}, () -> {});
            settings.bind(ConnectionMode.DIRECT, false, false, false, false, java.util.Map.of(), "");
            assertFalse(settings.login.isEnabled());
            assertEquals("Connect", settings.rows.get(ConnectionMode.DIRECT).use.getText().toString());
            assertFalse(settings.rows.get(ConnectionMode.TAILSCALE).use.isEnabled());
            settings.rows.get(ConnectionMode.DIRECT).use.performClick(); assertEquals(1, selected.get().candidates(null).size());
            assertEquals("https://chat.example.com", selected.get().direct);
        }
    }

    @Test @Config(sdk = {26, 35}) public void explanationsAndLiveTailnetDetailsAppearOnlyInTitleInformationPopups() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            NativeConnectionSettings settings = new NativeConnectionSettings(ui,
                    new ConnectionPlan("https://chat.example.com", "https://spool.example.ts.net", ConnectionMode.TAILSCALE, true),
                    plan -> {}, (plan, mode) -> {}, () -> {}, () -> {});
            controller.get().setContentView(settings);
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle(); layout(settings, 360);
            assertNull(settings.note.getParent()); assertNull(settings.diagnostics.getParent());
            assertNull(settings.rows.get(ConnectionMode.TAILSCALE).status.getParent());
            settings.information.button.performClick();
            assertNotNull(ui.activePopover);
            assertTrue(((android.widget.TextView) ui.activePopover.content.getChildAt(2)).getText().toString().contains("remembers your choice"));
            ui.activePopover.dismiss();
            settings.bind(ConnectionMode.TAILSCALE, true, true, false, true, java.util.Map.of(), "Tailnet: Running\nNode path: DERP\nRound-trip time: 6.5 ms");
            settings.rows.get(ConnectionMode.TAILSCALE).information.button.performClick();
            assertNotNull(ui.activePopover);
            android.widget.TextView text = (android.widget.TextView) ui.activePopover.content.getChildAt(2);
            assertTrue(text.getText().toString().contains("6.5 ms"));
            NativePopover popup = ui.activePopover;
            settings.bind(ConnectionMode.TAILSCALE, true, true, false, true, java.util.Map.of(), "Tailnet: Running\nRound-trip time: 8.0 ms");
            assertSame(popup, ui.activePopover); assertTrue(text.getText().toString().contains("8.0 ms"));
            ui.activePopover.dismiss();
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
