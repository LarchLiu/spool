package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.RectF;
import android.os.Build;
import android.text.Editable;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeComposerLayoutTest {
    private void layout(View page, int width, int height) {
        page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        page.layout(0, 0, width, height);
    }

    private JSONObject box(View view, View root) throws Exception {
        float x = 0, y = 0;
        for (View current = view; current != root; current = (View) current.getParent()) {
            x += current.getX(); y += current.getY();
        }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }

    @Test public void composedComposerRecordsLive420dpiGrowthKeyboardAndWindowTransitions() throws Exception {
        RuntimeEnvironment.setQualifiers("w900dp-h800dp-420dpi");
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        JSONArray cases;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("composerLayoutCases");
        }
        JSONArray samples = new JSONArray();
        for (boolean dark : new boolean[] {false, true}) for (String locale : new String[] {"en", "zh-CN", "ja"}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark, locale);
                float density = ui.context.getResources().getDisplayMetrics().density;
                assertEquals(2.625f, density, 0);
                LinearLayout page = ui.workspace();
                NativeUi.Header header = ui.new Header(ui.t("New chat"), "Mobile project", () -> {}, () -> {}, () -> {});
                View divider = ui.divider();
                page.addView(header); page.addView(divider);
                ScrollView history = new ScrollView(ui.context); history.setFillViewport(true);
                View transcript = new View(ui.context); transcript.setMinimumHeight(ui.dp(3000));
                history.addView(transcript, new ScrollView.LayoutParams(-1, ui.dp(3000)));
                page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
                NativeUi.Composer composer = ui.new Composer(ui.t("Message {agent}", "agent", "Codex"), () -> {}, () -> {}, () -> {}, () -> {});
                composer.model.bind("gpt-6.1-codex", "codex", ui.t("Choose a model"));
                composer.permission.bind(ui.t("Ask for approval"), "shield-question", "Permissions");
                page.addView(composer); controller.get().setContentView(page);
                for (int i = 0; i < cases.length(); i++) {
                    JSONObject item = cases.getJSONObject(i);
                    if (!locale.equals(item.getString("locale"))) continue;
                    String value = item.getString("value"), id = item.getString("id");
                    if (!value.contentEquals(composer.input.getText())) composer.input.setText(value);
                    Editable document = composer.input.getText();
                    int start = composer.input.getSelectionStart(), end = composer.input.getSelectionEnd();
                    layout(page, ui.dp(item.getInt("width")), ui.dp(item.getInt("height")));
                    assertSame(document, composer.input.getText());
                    assertEquals(start, composer.input.getSelectionStart()); assertEquals(end, composer.input.getSelectionEnd());
                    JSONObject headerBox = box(header, page).put("height", header.getHeight() + divider.getHeight());
                    JSONObject surface = box(composer.box, page);
                    RectF painted = ReflectionHelpers.getField(composer.box, "outer");
                    JSONObject geometry = new JSONObject().put("page", box(page, page)).put("header", headerBox).put("history", box(history, page))
                            .put("composer", box(composer, page)).put("surface", surface).put("input", box(composer.input, page))
                            .put("controls", box(composer.bottom, page)).put("add", box(composer.add, page))
                            .put("permission", box(composer.permission, page)).put("model", box(composer.model, page)).put("send", box(composer.send, page))
                            .put("paint", new JSONObject().put("left", surface.getDouble("x") + painted.left).put("top", surface.getDouble("y") + painted.top)
                                    .put("right", surface.getDouble("x") + painted.right).put("bottom", surface.getDouble("y") + painted.bottom).put("radius", 19 * density))
                            .put("inputStyle", new JSONObject().put("paddingLeft", composer.input.getPaddingLeft()).put("paddingRight", composer.input.getPaddingRight())
                                    .put("paddingTop", composer.input.getPaddingTop()).put("paddingBottom", composer.input.getPaddingBottom())
                                    .put("fontSize", composer.input.getTextSize()).put("lineHeight", (float) ReflectionHelpers.getField(composer.input, "spanHeight")))
                            .put("value", composer.input.getText().toString()).put("hint", composer.input.getHint().toString())
                            .put("modelLabel", composer.model.label.getText().toString()).put("permissionLabel", composer.permission.label.getText().toString())
                            .put("modelLabelTruncated", composer.model.label.getLayout().getEllipsisCount(0) > 0)
                            .put("permissionLabelVisible", composer.permission.label.getVisibility() == View.VISIBLE);
                    samples.put(new JSONObject(item.toString()).put("theme", dark ? "dark" : "light").put("density", density).put("geometry", geometry));
                    Bitmap image = Bitmap.createBitmap(page.getWidth(), page.getHeight(), Bitmap.Config.ARGB_8888);
                    page.draw(new Canvas(image));
                    try (FileOutputStream output = new FileOutputStream(new File(directory,
                            "native-api" + Build.VERSION.SDK_INT + "-composer-layout-" + id + (dark ? "-dark" : "-light") + "-dpr2.625.png"))) {
                        assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
                    } finally { image.recycle(); }
                }
            }
        }
        Files.writeString(new File(directory, "native-composer-layout-api" + Build.VERSION.SDK_INT + "-dpr2.625.json").toPath(), samples.toString(2));
    }
}
