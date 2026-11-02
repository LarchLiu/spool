package com.cloudgeek.spool.android;

import java.util.Locale;

/** Web preference resolution, including ordered system language fallbacks. */
final class NativePreferences {
    static String language(String preference, String[] preferred) {
        String[] candidates = preference.equals("system") ? preferred : new String[] {preference};
        for (String candidate : candidates) {
            String base = candidate.toLowerCase(Locale.ROOT).replace('_', '-').split("-", 2)[0];
            if (base.equals("zh")) return "zh-CN";
            if (base.equals("en") || base.equals("ja")) return base;
        }
        return "en";
    }
}
