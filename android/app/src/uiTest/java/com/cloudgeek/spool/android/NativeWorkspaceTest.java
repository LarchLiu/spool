package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.FrameLayout;
import androidx.core.widget.NestedScrollView;
import java.io.File;
import java.io.FileOutputStream;
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

/** Login/empty workspace boxes and real editable input, with no server or user data. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeWorkspaceTest {
    private void layout(View root, int width) {
        root.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, width, 800);
    }

    private JSONObject box(View view, View root) throws Exception {
        int x = 0, y = 0;
        for (View current = view; current != root; current = (View) current.getParent()) {
            x += current.getLeft(); y += current.getTop();
        }
        return new JSONObject().put("x", x).put("y", y)
                .put("width", view.getWidth()).put("height", view.getHeight());
    }

    private void record(View root, String name, JSONObject geometry) throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        String prefix = android.os.Build.VERSION.SDK_INT == 26 ? "native-api26-" : "native-";
        Files.writeString(new File(directory, prefix + name + ".json").toPath(), geometry.toString(2));
        Bitmap image = Bitmap.createBitmap(root.getWidth(), root.getHeight(), Bitmap.Config.ARGB_8888);
        root.draw(new Canvas(image));
        String imagePrefix = android.os.Build.VERSION.SDK_INT == 26 ? "native-api26-" : "";
        try (FileOutputStream output = new FileOutputStream(new File(directory, imagePrefix + name + ".png"))) {
            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, output));
        }
        image.recycle();
    }

    @Test public void loginMatchesWebMobileFormGeometry() throws Exception {
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    NativeLogin login = new NativeLogin(ui, "", false, () -> {}, () -> {});
                    controller.get().setContentView(login);
                    layout(login, width);
                    record(login, "login-" + width + (dark ? "-dark" : "-light"), new JSONObject()
                            .put("story", box(login.story, login)).put("brand", box(login.brand, login))
                            .put("headline", box(login.headline, login)).put("panel", box(login.panel, login))
                            .put("form", box(login.form, login)).put("heading", box(login.heading, login))
                            .put("description", box(login.description, login)).put("username", box(login.username, login))
                            .put("password", box(login.password, login)).put("submit", box(login.submit, login))
                            .put("preferences", box(login.preferences, login)));
                }
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void workspaceAndAutosizingComposerMatchWebMobileGeometry() throws Exception {
        String[] screens = {"workspace", "workspace-1", "workspace-3", "workspace-6", "workspace-overflow", "workspace-wrapped"};
        String[] values = {"", "First line", "First line\nSecond line\nThird line",
                "One\nTwo\nThree\nFour\nFive\nSix", "One\nTwo\nThree\nFour\nFive\nSix\nSeven\nEight",
                "A long message that wraps naturally across several lines in the mobile composer. The history must remain stable while the input grows."};
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    LinearLayout page = ui.workspace();
                    NativeUi.Header header = ui.new Header("New chat", "Mobile project", () -> {}, () -> {}, () -> {});
                    page.addView(header); page.addView(ui.divider());
                    NestedScrollView history = new NestedScrollView(controller.get());
                    LinearLayout body = ui.padded(18, 25);
                    NativeWelcome welcome = ui.welcome(ui.t("What shall we build today?"),
                            ui.t("Describe your idea. Your agent takes it from here."), "Mobile project",
                            ui.t("Run locally. Pick up anytime."));
                    body.addView(welcome); history.addView(body);
                    page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
                    NativeUi.Composer composer = ui.new Composer("Message Codex", () -> {}, () -> {}, () -> {}, () -> {});
                    composer.model.bind("gpt-6.1-codex", "codex", "Choose model");
                    composer.permission.bind("Ask for approval", "shield-question", "Permissions");
                    page.addView(composer); controller.get().setContentView(page);
                    for (int scenario = 0; scenario < screens.length; scenario++) {
                        composer.input.setText(values[scenario]); layout(page, width);
                        android.view.ViewGroup shell = (android.view.ViewGroup) composer.getChildAt(0);
                        View bottom = shell.getChildAt(shell.getChildCount() - 1);
                        JSONObject headerBox = box(header, page).put("height", header.getHeight() + 1);
                        record(page, screens[scenario] + "-" + width + (dark ? "-dark" : "-light"), new JSONObject()
                                .put("header", headerBox).put("menu", box(header.getChildAt(0), page))
                                .put("title", box(header.title, page)).put("subtitle", box(header.subtitle, page))
                                .put("online", box(header.online, page)).put("files", box(header.getChildAt(3), page))
                                .put("welcome", box(welcome, page)).put("mark", box(welcome.mark, page))
                                .put("eyebrow", box(welcome.eyebrow, page)).put("titleWelcome", box(welcome.title, page))
                                .put("description", box(welcome.description, page)).put("notes", box(welcome.notes, page))
                                .put("noteItems", new JSONArray().put(box(welcome.project, page)).put(box(welcome.local, page)))
                                .put("composer", box(shell, page)).put("input", box(composer.input, page))
                                .put("bottom", box(bottom, page)).put("add", box(composer.add, page))
                                .put("permission", box(composer.permission, page)).put("model", box(composer.model, page))
                                .put("send", box(composer.send, page)));
                    }
                }
            }
        }
    }

    @Test public void atmosphereUsesWebEllipsesAndClippedOrbitLayers() throws Exception {
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeUi ui = new NativeUi(controller.get(), dark);
                    for (int layer = 0; layer < 3; layer++) {
                        boolean story = layer > 0;
                        int height = story ? 270 : 800;
                        FrameLayout root = new FrameLayout(controller.get());
                        root.setBackground(story ? ui.storyBackground() : ui.workspaceBackground());
                        root.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
                        root.layout(0, 0, width, height);
                        Bitmap image = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                        root.getBackground().setBounds(0, 0, width, height);
                        if (layer == 2) ((NativeAtmosphere) root.getBackground()).drawSurface(new Canvas(image));
                        else root.draw(new Canvas(image));
                        JSONArray samples = new JSONArray();
                        for (int row = 0; row <= 20; row++) for (int column = 0; column <= 20; column++) {
                            int x = (width - 1) * column / 20, y = (height - 1) * row / 20, color = image.getPixel(x, y);
                            samples.put(new JSONObject().put("x", x).put("y", y)
                                    .put("red", android.graphics.Color.red(color)).put("green", android.graphics.Color.green(color))
                                    .put("blue", android.graphics.Color.blue(color)).put("alpha", android.graphics.Color.alpha(color)));
                        }
                        String name = "background-" + (layer == 2 ? "login-surface" : story ? "login" : "workspace") + "-" + width + (dark ? "-dark" : "-light");
                        Files.writeString(new File(directory, "native-" + name + ".json").toPath(), samples.toString(2));
                        try (FileOutputStream stream = new FileOutputStream(new File(directory, name + ".png"))) {
                            assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, stream));
                        }
                        image.recycle();
                    }
                }
            }
        }
    }

    @Test public void loginAndWelcomeShareAllThreeCatalogsAndNotesWrapWithoutClipping() throws Exception {
        for (String locale : new String[] {"zh-CN", "en", "ja"}) {
            RuntimeEnvironment.setQualifiers("w320dp-h800dp-mdpi");
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                NativeLogin login = new NativeLogin(ui, "", true, () -> {}, () -> {});
                controller.get().setContentView(login); layout(login, 320);
                assertEquals(ui.t("Ideas, brought to life."), login.headline.getText().toString());
                assertEquals(ui.t("Sign in to your workspace"), login.heading.getText().toString());
                assertEquals(ui.t("Use the account created by your desktop administrator."), login.description.getText().toString());
                assertEquals(ui.t("Username"), login.usernameLabel.getText().toString());
                assertEquals(ui.t("Password"), login.passwordLabel.getText().toString());
                assertEquals(ui.t("Signing in…"), login.submit.getText().toString());
                assertEquals(ui.t("Need an account? Contact your desktop administrator."), login.help.getText().toString());
                assertFalse(login.submit.isEnabled());
                NativeWelcome welcome = ui.welcome(ui.t("What shall we build today?"),
                        ui.t("Describe your idea. Your agent takes it from here."),
                        "A longer mobile project with reusable native components", ui.t("Run locally. Pick up anytime."));
                welcome.measure(View.MeasureSpec.makeMeasureSpec(284, View.MeasureSpec.EXACTLY),
                        View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.AT_MOST));
                welcome.layout(0, 0, 284, welcome.getMeasuredHeight());
                assertEquals(ui.t("A little space for big ideas"), welcome.eyebrow.getText().toString());
                assertTrue(welcome.notes.getHeight() > 14);
                for (int i = 0; i < welcome.notes.getChildCount(); i++) {
                    View note = welcome.notes.getChildAt(i);
                    assertTrue(note.getLeft() >= 0);
                    assertTrue(note.getRight() <= welcome.notes.getWidth());
                    assertTrue(note.getBottom() <= welcome.notes.getHeight());
                    assertTrue(note.getHeight() >= (i == 0 ? 14 : 13));
                }
            }
        }
    }

    @Test public void editorKeepsCompositionSelectionAndHistoryScrollWhileGrowingAndShrinking() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            LinearLayout page = ui.column();
            NestedScrollView history = new NestedScrollView(controller.get());
            View contents = new View(controller.get());
            contents.setMinimumHeight(3000);
            contents.setLayoutParams(new NestedScrollView.LayoutParams(-1, 3000));
            history.addView(contents); page.addView(history, new LinearLayout.LayoutParams(-1, 0, 1));
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
            page.addView(composer); controller.get().setContentView(page); layout(page, 360);
            composer.input.requestFocus();
            org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle();
            layout(page, 360);
            history.scrollTo(0, 900);
            assertEquals(3000, contents.getHeight());
            assertEquals(900, history.getScrollY());
            android.view.inputmethod.InputConnection connection = composer.input.onCreateInputConnection(new android.view.inputmethod.EditorInfo());
            assertNotNull(connection);
            assertTrue(connection.setComposingText("日本語\n入力\nThree", 1));
            layout(page, 360);
            assertEquals(99, composer.input.getHeight());
            assertEquals(900, history.getScrollY());
            assertEquals(0, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(composer.input.getText()));
            assertEquals(composer.input.length(), composer.input.getSelectionEnd());
            connection.finishComposingText();
            assertEquals(-1, android.view.inputmethod.BaseInputConnection.getComposingSpanStart(composer.input.getText()));
            composer.input.setText("One\nTwo\nThree\nFour\nFive\nSix\nSeven\nEight"); layout(page, 360);
            assertEquals(176, composer.input.getHeight());
            composer.input.setSelection(composer.input.length());
            assertEquals(900, history.getScrollY());
            composer.input.setText(""); layout(page, 360);
            assertEquals(48, composer.input.getHeight());
            assertEquals(900, history.getScrollY());
            assertEquals(6, composer.input.getMaxLines());
        }
    }

    @Test @Config(sdk = 26) public void loginAndAutosizingInputAlsoWorkOnTheMinimumAndroidVersion() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "ja");
            NativeLogin login = new NativeLogin(ui, "", false, () -> {}, () -> {});
            controller.get().setContentView(login); layout(login, 360);
            assertEquals(ui.t("Username"), login.username.getContentDescription());
            Bitmap image = Bitmap.createBitmap(360, 800, Bitmap.Config.ARGB_8888);
            login.draw(new Canvas(image));
            assertEquals(255, android.graphics.Color.alpha(image.getPixel(0, 0)));
            image.recycle();
            NativeUi.Composer composer = ui.new Composer("Message", () -> {}, () -> {}, () -> {}, () -> {});
            composer.input.setText("One\nTwo\nThree");
            controller.get().setContentView(composer);
            composer.measure(View.MeasureSpec.makeMeasureSpec(360, View.MeasureSpec.EXACTLY),
                    View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.AT_MOST));
            composer.layout(0, 0, 360, composer.getMeasuredHeight());
            assertEquals(99, composer.input.getHeight());
            assertEquals(6, composer.input.getMaxLines());
        }
    }
}
