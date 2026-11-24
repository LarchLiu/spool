package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.animation.ValueAnimator;
import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.widget.Button;
import java.io.File;
import java.nio.file.Files;
import java.util.List;
import java.util.Map;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class NativeServerTabsTest {
    private static void layout(NativeServerTabs tabs) {
        tabs.measure(View.MeasureSpec.makeMeasureSpec(640, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(120, View.MeasureSpec.AT_MOST));
        tabs.layout(0, 0, 640, tabs.getMeasuredHeight());
        ValueAnimator animation = ReflectionHelpers.getField(tabs, "animation");
        if (animation != null) animation.end();
    }

    private static Bitmap image(View view, int background) {
        Bitmap image = Bitmap.createBitmap(view.getWidth(), view.getHeight(), Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(image); canvas.drawColor(background); view.draw(canvas); return image;
    }

    private static void assertShapes(NativeServerTabs tabs, NativeUi ui, Button ordinary, Button saved, Button pending) {
        assertEquals(ui.dp(32), ordinary.getHeight());
        assertEquals("Trusted destinations are inset vertically", ordinary.getHeight() - ui.dp(6), saved.getHeight());
        assertEquals(saved.getHeight(), pending.getHeight());
        Bitmap image = image(tabs.tabs, ui.soft);
        try {
            int selectedColor = ui.dark ? ui.sidebar : ui.elevated;
            assertEquals("An ordinary selected tab has a small corner radius", selectedColor,
                    image.getPixel(ordinary.getLeft() + ui.dp(2), ordinary.getTop() + ui.dp(3)));
            for (Button trusted : new Button[]{saved, pending}) {
                assertEquals("Only trusted destinations have capsule corners", ui.soft,
                        image.getPixel(trusted.getLeft() + ui.dp(2), trusted.getTop() + ui.dp(3)));
                assertEquals("Trusted destinations retain an outline even when unselected", ui.borderStrong,
                        image.getPixel((trusted.getLeft() + trusted.getRight()) / 2, trusted.getTop()));
                assertEquals(trusted.isSelected() ? selectedColor : ui.soft,
                        image.getPixel((trusted.getLeft() + trusted.getRight()) / 2, trusted.getTop() + ui.dp(2)));
            }
        } finally { image.recycle(); }
    }

    private static void assertDesktopSpacing(NativeServerTabs tabs, NativeUi ui) {
        assertEquals(ui.dp(6), tabs.track.getLeft()); assertEquals(ui.dp(6), tabs.track.getTop());
        assertEquals(ui.dp(6), tabs.getWidth() - tabs.track.getRight());
        assertEquals(ui.dp(6), tabs.getHeight() - tabs.track.getBottom());
        assertEquals(ui.dp(4), tabs.scroll.getLeft()); assertEquals(ui.dp(4), tabs.scroll.getTop());
        assertEquals(ui.dp(4), tabs.track.getWidth() - tabs.scroll.getRight());
        assertEquals(ui.dp(4), tabs.track.getHeight() - tabs.scroll.getBottom());
        assertEquals(ui.dp(32), tabs.add.getHeight()); assertEquals(tabs.add.getHeight(), tabs.add.getWidth());
        for (int i = 0; i < tabs.tabs.getChildCount(); i++) if (tabs.tabs.getChildAt(i) instanceof Button) {
            Button tab = (Button) tabs.tabs.getChildAt(i);
            assertEquals(ui.dp(12), tab.getPaddingLeft()); assertEquals(ui.dp(12), tab.getPaddingRight());
            assertEquals(ui.dp(6), tab.getPaddingTop()); assertEquals(ui.dp(6), tab.getPaddingBottom());
            assertTrue("The shorter tabs still fit their label", tab.getHeight() - tab.getCompoundPaddingTop()
                    - tab.getCompoundPaddingBottom() >= tab.getLayout().getHeight());
        }
    }

    @Test public void onlyTrustedDestinationsUseOutlinedCapsulesAcrossSelectionChanges() throws Exception {
        RuntimeEnvironment.setQualifiers("w640dp-h120dp-mdpi");
        for (boolean dark : new boolean[]{false, true})
            try (ActivityController<Activity> activity = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(activity.get(), dark, "en");
                ServerProfiles profiles = new ServerProfiles(new ConnectionPlan("https://home.example.com", "", ConnectionMode.DIRECT));
                ServerProfiles.Server home = profiles.active(), office = profiles.add("Office");
                home.name = "Home"; office.plan = new ConnectionPlan("https://office.example.com", "", ConnectionMode.DIRECT);
                // Cached account metadata does not turn an ordinary profile into a trusted destination.
                office.account.put("source_server_id", "b".repeat(52));
                String target = "b".repeat(52);
                ServerProfiles.Server session = profiles.trustedProfile(office, home, target, "a".repeat(43));
                session.enabled = true;
                List<NativeFederation.Tab> trusted = NativeFederation.tabs(profiles, home, List.of(
                        new NativeFederation.Target(target, "Office", office.plan.direct),
                        new NativeFederation.Target("c".repeat(52), "Lab", "")));
                Map<String, String> states = Map.of(home.id, "connected", office.id, "connected", session.id, "connected");
                NativeServerTabs tabs = new NativeServerTabs(ui, profiles, states, id -> {}, true, () -> {}, trusted);
                activity.get().setContentView(tabs); layout(tabs);
                assertDesktopSpacing(tabs, ui);
                Button saved = tabs.button(trusted.get(0).key()), pending = tabs.button(trusted.get(1).key());
                assertShapes(tabs, ui, tabs.button(home.id), saved, pending);
                Bitmap strip = Bitmap.createBitmap(tabs.getWidth(), tabs.getHeight() * 3, Bitmap.Config.ARGB_8888);
                try {
                    Canvas canvas = new Canvas(strip); tabs.draw(canvas);
                    profiles.selected = session.id; tabs.status(states); layout(tabs);
                    assertTrue(saved.isSelected()); assertFalse(tabs.button(home.id).isSelected());
                    Bitmap selected = image(tabs.tabs, ui.soft);
                    try {
                        assertEquals("The selected destination's indicator follows its capsule shape", ui.soft,
                                selected.getPixel(saved.getLeft() + ui.dp(2), saved.getTop() + ui.dp(3)));
                        assertEquals(ui.dark ? ui.sidebar : ui.elevated,
                                selected.getPixel((saved.getLeft() + saved.getRight()) / 2, saved.getTop() + ui.dp(2)));
                    } finally { selected.recycle(); }
                    canvas.save(); canvas.translate(0, tabs.getHeight()); tabs.draw(canvas); canvas.restore();
                    profiles.selected = office.id; tabs.status(states); layout(tabs);
                    assertShapes(tabs, ui, tabs.button(office.id), saved, pending);
                    canvas.save(); canvas.translate(0, tabs.getHeight() * 2); tabs.draw(canvas); canvas.restore();
                    File output = new File(System.getProperty("spool.ui.artifacts"), "server-tabs");
                    assertTrue(output.isDirectory() || output.mkdirs());
                    try (java.io.OutputStream stream = Files.newOutputStream(new File(output,
                            "shapes-" + (dark ? "dark" : "light") + "-api" + android.os.Build.VERSION.SDK_INT + ".png").toPath())) {
                        assertTrue(strip.compress(Bitmap.CompressFormat.PNG, 100, stream));
                    }
                } finally { strip.recycle(); }
            }
    }
}
