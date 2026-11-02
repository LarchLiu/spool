package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.drawable.BitmapDrawable;
import android.os.Looper;
import android.widget.ImageView;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
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
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;

/** Exercises the production default generator binding, without any Client or session. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {NativeLocalAvatarsTest.LocalGenerator.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeLocalAvatarsTest {
    private static JSONArray fixtures() throws Exception {
        try (java.io.InputStream input = NativeLocalAvatarsTest.class.getResourceAsStream("/web-fixtures.json")) {
            assertNotNull(input);
            return new JSONArray(new String(input.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    @Implements(value = spoolnet.Spoolnet.class, isInAndroidSdk = false)
    public static class LocalGenerator {
        static final List<String> calls = new ArrayList<>();
        @Implementation protected static void __staticInitializer__() {}
        @Implementation protected static String renderAvatar(String style, String seed) throws Exception {
            calls.add(new JSONArray().put(style).put(seed).toString());
            JSONArray cases = fixtures();
            for (int i = 0; i < cases.length(); i++) {
                JSONObject item = cases.getJSONObject(i);
                if (style.equals(item.getString("name")) && seed.equals(item.getString("seed"))) return item.getString("svg");
            }
            throw new IllegalArgumentException("No local fixture");
        }
    }

    @Test public void allStylesRenderThroughTheDefaultLocalBindingWithoutAConfiguredServer() throws Exception {
        LocalGenerator.calls.clear();
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            ui.avatars = new NativeAvatars(Runnable::run);
            JSONArray cases = fixtures();
            for (int i = 0; i < cases.length(); i++) {
                JSONObject item = cases.getJSONObject(i), person = new JSONObject().put("id", "offline")
                        .put("avatar", NativeAvatars.options(item.getString("name"), item.getString("seed")));
                ImageView image = ui.avatar(person, 28);
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertTrue(image.getDrawable() instanceof BitmapDrawable);
                assertNotEquals(0, ((BitmapDrawable) image.getDrawable()).getBitmap().getPixel(48, 48) >>> 24);
                assertSame(image.getDrawable().getClass(), ui.avatar(person, 16).getDrawable().getClass());
            }
            assertEquals(24, LocalGenerator.calls.size());
            int beforeBot = LocalGenerator.calls.size();
            assertNotNull(ui.avatar(new JSONObject("{id:'spool-bot',bot:true}"), 28).getDrawable());
            assertEquals(beforeBot, LocalGenerator.calls.size());
            ui.avatars.clear();
        }
    }

    @Test public void concurrentViewsShareOneJobAndClearingRevokesQueuedOrPostedResults() throws Exception {
        for (boolean alreadyRendered : new boolean[] {false, true}) {
            LocalGenerator.calls.clear();
            List<Runnable> jobs = new ArrayList<>();
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                ui.avatars = new NativeAvatars(jobs::add);
                JSONObject person = new JSONObject("{id:'offline',nickname:'Mobile user'}");
                ImageView old = ui.avatar(person, 28), duplicate = ui.avatar(person, 24);
                assertEquals(1, jobs.size());
                Runnable stale = jobs.remove(0);
                if (alreadyRendered) stale.run();
                ui.avatars.clear();
                if (!alreadyRendered) stale.run();
                Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertNull(old.getDrawable()); assertNull(duplicate.getDrawable());
                assertEquals(alreadyRendered ? 1 : 0, LocalGenerator.calls.size());
                ImageView current = ui.avatar(person, 28); assertEquals(1, jobs.size());
                jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle();
                assertNotNull(current.getDrawable()); assertNull(old.getDrawable());
                ui.avatars.clear();
            }
        }
    }

    @Test public void generatedBitmapCacheRetainsAnEightMiBBudgetAcrossPreviewSizes() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-xxhdpi");
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false);
            String svg = fixtures().getJSONObject(0).getString("svg");
            ui.avatars = new NativeAvatars(Runnable::run, (style, seed) -> svg);
            for (int i = 0; i < 48; i++) {
                ui.avatar(new JSONObject().put("id", "local-" + i), 200);
                Shadows.shadowOf(Looper.getMainLooper()).idle();
            }
            android.util.LruCache<?, ?> cache = org.robolectric.util.ReflectionHelpers.getField(ui.avatars, "cache");
            assertEquals(8 * 1024 * 1024, cache.maxSize());
            assertTrue(cache.size() <= cache.maxSize()); assertTrue(cache.evictionCount() > 0);
            ui.avatars.clear(); assertEquals(0, cache.size());
        }
    }
}
