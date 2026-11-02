package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Rect;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
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

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativePermissionTest {
    private JSONArray cases() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("permissionCases");
        }
    }
    private JSONObject catalog(JSONObject item) throws Exception {
        return item.has("modes") ? new JSONObject().put("modes", item.getJSONArray("modes")) : null;
    }
    private View tagged(View root, String tag) { return root.findViewWithTag(tag); }
    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        view.layout(0, 0, view.getMeasuredWidth(), view.getMeasuredHeight());
    }
    private JSONObject box(ViewGroup root, View view) throws Exception {
        Rect rect = new Rect(0, 0, view.getWidth(), view.getHeight()); root.offsetDescendantRectToMyCoords(view, rect);
        return new JSONObject().put("x", rect.left).put("y", rect.top).put("width", rect.width()).put("height", rect.height());
    }
    private JSONObject text(ViewGroup root, TextView view) throws Exception {
        int color = view.getCurrentTextColor();
        return box(root, view).put("text", view.getText().toString())
                .put("color", "rgb(" + Color.red(color) + ", " + Color.green(color) + ", " + Color.blue(color) + ")");
    }
    private NativePermissionMenu menu(NativeUi ui, JSONObject item, java.util.function.Consumer<String> choose, Runnable retry) throws Exception {
        return new NativePermissionMenu(ui, item.getString("backend"), item.getString("selected"), catalog(item),
                item.optBoolean("loading"), item.optBoolean("failed"), choose, retry);
    }

    @Test public void actualPermissionBodiesMatchWebAcrossWidthsThemesStatesAndThreeLocales() throws Exception {
        renderBodies("native");
    }

    @Test @Config(sdk = 26) public void legacyPermissionBodiesHaveSeparateInspectableEvidence() throws Exception {
        renderBodies("native-api26");
    }

    private void renderBodies(String prefix) throws Exception {
        JSONArray fixtures = cases();
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    JSONArray metrics = new JSONArray();
                    android.widget.LinearLayout page = new android.widget.LinearLayout(controller.get());
                    page.setOrientation(android.widget.LinearLayout.VERTICAL); page.setPadding(24, 24, 24, 0);
                    for (int i = 0; i < fixtures.length(); i++) {
                        JSONObject item = fixtures.getJSONObject(i);
                        NativeUi ui = new NativeUi(controller.get(), dark, item.optString("locale", "en"));
                        ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                        page.setBackgroundColor(ui.elevated);
                        NativePermissionMenu menu = menu(ui, item, ignored -> {}, () -> {});
                        if (item.optBoolean("confirm")) tagged(menu, "option:bypassPermissions").performClick();
                        ViewGroup panel = (ViewGroup) menu.popover.panel();
                        layout(panel, Math.min(320, width - 48));
                        JSONObject result = new JSONObject().put("id", item.getString("id")).put("panel", box(panel, panel))
                                .put("heading", box(panel, tagged(menu, "heading")));
                        if (item.optBoolean("loading")) {
                            TextView status = (TextView) tagged(menu, "loading");
                            result.put("status", box(panel, status)).put("statusText", status.getText().toString());
                        }
                        if (item.optBoolean("failed")) {
                            ViewGroup status = (ViewGroup) tagged(menu, "failed");
                            result.put("status", box(panel, status)).put("statusText", ((TextView) status.getChildAt(0)).getText().toString())
                                    .put("retry", text(panel, (TextView) tagged(menu, "retry")));
                        }
                        JSONArray options = new JSONArray();
                        for (int j = 0; j < menu.flow.getChildCount(); j++) {
                            View view = menu.flow.getChildAt(j);
                            if (!(view instanceof NativePermissionMenu.Option)) continue;
                            NativePermissionMenu.Option row = (NativePermissionMenu.Option) view;
                            JSONObject option = new JSONObject().put("box", box(panel, row)).put("title", text(panel, row.title))
                                    .put("detail", text(panel, row.detail)).put("selected", String.valueOf(row.isSelected()))
                                    .put("enabled", String.valueOf(row.isEnabled())).put("checks", row.check == null ? 0 : 1);
                            if (row.check != null) {
                                int color = row.check.getImageTintList().getDefaultColor();
                                option.put("check", box(panel, row.check).put("color", "rgb(" + Color.red(color) + ", " + Color.green(color) + ", " + Color.blue(color) + ")"));
                            }
                            options.put(option);
                        }
                        result.put("options", options);
                        ViewGroup confirmation = (ViewGroup) tagged(menu, "confirmation");
                        if (confirmation != null) {
                            result.put("confirmation", box(panel, confirmation));
                            result.put("warning", text(panel, (TextView) tagged(menu, "warning")));
                            result.put("actions", new JSONArray().put(text(panel, (TextView) tagged(menu, "cancel"))).put(text(panel, (TextView) tagged(menu, "enable"))));
                        }
                        ViewGroup scope = (ViewGroup) tagged(menu, "scope");
                        if (scope != null) result.put("scope", text(panel, (TextView) scope.getChildAt(0)));
                        metrics.put(result);
                        // The test page mounts the production surface, not a hand-styled copy.
                        ((ViewGroup) panel.getParent()).removeView(panel);
                        android.widget.LinearLayout.LayoutParams params = new android.widget.LinearLayout.LayoutParams(Math.min(320, width - 48), panel.getMeasuredHeight());
                        params.bottomMargin = 24; page.addView(panel, params);
                    }
                    controller.get().setContentView(page); layout(page, width);
                    Bitmap image = Bitmap.createBitmap(width, page.getHeight(), Bitmap.Config.ARGB_8888); page.draw(new Canvas(image));
                    String suffix = width + (dark ? "-dark" : "-light");
                    try (FileOutputStream stream = new FileOutputStream(new File(directory, prefix + "-permissions-" + suffix + ".png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                    }
                    image.recycle();
                    Files.writeString(new File(directory, prefix + "-permissions-" + suffix + ".json").toPath(), metrics.toString(2) + "\n");
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void bypassRequiresOneExplicitConfirmationAndCanBeCancelledOrDismissed() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            AtomicReference<String> chosen = new AtomicReference<>();
            JSONObject data = cases().getJSONObject(0);
            NativePermissionMenu menu = menu(ui, data, chosen::set, () -> {});
            FrameLayout page = new FrameLayout(ui.context); View anchor = ui.button("Permissions", false, () -> {}); anchor.setFocusableInTouchMode(true);
            FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(80, 32); params.leftMargin = 68; params.topMargin = 700;
            page.addView(anchor, params); controller.get().setContentView(page);
            page.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY)); page.layout(0, 0, 360, 800);
            menu.show(anchor); Shadows.shadowOf(Looper.getMainLooper()).idle();
            tagged(menu, "option:dontAsk").performClick(); assertNull(chosen.get());
            tagged(menu, "option:bypassPermissions").performClick(); assertNull(chosen.get());
            int count = menu.flow.getChildCount();
            tagged(menu, "option:bypassPermissions").performClick(); assertEquals(count, menu.flow.getChildCount());
            tagged(menu, "cancel").performClick(); assertNull(tagged(menu, "confirmation")); assertNull(chosen.get());
            tagged(menu, "option:bypassPermissions").performClick();
            menu.bind("ask", null, true, false);
            tagged(menu, "enable").performClick(); assertNull("An outstanding discovery cannot enable bypass", chosen.get());
            menu.bind("ask", catalog(data), false, false);
            View frame = menu.popover.panel().getParent() instanceof View ? (View) menu.popover.panel().getParent() : null;
            assertNotNull(frame); assertTrue(frame.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_ESCAPE)));
            assertFalse(menu.popover.isShowing()); assertTrue(anchor.hasFocus());
            menu.show(anchor); assertNull(tagged(menu, "confirmation"));
            tagged(menu, "option:bypassPermissions").performClick(); tagged(menu, "enable").performClick();
            assertEquals("bypassPermissions", chosen.get()); assertFalse(menu.popover.isShowing());
        }
    }

    @Test @Config(sdk = {26, 35}) public void discoveryStatesRetrySelectionReasonsAndBackendLabelsRemainNative() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (String locale : new String[] {"en", "zh-CN", "ja"}) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                AtomicInteger retries = new AtomicInteger(); AtomicReference<String> chosen = new AtomicReference<>();
                NativePermissionMenu menu = new NativePermissionMenu(ui, "claude", "plan", null, true, false, chosen::set, retries::incrementAndGet);
                assertNotNull(tagged(menu, "loading")); assertNull(tagged(menu, "retry")); assertNull(tagged(menu, "scope"));
                menu.bind("plan", null, false, true); assertNull(tagged(menu, "loading"));
                tagged(menu, "retry").performClick(); assertEquals(1, retries.get());
                JSONObject data = cases().getJSONObject(1); menu.bind("plan", catalog(data), false, false);
                assertNull(tagged(menu, "failed")); assertNull(tagged(menu, "retry"));
                tagged(menu, "option:plan").performClick(); assertEquals("plan", chosen.get());
                assertEquals(ui.t("Auto"), NativePermissionMenu.label(ui, "claude", "auto"));
                assertEquals(ui.t("Approve for me"), NativePermissionMenu.label(ui, "codex", "auto"));
                NativePermissionMenu.Option selected = (NativePermissionMenu.Option) tagged(menu, "option:plan");
                android.view.accessibility.AccessibilityNodeInfo info = selected.createAccessibilityNodeInfo();
                assertTrue(info.isCheckable()); assertTrue(info.isChecked()); assertEquals("android.widget.Button", info.getClassName().toString());
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void permissionAnchorPlacementHeightLimitDangerInkAndKeyboardActionsMatchCss() throws Exception {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeUi.ChoiceButton anchor = ui.new ChoiceButton(true, () -> {});
                anchor.bind(ui.t("Bypass permissions"), "shield-check", ui.t("Permissions"));
                anchor.setDanger(true); anchor.setFocusableInTouchMode(true);
                assertEquals(ui.primary, anchor.label.getCurrentTextColor());
                FrameLayout page = new FrameLayout(ui.context);
                FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(ui.dp(58), ui.dp(32));
                params.leftMargin = ui.dp(68); params.topMargin = ui.dp(700); page.addView(anchor, params);
                controller.get().setContentView(page);
                page.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(360), View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(ui.dp(800), View.MeasureSpec.EXACTLY));
                page.layout(0, 0, ui.dp(360), ui.dp(800));
                JSONObject catalog = new JSONObject().put("modes", new JSONArray().put(new JSONObject()
                        .put("id", "ask").put("available", true).put("reason", "A long permission reason. ".repeat(80))));
                AtomicReference<String> chosen = new AtomicReference<>();
                NativePermissionMenu menu = new NativePermissionMenu(ui, "codex", "ask", catalog, false, false, chosen::set, () -> {});
                menu.show(anchor); Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertTrue(anchor.isActivated());
                NativePopupFrame frame = (NativePopupFrame) menu.popover.panel().getParent();
                Rect viewport = new Rect(); anchor.getWindowVisibleDisplayFrame(viewport);
                int[] position = new int[2]; anchor.getLocationOnScreen(position);
                assertEquals(position[0] - ui.dp(35), frame.window.left + frame.panel.left);
                assertEquals(position[1] - frame.panel.height() - ui.dp(12), frame.window.top + frame.panel.top);
                assertEquals(Math.min(ui.dp(580), Math.round(viewport.height() * .65f)), frame.panel.height());
                View option = tagged(menu, "option:ask"); assertTrue(option.requestFocus());
                assertTrue(option.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_SPACE)));
                assertNull(chosen.get());
                assertTrue(option.dispatchKeyEvent(new KeyEvent(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_SPACE)));
                assertEquals("ask", chosen.get()); assertFalse(menu.popover.isShowing()); assertFalse(anchor.isActivated());
                anchor.setDanger(false); assertEquals(ui.muted, anchor.label.getCurrentTextColor());
            }
        }
    }
}
