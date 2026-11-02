package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.text.Spanned;
import android.text.style.ClickableSpan;
import android.text.style.StyleSpan;
import android.view.KeyEvent;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.LinkedHashMap;
import java.util.Map;
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

/** Golden models are evaluated from the actual Web TypeScript, not a handwritten expected implementation. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeBehaviorTest {
    private JSONObject fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull("Run sync-web-behavior.mjs", input);
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    private List<JSONObject> parts(JSONArray array) throws Exception {
        List<JSONObject> values = new ArrayList<>();
        for (int i = 0; i < array.length(); i++) values.add(array.getJSONObject(i));
        return values;
    }

    private void selection(JSONObject expected, NativeModelSelection actual) throws Exception {
        assertEquals(expected.getString("backend"), actual.backend);
        assertEquals(expected.getString("model"), actual.model);
        assertEquals(expected.getString("effort"), actual.effort);
    }

    @Test public void nativeModelDefaultsAndClaudeResetMatchActualWebVectors() throws Exception {
        JSONObject fixtures = fixtures();
        JSONArray models = fixtures.getJSONArray("models");
        for (int i = 0; i < models.length(); i++) {
            JSONObject test = models.getJSONObject(i), agent = test.getJSONObject("agent");
            String id = test.getString("model");
            selection(test.getJSONObject("expected"), NativeModelSelection.choose(agent, id));
            List<String> levels = NativeModelSelection.levels(agent.getString("backend"), NativeModelSelection.model(agent, id));
            JSONArray expected = test.getJSONArray("levels");
            assertEquals(expected.length(), levels.size());
            for (int j = 0; j < expected.length(); j++) assertEquals(expected.getString(j), levels.get(j));
        }
        JSONArray chats = fixtures.getJSONArray("chats");
        for (int i = 0; i < chats.length(); i++) {
            JSONObject test = chats.getJSONObject(i);
            selection(test.getJSONObject("expected"), NativeModelSelection.chat(test.getJSONArray("agents"), test.getJSONObject("chat")));
        }
    }

    @Test public void activityClassificationPairingLabelsAndStatsMatchActualWebInBothLanguages() throws Exception {
        JSONArray cases = fixtures().getJSONArray("activities");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            for (int i = 0; i < cases.length(); i++) {
                JSONObject test = cases.getJSONObject(i);
                NativeUi ui = new NativeUi(controller.get(), false, test.getString("locale").equals("zh-CN"));
                List<NativeActivityModel.Activity> actual = NativeActivityModel.fromParts(ui, parts(test.getJSONArray("parts")), test.getString("backend"));
                JSONArray expected = test.getJSONArray("expected");
                assertEquals(test.getString("name"), expected.length(), actual.size());
                for (int j = 0; j < expected.length(); j++) {
                    JSONObject item = expected.getJSONObject(j);
                    NativeActivityModel.Activity value = actual.get(j);
                    assertEquals(item.getString("kind"), value.kind);
                    assertEquals(item.getString("label"), value.label);
                    assertEquals(item.getString("detail"), value.detail);
                    assertEquals(item.optString("stats"), value.stats == null ? "" : value.stats);
                    assertEquals(item.optJSONObject("use") == null ? null : item.getJSONObject("use").toString(), value.use == null ? null : value.use.toString());
                    assertEquals(item.optJSONObject("result") == null ? null : item.getJSONObject("result").toString(), value.result == null ? null : value.result.toString());
                    assertEquals(item.getString("changeStats"), NativeActivityModel.changeStats(java.util.Collections.singletonList(value)));
                }
                assertEquals(test.getString("stats"), NativeActivityModel.changeStats(actual));
            }
        }
    }

    private View label(View view, String value) {
        if (value.equals(view.getContentDescription()) || view instanceof TextView && value.equals(((TextView) view).getText().toString())) return view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            View child = label(((ViewGroup) view).getChildAt(i), value);
            if (child != null) return child;
        }
        return null;
    }

    private <T> T first(View view, Class<T> type) {
        if (type.isInstance(view)) return type.cast(view);
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) {
            T found = first(((ViewGroup) view).getChildAt(i), type);
            if (found != null) return found;
        }
        return null;
    }

    private void draw(View view, int width, int maximum, String name) throws Exception {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(maximum, View.MeasureSpec.AT_MOST));
        view.layout(0, 0, width, view.getMeasuredHeight());
        Bitmap image = Bitmap.createBitmap(width, view.getHeight(), Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(image));
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output)); }
        image.recycle();
    }

    private void childrenFit(ViewGroup parent) {
        for (int i = 0; i < parent.getChildCount(); i++) {
            View view = parent.getChildAt(i);
            if (view.getVisibility() == View.GONE) continue;
            assertTrue(view.getClass().getSimpleName() + " overflows right edge", view.getRight() <= parent.getWidth());
            assertTrue(view.getLeft() >= 0);
            if (view instanceof ViewGroup && !(view instanceof ScrollView) && !(view instanceof android.widget.HorizontalScrollView)) childrenFit((ViewGroup) view);
        }
    }

    private void metrics(View root, Map<String, View> targets, String name) throws Exception {
        JSONObject output = new JSONObject();
        for (Map.Entry<String, View> entry : targets.entrySet()) {
            View view = entry.getValue();
            int x = 0, y = 0;
            for (View current = view; current != root; current = (View) current.getParent()) {
                x += current.getLeft();
                y += current.getTop();
            }
            JSONObject box = new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
            output.put(entry.getKey(), box);
        }
        File target = new File(System.getProperty("spool.ui.artifacts"), name + ".json");
        java.nio.file.Files.writeString(target.toPath(), output.toString(2));
    }

    @Test public void modelMenuSwitchesAgentsAndOffersAnExplicitClaudeDefaultWithoutClosing() throws Exception {
        JSONObject fixtures = fixtures();
        JSONArray agents = fixtures.getJSONArray("chats").getJSONObject(0).getJSONArray("agents");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            AtomicReference<NativeModelSelection> changed = new AtomicReference<>();
            NativeAgentModelMenu menu = new NativeAgentModelMenu(ui, agents, new NativeModelSelection("codex", "gpt-6.1-codex", "high"), "codex", changed::set);
            assertNotNull(label(menu, "Claude Sonnet"));
            label(menu, "Claude Sonnet").performClick();
            assertEquals("claude", changed.get().backend);
            assertEquals("", changed.get().effort);
            NativeReasoningSlider slider = first(menu, NativeReasoningSlider.class);
            assertNotNull(slider);
            assertEquals(2, slider.getMax());
            assertTrue(slider.onKeyDown(KeyEvent.KEYCODE_DPAD_RIGHT, new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_RIGHT)));
            assertEquals("low", changed.get().effort);
            assertNotNull(label(menu, "The next message creates a new chat with the selected agent and converted context."));
            label(menu, "Claude Fixed").performClick();
            assertEquals("", changed.get().effort);
            assertNull(first(menu, NativeReasoningSlider.class));
            menu.bind(new NativeModelSelection("claude", "imported-custom", ""));
            assertNotNull(label(menu, "imported-custom"));
        }
    }

    @Test public void activityStateKeepsUserTogglesAcrossSnapshotsAndCollapsesWhenTheFinalStarts() throws Exception {
        JSONObject test = fixtures().getJSONArray("activities").getJSONObject(0);
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            List<NativeActivityModel.Activity> activities = NativeActivityModel.fromParts(ui, parts(test.getJSONArray("parts")), "codex");
            NativeActivityList.State state = new NativeActivityList.State();
            NativeActivityList first = new NativeActivityList(ui, activities, "Working for 5s", true, state, ignored -> {});
            assertTrue(state.open);
            assertEquals(5, state.groups.size());
            label(first, "Read web/src/style.css").performClick();
            assertTrue(state.items.contains("1:0"));
            assertNotNull(label(first, "The shared mobile styles."));
            label(first, "Working for 5s").performClick();
            assertFalse(state.open);
            NativeActivityList next = new NativeActivityList(ui, activities, "Working for 6s", true, state, ignored -> {});
            assertFalse(state.open);
            label(next, "Working for 6s").performClick();
            assertNotNull(label(next, "The shared mobile styles."));
            new NativeActivityList(ui, activities, "Worked for 6s", false, state, ignored -> {});
            assertFalse(state.open);
            assertTrue(state.groups.isEmpty());
            assertTrue(state.items.contains("1:0"));
        }
    }

    @Test public void popoverResizesInPlaceWhenTheChosenModelHasNoReasoningSlider() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        JSONArray agents = fixtures().getJSONArray("chats").getJSONObject(0).getJSONArray("agents");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            Activity activity = controller.get();
            NativeUi ui = new NativeUi(activity, false);
            FrameLayout page = new FrameLayout(activity);
            View anchor = ui.button("Model", false, () -> {});
            page.addView(anchor, new FrameLayout.LayoutParams(80, 32, android.view.Gravity.BOTTOM | android.view.Gravity.END));
            activity.setContentView(page);
            page.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
            page.layout(0, 0, 360, 800);
            NativePopover popover = NativePopover.model(ui);
            NativeAgentModelMenu menu = new NativeAgentModelMenu(ui, agents, new NativeModelSelection("codex", "gpt-6.1-codex", "high"), "codex", ignored -> {});
            popover.content.addView(menu);
            popover.show(anchor, true);
            android.widget.PopupWindow window = org.robolectric.util.ReflectionHelpers.getField(popover, "window");
            assertTrue(popover.isShowing());
            int before = window.getHeight();
            label(menu, "Claude Fixed").performClick();
            assertNull(first(menu, NativeReasoningSlider.class));
            popover.content.measure(View.MeasureSpec.makeMeasureSpec(290, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            int contentHeight = popover.content.getMeasuredHeight();
            popover.content.layout(0, 0, 290, popover.content.getMeasuredHeight());
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertTrue("Content must shrink without dismiss/reopening: before=" + before + ", after=" + window.getHeight()
                    + ", content=" + contentHeight + "/" + popover.content.getMeasuredHeight() + ", menu=" + menu.getMeasuredHeight(), window.getHeight() < before);
            assertTrue(popover.isShowing());
            assertSame(popover, ui.activePopover);
            int smaller = window.getHeight();
            label(menu, "GPT-6.1 Codex").performClick();
            popover.content.measure(View.MeasureSpec.makeMeasureSpec(290, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            popover.content.layout(0, 0, 290, popover.content.getMeasuredHeight());
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertTrue("Menu must also grow in place", window.getHeight() > smaller);
            popover.dismiss();
            assertNull(ui.activePopover);
        }
    }

    @Test public void thinkingPreviewParsesFullMarkdownWithoutLinksAndDoesNotLoseCodeBlocks() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            CharSequence value = NativeMarkdown.preview(ui, "## Heading\n\n**Strong** [link](https://example.com)\n\n```java\nNativeUi ui;\n```\n\n- Step");
            assertTrue(value instanceof Spanned);
            assertFalse(value.toString().contains("**"));
            assertTrue(value.toString().contains("Heading"));
            assertTrue(value.toString().contains("NativeUi ui;"));
            Spanned text = (Spanned) value;
            assertEquals(0, text.getSpans(0, text.length(), ClickableSpan.class).length);
            assertTrue(text.getSpans(0, text.length(), StyleSpan.class).length >= 2);
            JSONObject turn = new JSONObject("{status:'running',started_at:1000,backend:'codex',model:'gpt-6.1',reasoning_effort:'high'}");
            assertEquals("Working for 5s · Codex · gpt-6.1 · High", NativeTurn.summary(ui, turn, false, 6000).toString());
            assertEquals("Worked for 5s · Codex · gpt-6.1 · High", NativeTurn.summary(ui, turn, true, 6000).toString());
            turn.put("compacting", true);
            assertEquals("Compacting context… · Codex · gpt-6.1 · High", NativeTurn.summary(ui, turn, false, 6000).toString());
            turn.put("status", "completed").put("finished_at", 6000);
            assertEquals("Worked for 5s · Codex · gpt-6.1 · High", NativeTurn.summary(ui, turn, false, 9000).toString());
            assertEquals("Completed", NativeTurn.summary(ui, new JSONObject("{status:'completed'}"), false, 6000).toString());
        }
    }

    @Test public void modelAndActivityComponentsRenderAtEveryMobileWidthAndTheme() throws Exception {
        JSONObject fixtures = fixtures();
        JSONArray agents = fixtures.getJSONArray("chats").getJSONObject(0).getJSONArray("agents");
        List<JSONObject> source = parts(fixtures.getJSONArray("activities").getJSONObject(0).getJSONArray("parts"));
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h1000dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    NativeAgentModelMenu menu = new NativeAgentModelMenu(ui, agents, new NativeModelSelection("codex", "gpt-6.1-codex", "high"), "codex", ignored -> {});
                    FrameLayout panel = new FrameLayout(controller.get());
                    panel.setPadding(1, 1, 1, 1);
                    panel.setBackground(ui.shape(ui.elevated, 17, ui.borderStrong));
                    panel.addView(menu, new FrameLayout.LayoutParams(-1, -2));
                    draw(panel, Math.min(290, width - 46), 540, "model-menu-" + width + (dark ? "-dark" : "-light"));
                    assertTrue(menu.catalog.getHeight() <= 240);
                    childrenFit(panel);
                    Map<String, View> boxes = new LinkedHashMap<>();
                    boxes.put(".model-popover", panel);
                    boxes.put(".agent-model-groups", menu.catalog);
                    boxes.put(".effort-heading", menu.getChildAt(2));
                    boxes.put(".effort-track", menu.getChildAt(3));
                    boxes.put(".effort-caption", menu.getChildAt(4));
                    boxes.put(".model-popover p:first-of-type", menu.getChildAt(5));
                    boxes.put(".model-popover p:last-of-type", menu.getChildAt(6));
                    metrics(panel, boxes, "native-model-" + width + (dark ? "-dark" : "-light"));
                    LinearLayout page = ui.padded(18, 25);
                    page.setBackgroundColor(ui.elevated);
                    NativeActivityList.State state = new NativeActivityList.State();
                    NativeActivityList list = new NativeActivityList(ui, NativeActivityModel.fromParts(ui, source, "codex"), "Working for 5s", true, state, ignored -> {});
                    page.addView(list);
                    draw(page, width, 1000, "activity-" + width + (dark ? "-dark" : "-light"));
                    childrenFit(page);
                    label(list, "Read web/src/style.css").performClick();
                    draw(page, width, 1000, "activity-detail-" + width + (dark ? "-dark" : "-light"));
                    childrenFit(page);
                    label(list, "Read web/src/style.css").performClick();
                    label(list, "Edited src/mobile/NativeUi.java").performClick();
                    draw(page, width, 1000, "activity-patch-" + width + (dark ? "-dark" : "-light"));
                    childrenFit(page);
                }
            }
        }
    }
}
