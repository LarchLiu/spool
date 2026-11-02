package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.os.Looper;
import android.view.View;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.view.ViewGroup;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Locale;
import java.util.TimeZone;
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
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeMessageTest {
    @Test public void filteredEmptyInputHidesUserRowsButTextFilesAndImagesRemainVisible() throws Exception {
        JSONArray empty = new JSONArray();
        assertFalse(NativeUserRow.hasContent("", empty, empty, 0, 0));
        assertFalse(NativeUserRow.hasContent(" \n\t", empty, empty, 0, 0));
        assertTrue(NativeUserRow.hasContent("Real prompt", empty, empty, 0, 0));
        assertTrue(NativeUserRow.hasContent("", new JSONArray("[{id:'file'}]"), empty, 0, 0));
        JSONArray image = new JSONArray("[{role:'user',kind:'image',image_url:'data:image/png;base64,cGl4ZWw='}]");
        assertTrue(NativeUserRow.hasContent("", empty, image, 0, 1));
        image.getJSONObject(0).put("role", "tool");
        assertFalse(NativeUserRow.hasContent("", empty, image, 0, 1));
    }

    @Test public void userTextPreservesTheOriginalPartsAndWhitespaceForDisplayAndCopy() throws Exception {
        JSONArray parts = new JSONArray("[{role:'assistant',kind:'text',text:'skip'},"
                + "{role:'user',kind:'text',text:'  First  \\n'},"
                + "{role:'user',kind:'image',text:'skip'},"
                + "{role:'user',kind:'text',text:''},"
                + "{role:'user',kind:'text',text:' Last\\n'}]");
        assertEquals("  First  \n\n\n Last\n", NativeUserRow.text(parts, 0, parts.length()));
        assertEquals("\n Last\n", NativeUserRow.text(parts, 3, 100));
        assertEquals("", NativeUserRow.text(parts, -10, 1));
    }

    private JSONObject resource(String name) throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream(name)) {
            assertNotNull(name, input);
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    private JSONObject box(View view, View root) throws Exception {
        int x = 0, y = 0;
        for (View child = view; child != root; child = (View) child.getParent()) { x += child.getLeft(); y += child.getTop(); }
        return new JSONObject().put("x", x).put("y", y).put("width", view.getWidth()).put("height", view.getHeight());
    }

    private JSONArray optional(View view, View root) throws Exception {
        JSONArray result = new JSONArray();
        if (view != null && view.getParent() != null && view.getVisibility() != View.GONE) result.put(box(view, root));
        return result;
    }

    private void sceneLayout(View page, int width) {
        page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(100_000, View.MeasureSpec.AT_MOST));
        page.layout(0, 0, width, page.getMeasuredHeight());
    }

    private JSONArray paragraphs(NativeMessage message, View row) throws Exception {
        JSONArray result = new JSONArray();
        if (message.bubble.getVisibility() == View.GONE) return result;
        ViewGroup markdown = (ViewGroup) message.content.getChildAt(0);
        for (int i = 0; i < markdown.getChildCount(); i++) {
            View child = markdown.getChildAt(i);
            if (child instanceof TextView) {
                TextView text = (TextView) child;
                JSONArray lines = new JSONArray();
                for (int j = 0; j < text.getLineCount(); j++) lines.put(new JSONObject().put("offset", text.getLayout().getLineBaseline(j) - text.getLayout().getLineBaseline(0)));
                result.put(box(child, row).put("lineCount", text.getLineCount()).put("lines", lines));
            }
        }
        return result;
    }

    @Test @Config(sdk = 26) public void userRowsKeepMobileCapsAndAvatarGeometryAtBothDensities() throws Exception {
        for (String density : new String[] {"mdpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                NativeUserRow row = new NativeUserRow(ui, new JSONObject("{id:'mobile',nickname:'Mobile user'}"),
                        "A long message keeps the same native line boxes and mobile width.\n".repeat(12), 0, null, ignored -> {});
                controller.get().setContentView(row);
                sceneLayout(row, ui.dp(324));
                assertTrue(row.content.getWidth() <= ui.dp(324 - 38));
                assertEquals(ui.dp(28), row.getChildAt(1).getWidth());
                assertEquals(ui.dp(6), row.getChildAt(1).getTop());
                assertEquals(ui.dp(10), row.getChildAt(1).getLeft() - row.content.getRight());
                assertEquals(ui.dp(196), row.message.content.getHeight());
                assertEquals(ui.dp(22), row.message.toggle.getHeight());
                row.message.toggle.performClick();
                sceneLayout(row, ui.dp(324));
                assertTrue(row.message.content.getHeight() > ui.dp(196));
            }
        }
    }

    @Test @Config(sdk = {26, 35}) public void transcriptRowsRenderFromTheSameFixturesAsActualWebComponents() throws Exception {
        JSONObject fixtures = resource("/web-behavior.json");
        JSONArray cases = fixtures.getJSONArray("messageCases"), files = fixtures.getJSONArray("attachmentFiles");
        JSONArray goldens;
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-fixtures.json")) {
            goldens = new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
        TimeZone saved = TimeZone.getDefault();
        try {
            TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"));
            File directory = new File(System.getProperty("spool.ui.artifacts"));
            assertTrue(directory.isDirectory() || directory.mkdirs());
            for (int width : new int[] {320, 360, 412}) for (boolean dark : new boolean[] {false, true}) {
                RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    NativeAvatars avatars = new NativeAvatars(Runnable::run, (style, seed) -> {
                        for (int i = 0; i < goldens.length(); i++) if (goldens.getJSONObject(i).getString("name").equals(style)) return goldens.getJSONObject(i).getString("svg");
                        throw new java.io.IOException("Unknown avatar fixture");
                    });
                    NativeUi base = new NativeUi(controller.get(), dark);
                    LinearLayout page = base.padded(18, 25); page.setBackgroundColor(base.elevated);
                    List<View> rows = new ArrayList<>();
                    List<NativeMessage> messages = new ArrayList<>();
                    for (int i = 0; i < cases.length(); i++) {
                        JSONObject item = cases.getJSONObject(i);
                        NativeUi ui = new NativeUi(controller.get(), dark, item.getString("locale")); ui.avatars = avatars;
                        boolean mine = !item.optString("role").equals("assistant");
                        NativeMessage message;
                        View row;
                        if (mine) {
                            NativeAttachmentCards cards = null;
                            if (item.has("attachment")) {
                                JSONObject file = files.getJSONObject(item.getInt("attachment"));
                                cards = new NativeAttachmentCards(ui, true);
                                cards.addView(new NativeAttachmentCard(ui, file.getString("name"), file.getString("media_type"),
                                        file.optString("excerpt"), null, () -> {}, null));
                            }
                            NativeUserRow user = new NativeUserRow(ui, item.optJSONObject("operator"), item.getString("text"),
                                    item.optLong("timestamp"), cards, ignored -> {});
                            row = user; message = user.message;
                        } else { row = message = new NativeMessage(ui, item.getString("text"), false, "", item.optLong("timestamp"), false, ignored -> {}); }
                        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2);
                        if (mine) params.bottomMargin = 22;
                        page.addView(row, params); rows.add(row); messages.add(message);
                    }
                    controller.get().setContentView(page);
                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                    sceneLayout(page, width);
                    for (int i = 0; i < cases.length(); i++) if (cases.getJSONObject(i).optBoolean("expanded")) messages.get(i).toggle.performClick();
                    sceneLayout(page, width);
                    JSONArray geometry = new JSONArray();
                    for (int i = 0; i < rows.size(); i++) {
                        View row = rows.get(i); NativeMessage message = messages.get(i);
                        if (message.footer.author.getParent() != null && !cases.getJSONObject(i).getString("id").equals("long-author"))
                            assertEquals("Intrinsic author must not lose its final glyph", 0, message.footer.author.getLayout().getEllipsisCount(0));
                        NativeUserRow user = row instanceof NativeUserRow ? (NativeUserRow) row : null;
                        View avatar = user != null && user.getChildCount() > 1 ? user.getChildAt(1) : null;
                        JSONArray attachments = new JSONArray();
                        if (user != null && user.attachments != null) for (int j = 0; j < user.attachments.getChildCount(); j++) attachments.put(box(user.attachments.getChildAt(j), row));
                        geometry.put(new JSONObject().put("row", box(row, row)).put("content", box(user == null ? row : user.content, row))
                                .put("bubble", optional(message.bubble, row)).put("clip", message.bubble.getVisibility() == View.GONE ? new JSONArray() : optional(message.content, row))
                                .put("markdown", message.bubble.getVisibility() == View.GONE ? new JSONArray() : optional(message.content.getChildAt(0), row))
                                .put("paragraphs", paragraphs(message, row)).put("footer", box(message.footer, row)).put("author", optional(message.footer.author, row)).put("time", optional(message.footer.time, row))
                                .put("copy", box(message.footer.copy, row)).put("toggle", optional(message.toggle, row)).put("avatar", optional(avatar, row)).put("attachments", attachments));
                    }
                    String suffix = width + (dark ? "-dark" : "-light");
                    String prefix = android.os.Build.VERSION.SDK_INT == 26 ? "native-api26-" : "native-";
                    Files.writeString(new File(directory, prefix + "messages-" + suffix + ".json").toPath(), new JSONObject().put("rows", geometry).toString(2));
                    Bitmap bitmap = Bitmap.createBitmap(width, page.getHeight(), Bitmap.Config.ARGB_8888); page.draw(new Canvas(bitmap));
                    String imagePrefix = android.os.Build.VERSION.SDK_INT == 26 ? "native-api26-" : "";
                    try (FileOutputStream output = new FileOutputStream(new File(directory, imagePrefix + "messages-" + suffix + ".png"))) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
                    bitmap.recycle(); avatars.clear();
                }
            }
        } finally { TimeZone.setDefault(saved); }
    }

    @Test public void messageTimesFollowTheThreeUiLocalesRatherThanTheDeviceLocale() throws Exception {
        Locale saved = Locale.getDefault();
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            assertNotNull(input);
            JSONArray cases = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getJSONArray("messageTimes");
            assertEquals(144, cases.length());
            Locale.setDefault(Locale.FRANCE);
            for (int i = 0; i < cases.length(); i++) {
                JSONObject test = cases.getJSONObject(i);
                String context = test.toString();
                NativeMessageTime actual = NativeMessageTime.format(test.getLong("timestamp"), test.getString("locale"),
                        test.getLong("now"), TimeZone.getTimeZone(test.getString("timezone")));
                if (test.isNull("expected")) { assertNull(context, actual); continue; }
                JSONObject expected = test.getJSONObject("expected");
                assertNotNull(context, actual);
                assertEquals(context, expected.getString("label"), actual.label);
                assertEquals(context, expected.getString("title"), actual.title);
                assertEquals(context, expected.getString("dateTime"), actual.dateTime);
            }
        } finally { Locale.setDefault(saved); }
    }

    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.AT_MOST),
                View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.AT_MOST));
        view.layout(0, 0, view.getMeasuredWidth(), view.getMeasuredHeight());
    }

    private void iconSize(NativeUi ui, NativeMessageFooter footer) {
        assertEquals(ui.dp(28), footer.copy.getWidth());
        assertEquals(ui.dp(13), footer.copy.getWidth() - footer.copy.getPaddingLeft() - footer.copy.getPaddingRight());
        assertEquals(ui.dp(13), footer.copy.getHeight() - footer.copy.getPaddingTop() - footer.copy.getPaddingBottom());
    }

    @Test public void footersKeepTimeAndCopyVisibleWhenTheAuthorNeedsToShrink() {
        RuntimeEnvironment.setQualifiers("w320dp-h800dp-mdpi");
        for (String locale : new String[] {"zh-CN", "en", "ja"}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                NativeMessageFooter footer = new NativeMessageFooter(ui, true, "A very long operator nickname", "Copy this", 1735787045000L,
                        1793440000000L, TimeZone.getTimeZone("Asia/Shanghai"), ignored -> {});
                layout(footer, 190);
                assertSame(footer.author, footer.getChildAt(0));
                assertSame(footer.time, footer.getChildAt(1));
                assertSame(footer.copy, footer.getChildAt(2));
                assertTrue(footer.author.getWidth() < ui.dp(140));
                assertEquals(0, footer.time.getLayout().getEllipsisCount(0));
                assertTrue(footer.author.getLeft() >= 0);
                assertTrue(footer.copy.getRight() <= footer.getWidth());
                iconSize(ui, footer);
                layout(footer, 360);
                assertEquals(ui.dp(140), footer.author.getWidth());
                NativeMessageFooter assistant = new NativeMessageFooter(ui, false, "Ignored", "Reply", 1735787045000L,
                        1793440000000L, TimeZone.getTimeZone("UTC"), ignored -> {});
                layout(assistant, 320);
                assertEquals(2, assistant.getChildCount());
                assertSame(assistant.copy, assistant.getChildAt(0));
                assertSame(assistant.time, assistant.getChildAt(1));
                assertFalse(assistant.time.getContentDescription().toString().isEmpty());
            }
        }
    }

    @Test public void copyFeedbackIsLocalizedAndResetsWithoutChangingTheIconSize() {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        for (String locale : new String[] {"zh-CN", "en", "ja"}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, locale);
                AtomicReference<String> copied = new AtomicReference<>();
                NativeMessageFooter footer = new NativeMessageFooter(ui, true, "", "  Exact text\n", 0, 0, TimeZone.getTimeZone("UTC"), copied::set);
                controller.get().setContentView(footer);
                layout(footer, 320);
                footer.copy.performClick();
                assertEquals("  Exact text\n", copied.get());
                assertEquals(ui.t("Message copied"), footer.copy.getContentDescription());
                iconSize(ui, footer);
                Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1999));
                assertEquals(ui.t("Message copied"), footer.copy.getContentDescription());
                Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(1));
                assertEquals(ui.t("Copy message"), footer.copy.getContentDescription());
                iconSize(ui, footer);
                footer.copy.performClick();
                controller.get().setContentView(new View(controller.get()));
                assertEquals(ui.t("Copy message"), footer.copy.getContentDescription());
            }
        }
    }

    @Test public void failedCopyAndEmptyMessagesUseTheWebStates() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "ja");
            NativeMessageFooter footer = new NativeMessageFooter(ui, true, "", "Copy", 0, 0, TimeZone.getTimeZone("UTC"),
                    ignored -> { throw new IllegalStateException("Clipboard unavailable"); });
            controller.get().setContentView(footer);
            layout(footer, 320);
            footer.copy.performClick();
            assertEquals(ui.t("Could not copy message"), footer.copy.getContentDescription());
            iconSize(ui, footer);
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(2));
            assertEquals(ui.t("Copy message"), footer.copy.getContentDescription());
            NativeMessage empty = new NativeMessage(ui, "", true, "", 0, false, ignored -> {});
            layout(empty, 320);
            assertEquals(View.GONE, empty.bubble.getVisibility());
            assertEquals(0, empty.footer.getTop());
            assertEquals(ui.dp(28), empty.getHeight());
            assertFalse(empty.footer.copy.isEnabled());
        }
    }
}
