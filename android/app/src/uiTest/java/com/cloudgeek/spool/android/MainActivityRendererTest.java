package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.content.Intent;
import android.content.pm.ApplicationInfo;
import java.io.PrintWriter;
import java.io.StringWriter;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityRendererTest {
    private boolean launch(boolean debug, Boolean saved, Boolean requested, boolean checkProbe) {
        var application = RuntimeEnvironment.getApplication();
        ApplicationInfo info = application.getApplicationInfo(); int flags = info.flags;
        var preferences = application.getSharedPreferences("rendering-trial", 0);
        preferences.edit().clear().commit();
        if (saved != null) preferences.edit().putBoolean("markwon", saved).commit();
        info.flags = debug ? flags | ApplicationInfo.FLAG_DEBUGGABLE : flags & ~ApplicationInfo.FLAG_DEBUGGABLE;
        Intent intent = new Intent(application, MainActivity.class);
        if (requested != null) intent.putExtra("spool_markwon", requested);
        try (var controller = Robolectric.buildActivity(MainActivity.class, intent).setup()) {
            MainActivity activity = controller.get();
            boolean enabled = ReflectionHelpers.getField(activity, "useMarkwonHistory");
            if (checkProbe) {
                StringWriter output = new StringWriter();
                activity.dump("", null, new PrintWriter(output), new String[] {"spool-performance"});
                assertEquals(debug, output.toString().contains("SPOOL_PERF "));
            }
            if (requested != null && debug) assertEquals(requested, preferences.getBoolean("markwon", !requested));
            if (!debug && saved != null) assertEquals("Release must not rewrite the ignored debug preference", saved, preferences.getBoolean("markwon", !saved));
            return enabled;
        } finally { info.flags = flags; preferences.edit().clear().commit(); }
    }

    @Test public void everyBuildDefaultsToMarkwonAndOnlyDebugExposesTheProbe() {
        assertTrue(launch(false, null, null, true));
        assertTrue(launch(true, null, null, true));
    }

    @Test public void releaseIgnoresBothLegacyPreferenceAndIntentOverride() {
        assertTrue(launch(false, false, false, true));
        assertTrue(launch(false, true, false, false));
    }

    @Test public void debugRetainsPersistedAndIntentControlledRendererComparisons() {
        assertFalse(launch(true, false, null, false));
        assertTrue(launch(true, false, true, false));
        assertFalse(launch(true, true, false, false));
    }
}
