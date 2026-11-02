package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.drawable.Drawable;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.HorizontalScrollView;
import android.widget.TextView;
import android.text.Spanned;
import android.text.style.ClickableSpan;

import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

import java.io.File;
import java.io.FileOutputStream;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONArray;
import org.json.JSONObject;

/** Real native measurement/drawing; synthetic fixtures never connect or load user data. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeComponentsTest {
    private void draw(View view, int width, int height, String name) throws Exception {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        view.layout(0, 0, width, height);
        Bitmap image = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
        view.draw(new Canvas(image));
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) {
            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
        }
        image.recycle();
    }

    private void assertChildrenFit(ViewGroup parent) {
        for (int i = 0; i < parent.getChildCount(); i++) {
            View child = parent.getChildAt(i);
            if (child.getVisibility() == View.GONE) continue;
            assertTrue(child.getClass().getSimpleName() + " exceeds right edge: " + child.getRight()
                    + " > " + parent.getWidth(), child.getRight() <= parent.getWidth());
            assertTrue(child.getClass().getSimpleName() + " exceeds left edge", child.getLeft() >= 0);
            if (child instanceof ViewGroup && !(child instanceof ScrollView) && !(child instanceof HorizontalScrollView))
                assertChildrenFit((ViewGroup) child);
        }
    }

    @Test public void mobileComponentsRenderAtEveryTargetWidthAndTheme() throws Exception {
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class)) {
                    Activity activity = controller.get();
                    activity.setTheme(dark ? R.style.SpoolDarkTheme : R.style.SpoolTheme);
                    controller.setup();
                    NativeUi ui = new NativeUi(activity, dark);
                    String suffix = width + (dark ? "-dark" : "-light");
                    NativeLogin login = new NativeLogin(ui, "", false, () -> {}, () -> {});
                    activity.setContentView(login);
                    draw(login, width, 800, "login-" + suffix);
                    assertChildrenFit(login);
                    assertEquals(0, login.username.getText().length());
                    assertFalse(login.username.getIncludeFontPadding());

                    LinearLayout page = ui.workspace();
                    NativeUi.Header header = ui.new Header("New chat", "Mobile project", () -> {}, () -> {}, () -> {});
                    page.addView(header);
                    page.addView(ui.divider());
                    ScrollView history = new ScrollView(activity);
                    LinearLayout body = ui.padded(18, 25);
                    body.addView(ui.welcome("What shall we build today?",
                            "Describe your idea. Your agent takes it from here.",
                            "Mobile project", "Run locally. Pick up anytime."));
                    history.addView(body);
                    page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
                    NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
                    composer.model.bind("gpt-6.1-codex", "codex", "Choose model");
                    composer.permission.bind("Ask for approval", "shield-question", "Permissions");
                    page.addView(composer);
                    activity.setContentView(page);
                    draw(page, width, 800, "workspace-" + suffix);
                    assertChildrenFit(page);
                    assertEquals(49, header.getHeight());
                    assertEquals(32, composer.send.getWidth());
                    assertEquals(32, composer.send.getHeight());
                    assertEquals(1, composer.input.getMinLines());
                    assertEquals(6, composer.input.getMaxLines());
                    assertFalse(composer.input.getIncludeFontPadding());
                    composer.input.setText("First line\nSecond line\nThird line");
                    draw(page, width, 800, "composer-expanded-" + suffix);
                    assertChildrenFit(page);
                    assertTrue(composer.input.getHeight() > 60);
                    composer.send.setEnabled(false);
                    assertEquals(1f, composer.send.getAlpha(), 0f);
                    composer.permission.setEnabled(false);
                    assertEquals(.45f, composer.permission.getAlpha(), .001f);

                    LinearLayout transcript = ui.padded(18, 25);
                    transcript.setBackgroundColor(ui.elevated);
                    transcript.addView(new NativeMessage(ui, "Build a **native Android** workspace.", true,
                            "Mobile user", 0, false, ignored -> {}));
                    transcript.addView(ui.space(22));
                    transcript.addView(new NativeMessage(ui,
                            "Native components share the same **colors**, spacing, and `vector icons`.", false,
                            null, 0, false, ignored -> {}));
                    draw(transcript, width, 400, "messages-standalone-" + suffix);
                    assertChildrenFit(transcript);

                    NativeDrawer drawer = new NativeDrawer(ui, false, () -> {});
                    drawer.sheet.addView(ui.toolbar("Spool", () -> {}));
                    drawer.sheet.addView(ui.divider());
                    NativeProjectRow project = new NativeProjectRow(ui, () -> {}, () -> {}); project.bind(new JSONObject().put("name", "Mobile project"), true, false);
                    drawer.sheet.addView(project, new LinearLayout.LayoutParams(-1, 31));
                    drawer.sheet.addView(new NativeChatLink(ui, new JSONObject().put("title", "Native workspace").put("backend", "codex"), true, true, "", ignored -> new JSONObject(), () -> {}), new LinearLayout.LayoutParams(-1, 31));
                    drawer.show();
                    org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(250));
                    draw(drawer, width, 800, "drawer-" + suffix);
                    assertEquals(Math.min(298, Math.round(width * .88f)), drawer.sheet.getWidth());
                }
            }
        }
    }

    private void textViews(View view, List<TextView> views) {
        if (view instanceof TextView) views.add((TextView) view);
        if (view instanceof ViewGroup)
            for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++)
                textViews(((ViewGroup) view).getChildAt(i), views);
    }

    @Test public void gfmIsNativeSafeAndUserExpansionClipsTheWholeDocument() throws Exception {
        String markdown = "## Native Markdown\n\n**Strong** and *emphasis*, ~~removed~~ and `inline code`.\n\n"
                + "- [x] Shared vectors\n- [ ] Compare screenshots\n\n> A reusable native component.\n\n"
                + "| Component | Platform |\n| :--- | ---: |\n| Composer | Android |\n\n"
                + "```java\nNativeUi ui = new NativeUi(activity, false);\nString longLine = \"This code block scrolls horizontally without overflowing the message\";\n```\n\n"
                + "[Project file](src/Main.java#L42) and ![Remote image](https://example.com/image.png)\n\n"
                + "<script>window.track()</script>\n\n[Unsafe](javascript:alert%281%29)";
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h1000dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    Activity activity = controller.get();
                    NativeUi ui = new NativeUi(activity, dark);
                    LinearLayout page = ui.padded(18, 25);
                    page.setBackgroundColor(ui.elevated);
                    AtomicInteger opens = new AtomicInteger();
                    page.addView(NativeMarkdown.view(ui, markdown, false, ignored -> opens.incrementAndGet()));
                    draw(page, width, 1000, "markdown-" + width + (dark ? "-dark" : "-light"));
                    assertChildrenFit(page);
                    assertEquals("No image or link may be fetched by layout", 0, opens.get());
                    List<TextView> text = new ArrayList<>();
                    textViews(page, text);
                    int links = 0;
                    for (TextView view : text) {
                        assertFalse(view.getText().toString().contains("window.track"));
                        if (view.getText() instanceof Spanned)
                            links += ((Spanned) view.getText()).getSpans(0, view.length(), ClickableSpan.class).length;
                    }
                    assertEquals(2, links);

                    NativeMessage message = new NativeMessage(ui, markdown, true, "Mobile user", 0, false, ignored -> {});
                    draw(message, width, 1200, "long-user-" + width + (dark ? "-dark" : "-light"));
                    assertEquals(196, message.content.getHeight());
                    assertEquals(View.VISIBLE, message.toggle.getVisibility());
                    message.toggle.performClick();
                    draw(message, width, 1200, "long-user-expanded-" + width + (dark ? "-dark" : "-light"));
                    assertTrue(message.content.getHeight() > 196);
                    assertEquals("Show less", message.toggle.getText().toString());
                    message.toggle.performClick();
                    draw(message, width, 1200, "long-user-" + width + (dark ? "-dark" : "-light"));
                    assertEquals(196, message.content.getHeight());
                }
            }
        }
    }

    @Test public void processAndFinalAnswerAreDistinctNativeComponents() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h1000dp-mdpi");
        Activity activity = Robolectric.buildActivity(Activity.class).setup().get();
        NativeUi ui = new NativeUi(activity, false);
        JSONArray parts = new JSONArray("[{role:'assistant',kind:'text',text:'I will inspect the files.'},"
                + "{role:'assistant',kind:'tool_use',name:'exec_command',text:'rg -n NativeUi'},"
                + "{role:'assistant',kind:'tool_result',name:'exec_command',text:'NativeUi.java'},"
                + "{role:'assistant',kind:'text',phase:'final_answer',text:'## Ready\\n\\nThe **native components** are ready.'}]");
        JSONObject completed = new JSONObject("{status:'completed',started_at:1000,finished_at:4200,backend:'codex'}");
        NativeTurn turn = new NativeTurn(ui, parts, 0, parts.length(), completed, false, ignored -> {});
        LinearLayout page = ui.padded(18, 25);
        page.setBackgroundColor(ui.elevated);
        page.addView(turn);
        draw(page, 360, 600, "turn-completed");
        assertTrue(turn.getChildAt(turn.getChildCount() - 1) instanceof NativeMessage);
        assertEquals("Collapsed Web activity conditionally unmounts its list", 2, turn.activity.getChildCount());
        turn.activity.getChildAt(0).performClick();
        draw(page, 360, 600, "turn-expanded");
        assertEquals(View.VISIBLE, turn.activity.getChildAt(2).getVisibility());
        turn.activity.getChildAt(0).performClick();
        assertEquals(2, turn.activity.getChildCount());
        assertChildrenFit(page);
    }

    @Test public void composerUsesDensityIndependentAvailableWidth() throws Exception {
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-xxhdpi");
            Activity activity = Robolectric.buildActivity(Activity.class).setup().get();
            NativeUi ui = new NativeUi(activity, false, true);
            NativeUi.Composer composer = ui.new Composer("发送消息", () -> {}, () -> {}, () -> {}, () -> {});
            composer.model.bind("a-very-long-model-name-with-reasoning", "codex", "选择模型");
            composer.permission.bind("请求审批", "shield-question", "权限");
            draw(composer, ui.dp(width), ui.dp(150), "composer-density-" + width);
            assertChildrenFit(composer);
            assertEquals("发送消息", composer.send.getContentDescription());
        }
    }

    @Test public void everySharedVectorDrawsWithoutFallbackGeometry() throws Exception {
        RuntimeEnvironment.setQualifiers("w412dp-h800dp-mdpi");
        Activity activity = Robolectric.buildActivity(Activity.class).setup().get();
        NativeUi ui = new NativeUi(activity, false);
        LinearLayout rows = ui.column();
        String[] names = {"menu","close","back","send","stop","plus","folder","folder-open","files","people","attach","settings","file-text","file-code","chevron-down","chevron-up","chevron-right","download","log-out","monitor","sun","moon","check","command","copy","shield-check","shield-question","message","search","refresh","codex","claude"};
        for (String name : names) {
            Drawable icon = NativeIcons.drawable(activity, name, ui.text, 24);
            assertEquals(24, icon.getBounds().width());
            TextView label = ui.text(name, 12, ui.text);
            label.setCompoundDrawables(icon, null, null, null);
            label.setCompoundDrawablePadding(12);
            rows.addView(label, new LinearLayout.LayoutParams(-1, 32));
        }
        draw(rows, 240, names.length * 32, "shared-vector-icons");
    }
}
