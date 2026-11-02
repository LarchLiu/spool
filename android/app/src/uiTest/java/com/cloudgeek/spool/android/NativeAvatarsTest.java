package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.os.Looper;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.Shadows;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Consumer;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeAvatarsTest {
    @Test @Config(sdk = {26,35}) @org.robolectric.annotation.LooperMode(org.robolectric.annotation.LooperMode.Mode.PAUSED)
    public void rebindingKeepsTheSameImageCoalescesAndRejectsLateOldOrBotReplacements() throws Exception {
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false); List<Runnable> jobs=new ArrayList<>(); String svg=fixtures().getJSONObject(0).getString("svg");
            AtomicInteger generated=new AtomicInteger();ui.avatars=new NativeAvatars(jobs::add,(style,seed)->{generated.incrementAndGet();return svg;});
            JSONObject first=new JSONObject().put("id","alice").put("nickname","First").put("avatar",NativeAvatars.options("clay","one"));
            ImageView image=ui.avatar(first,28);ui.avatars.bind(image,ui,new JSONObject(first.toString()),28);assertEquals(1,jobs.size());
            JSONObject second=NativeAvatars.withAvatar(first,"pixel-art","two");ui.avatars.bind(image,ui,second,28);assertEquals(2,jobs.size());
            jobs.remove(0).run();Shadows.shadowOf(Looper.getMainLooper()).idle();assertNull("Old local decode must not bind to a new seed",image.getDrawable());
            ui.avatars.bind(image,ui,new JSONObject().put("id","spool-bot"),28);android.graphics.drawable.Drawable logo=image.getDrawable();
            jobs.remove(0).run();Shadows.shadowOf(Looper.getMainLooper()).idle();assertSame(logo,image.getDrawable());assertEquals("spool-bot",image.getTag());
            ui.avatars.bind(image,ui,first,28);assertNotNull(image.getDrawable());assertTrue(jobs.isEmpty());android.graphics.drawable.Drawable cached=image.getDrawable();
            first.put("nickname","Renamed");ui.avatars.bind(image,ui,first,28);assertSame(cached,image.getDrawable());assertEquals(2,generated.get());ui.avatars.clear();
        }
    }

    @Test @Config(sdk = {26,35}) @org.robolectric.annotation.LooperMode(org.robolectric.annotation.LooperMode.Mode.PAUSED)
    public void aFailedLocalRenderCanRetryOnTheSameImageWithoutAnApi() throws Exception {
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);List<Runnable> jobs=new ArrayList<>();String svg=fixtures().getJSONObject(0).getString("svg");AtomicInteger generated=new AtomicInteger();
            ui.avatars=new NativeAvatars(jobs::add,(style,seed)->{if(generated.incrementAndGet()==1)throw new java.io.IOException("local failure");return svg;});
            JSONObject person=new JSONObject().put("id","alice");ImageView image=ui.avatar(person,28);jobs.remove(0).run();Shadows.shadowOf(Looper.getMainLooper()).idle();assertNull(image.getDrawable());
            ui.avatars.bind(image,ui,person,28);assertEquals(1,jobs.size());jobs.remove(0).run();Shadows.shadowOf(Looper.getMainLooper()).idle();assertNotNull(image.getDrawable());assertEquals(2,generated.get());ui.avatars.clear();
        }
    }

    private JSONArray fixtures() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-fixtures.json")) {
            assertNotNull("Shared golden vectors must be generated", input);
            return new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    @Test public void allGoParityVectorsRenderAsNativeImages() throws Exception {
        JSONArray fixtures = fixtures();
        Bitmap sheet = Bitmap.createBitmap(96 * 3, 96 * 8, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(sheet);
        for (int i = 0; i < fixtures.length(); i++) {
            JSONObject fixture = fixtures.getJSONObject(i);
            Bitmap bitmap = NativeAvatars.decode(fixture.getString("svg"));
            assertNotEquals("Missing background for " + fixture.getString("name"), 0, bitmap.getPixel(48, 48) >>> 24);
            canvas.drawBitmap(bitmap, i % 3 * 96, i / 3 * 96, null);
            bitmap.recycle();
        }
        File directory = new File(System.getProperty("spool.ui.artifacts"));
        assertTrue(directory.isDirectory() || directory.mkdirs());
        try (FileOutputStream output = new FileOutputStream(new File(directory, "shared-avatars.png"))) {
            assertTrue(sheet.compress(Bitmap.CompressFormat.PNG, 100, output));
        }
        sheet.recycle();
    }

    @Test public void localGenerationAndBitmapCacheAreSharedAcrossComponents() throws Exception {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            Activity activity = controller.get();
            NativeUi ui = new NativeUi(activity, false);
            String svg = fixtures().getJSONObject(0).getString("svg");
            AtomicInteger requests = new AtomicInteger();
            ui.avatars = new NativeAvatars(Runnable::run, (style, seed) -> {
                requests.incrementAndGet();
                assertEquals("clay", style);
                assertEquals("Mobile user", seed);
                return svg;
            });
            JSONObject person = new JSONObject("{id:'alice',nickname:'Mobile user'}");
            ImageView first = ui.avatar(person, 28), second = ui.avatar(person, 24);
            assertEquals(1, requests.get());
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            assertNotNull(first.getDrawable());
            assertNotNull(second.getDrawable());
            assertNotNull(ui.avatar(person, 16).getDrawable());
            assertEquals(1, requests.get());
            first.measure(View.MeasureSpec.makeMeasureSpec(28, View.MeasureSpec.EXACTLY),
                    View.MeasureSpec.makeMeasureSpec(28, View.MeasureSpec.EXACTLY));
            first.layout(0, 0, 28, 28);
            Bitmap clipped = Bitmap.createBitmap(28, 28, Bitmap.Config.ARGB_8888);
            first.draw(new Canvas(clipped));
            assertEquals("Web avatars have circular, transparent corners", 0, clipped.getPixel(0, 0) >>> 24);
            assertNotEquals(0, clipped.getPixel(14, 14) >>> 24);
            clipped.recycle();
            List<JSONObject> people = new ArrayList<>();
            for (int i = 0; i < 6; i++) people.add(person);
            NativeAvatarStack stack = new NativeAvatarStack(ui, people, false);
            stack.measure(android.view.View.MeasureSpec.makeMeasureSpec(200, android.view.View.MeasureSpec.AT_MOST),
                    android.view.View.MeasureSpec.makeMeasureSpec(28, android.view.View.MeasureSpec.EXACTLY));
            assertEquals(4, stack.getChildCount());
            assertEquals(85, stack.getLayoutParams().width);
            assertEquals(28, stack.getLayoutParams().height);
            assertEquals(1, requests.get());
            ui.avatars.clear();
        }
    }

    @Test public void localOptionsStayDataAndRetiredStylesMatchWebFallbacks() throws Exception {
        JSONObject person = new JSONObject().put("id", "x").put("nickname", "<script> + / 中文");
        assertEquals("clay", NativeAvatars.config(person).getString("style"));
        assertEquals(person.getString("nickname"), NativeAvatars.seed(person));
        for (String style : new String[] {"initials", "shapes", "bottts-neutral"}) {
            person.put("avatar", new JSONObject().put("style", style).put("seed", "picked:42"));
            assertEquals("clay", NativeAvatars.config(person).getString("style"));
            assertEquals("picked:42", NativeAvatars.seed(person));
        }
        person.put("avatar", new JSONObject().put("style", "unknown").put("seed", "discard"));
        assertEquals(person.getString("nickname"), NativeAvatars.seed(person));
        person.put("nickname", "\u00a0\u2003Alice\u3000");
        assertEquals("Alice", NativeAvatars.name(person));
    }

    @Test public void previewRasterUsesPhysicalPixelsOnDenseScreens() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-xxhdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            String svg = fixtures().getJSONObject(0).getString("svg");
            ui.avatars = new NativeAvatars(Runnable::run, (style, seed) -> svg);
            ImageView preview = ui.avatar(new JSONObject("{id:'alice',nickname:'Mobile user'}"), 80);
            Shadows.shadowOf(Looper.getMainLooper()).idle();
            Bitmap image = ((android.graphics.drawable.BitmapDrawable) preview.getDrawable()).getBitmap();
            assertEquals(ui.dp(80), image.getWidth());
            assertEquals(ui.dp(80), image.getHeight());
            ui.avatars.clear();
        }
    }

    @Test public void nativeSvgCannotLoadExternalOrActiveResources() {
        for (String body : new String[] {"<image href='https://tracker.example/pixel'/>",
                "<use href='https://tracker.example/a.svg#x'/>", "<script>alert(1)</script>",
                "<foreignObject/>", "<!DOCTYPE svg SYSTEM 'https://tracker.example/evil.dtd'>",
                "<style>@import 'https://tracker.example/evil.css';</style>",
                "<rect fill='url( https://tracker.example/paint.svg )'/>"}) {
            assertThrows(body, IllegalArgumentException.class,
                    () -> NativeAvatars.decode("<svg xmlns='http://www.w3.org/2000/svg'>" + body + "</svg>"));
        }
    }

    private View label(View root, String value) {
        if (value.equals(root.getContentDescription()) || root instanceof TextView
                && value.contentEquals(((TextView) root).getText())) return root;
        if (root instanceof ViewGroup) {
            ViewGroup parent = (ViewGroup) root;
            for (int i = 0; i < parent.getChildCount(); i++) {
                View result = label(parent.getChildAt(i), value);
                if (result != null) return result;
            }
        }
        return null;
    }

    @Test public void pickerUsesTheSameChoicesAndPersistsOnlyOptionsWithFailureRecovery() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            JSONObject person = new JSONObject("{id:'alice',nickname:'Mobile user'}");
            AtomicReference<JSONObject> saved = new AtomicReference<>();
            AtomicReference<Consumer<String>> completion = new AtomicReference<>();
            NativeAvatarPicker picker = new NativeAvatarPicker(ui, person,
                    (avatar, done) -> { saved.set(avatar); completion.set(done); }, ignored -> fail("Link opened without a click"));
            picker.show();
            View decor = picker.getWindow().getDecorView();
            assertFalse(label(decor, "Save avatar").isEnabled());
            assertTrue(label(decor, "Avatar 1").performClick());
            label(decor, "More avatars").performClick();
            assertNull(label(decor, "Avatar 1"));
            label(decor, "Avatar 13").performClick();
            label(decor, "Pixel Art").performClick();
            label(decor, "Save avatar").performClick();
            assertEquals("pixel-art", saved.get().getString("style"));
            assertEquals("Mobile user:13", saved.get().getString("seed"));
            assertEquals(2, saved.get().length());
            assertFalse(label(decor, "Cancel").isEnabled());
            assertFalse(label(decor, "Avatar 13").isEnabled());
            completion.get().accept("A recoverable server error");
            assertEquals(View.VISIBLE, picker.error.getVisibility());
            assertTrue(label(decor, "Avatar 13").isSelected());
            label(decor, "Use my name").performClick();
            label(decor, "Save avatar").performClick();
            assertEquals("", saved.get().getString("seed"));
            completion.get().accept(null);
            assertFalse(picker.isShowing());
        }
        for (String seed : NativeAvatars.choices("\ud83d\udc31".repeat(50) + "\n", 50)) {
            assertTrue(seed.getBytes(StandardCharsets.UTF_8).length <= 128);
            assertFalse(seed.contains("\n"));
        }
        assertEquals("spool:1", NativeAvatars.choices("\n", 0).get(0));
    }

    @Test public void profilePushWinsOverOldSavesWithoutRestoringRevokedDirectoryMembers() throws Exception {
        NativeProfiles cache = new NativeProfiles();
        cache.accept(new JSONObject("{revision:4,people:[{id:'alice',nickname:'Alice'},{id:'revoked',nickname:'Revoked'}]}"));
        cache.accept(new JSONObject("{revision:4,people:[{id:'alice',nickname:'Alice'},{id:'new-member',nickname:'New member'}]}"));
        assertTrue(cache.acceptSaved(new JSONObject("{revision:4,people:[{id:'alice',nickname:'Updated'},{id:'revoked',nickname:'Revoked'}]}"), "alice"));
        assertEquals(2, cache.people.length());
        assertEquals("Updated", cache.people.getJSONObject(0).getString("nickname"));
        assertEquals("new-member", cache.people.getJSONObject(1).getString("id"));
        assertFalse(cache.acceptSaved(new JSONObject("{revision:3,people:[{id:'alice',nickname:'Stale'}]}"), "alice"));
        assertEquals("Updated", cache.people.getJSONObject(0).getString("nickname"));
        assertFalse(cache.accept(new JSONObject("{revision:3,people:[]}")));
        cache.clear();
        assertEquals(0, cache.people.length());
        assertTrue(cache.accept(new JSONObject("{revision:0,people:[]}")));
    }

    @Test public void pickerRendersAllMobileWidthsThemesAndGroupAvatarsStayComposed() throws Exception {
        JSONArray vectors = fixtures();
        for (int width : new int[] {320, 360, 412}) {
            RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi");
            for (boolean dark : new boolean[] {false, true}) {
                try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                    Activity activity = controller.get();
                    NativeUi ui = new NativeUi(activity, dark);
                    ui.avatars = new NativeAvatars(Runnable::run, (style, seed) -> {
                        return vectors.getJSONObject(NativeAvatarCatalog.index(style) * 3).getString("svg");
                    });
                    JSONObject person = new JSONObject("{id:'alice',nickname:'Mobile user'}");
                    NativeAvatarPicker picker = new NativeAvatarPicker(ui, person, (avatar, done) -> {}, ignored -> {});
                    picker.show();
                    Shadows.shadowOf(Looper.getMainLooper()).idle();
                    View frame = picker.modal;
                    int panelWidth = width - 38;
                    frame.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                            View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.AT_MOST));
                    frame.layout(0, 0, width, frame.getMeasuredHeight());
                    View panel = picker.modal.surface;
                    assertEquals(panelWidth, panel.getWidth());
                    assertTrue(panel.getMeasuredHeight() <= 760);
                    assertTrue(picker.scroll.getHeight() > 0);
                    assertTrue(picker.actions.getTop() + picker.actions.getHeight() <= 760);
                    Bitmap image = Bitmap.createBitmap(panelWidth, panel.getHeight(), Bitmap.Config.ARGB_8888);
                    panel.draw(new Canvas(image));
                    File file = new File(System.getProperty("spool.ui.artifacts"), "avatar-picker-" + width + (dark ? "-dark.png" : "-light.png"));
                    try (FileOutputStream output = new FileOutputStream(file)) { image.compress(Bitmap.CompressFormat.PNG, 100, output); }
                    image.recycle();
                    picker.dismiss();
                    List<JSONObject> members = List.of(person, person, person, person, person);
                    NativeAvatarStack group = new NativeAvatarStack(ui, members, NativeAvatarStack.Style.GROUP);
                    assertEquals(55, group.getLayoutParams().width);
                    assertEquals(20, group.getLayoutParams().height);
                    assertEquals(4, group.getChildCount());
                    assertEquals(34, NativeAvatarStack.group(ui, List.of()).getLayoutParams().width);
                    ui.avatars.clear();
                }
            }
        }
    }
}
