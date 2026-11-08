package com.cloudgeek.spool.android;

import java.text.SimpleDateFormat;
import java.util.Calendar;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;
import java.util.LinkedHashMap;
import java.util.Map;

/** Web MessageFooter's three supported locale formats, independent of the Android UI locale. */
final class NativeMessageTime {
    final String label, title, dateTime;

    private NativeMessageTime(String label, String title, String dateTime) {
        this.label = label; this.title = title; this.dateTime = dateTime;
    }

    private static <K, V> Map<K, V> bounded(int limit) {
        return new LinkedHashMap<K, V>(16, .75f, true) {
            @Override protected boolean removeEldestEntry(Map.Entry<K, V> eldest) { return size() > limit; }
        };
    }
    private static final class ResultKey {
        final long timestamp;
        final String locale;
        final TimeZone zone;
        final boolean sameYear, today;
        ResultKey(long timestamp, String locale, TimeZone zone, boolean sameYear, boolean today) {
            this.timestamp = timestamp; this.locale = locale; this.zone = (TimeZone) zone.clone(); this.sameYear = sameYear; this.today = today;
        }
        @Override public boolean equals(Object value) {
            if (!(value instanceof ResultKey)) return false;
            ResultKey other = (ResultKey) value;
            return timestamp == other.timestamp && locale.equals(other.locale) && zone.equals(other.zone) && sameYear == other.sameYear && today == other.today;
        }
        @Override public int hashCode() { return java.util.Objects.hash(timestamp, locale, zone, sameYear, today); }
    }
    private static final class FormatterKey {
        final String pattern;
        final TimeZone zone;
        FormatterKey(String pattern, TimeZone zone) { this.pattern = pattern; this.zone = (TimeZone) zone.clone(); }
        @Override public boolean equals(Object value) {
            if (!(value instanceof FormatterKey)) return false;
            FormatterKey other = (FormatterKey) value;
            return pattern.equals(other.pattern) && zone.equals(other.zone);
        }
        @Override public int hashCode() { return java.util.Objects.hash(pattern, zone); }
    }
    private static final class Cache {
        final Map<ResultKey, NativeMessageTime> results = bounded(128);
        final Map<FormatterKey, SimpleDateFormat> formatters = bounded(16);
        long hits, formats, creates;
    }
    // DateFormat is mutable. Each thread owns its bounded formatters/results; no text or users are keys.
    private static final ThreadLocal<Cache> CACHE = ThreadLocal.withInitial(Cache::new);
    static long cacheHits() { return CACHE.get().hits; }
    static long formats() { return CACHE.get().formats; }
    static long formatterCreates() { return CACHE.get().creates; }
    static int cachedResults() { return CACHE.get().results.size(); }

    static NativeMessageTime format(long timestamp, String locale, long now, TimeZone zone) {
        if (timestamp <= 0 || timestamp > 8_640_000_000_000_000L) return null;
        Calendar current = Calendar.getInstance(zone, Locale.ROOT), date = Calendar.getInstance(zone, Locale.ROOT);
        current.setTimeInMillis(now); date.setTimeInMillis(timestamp);
        boolean sameYear = current.get(Calendar.YEAR) == date.get(Calendar.YEAR);
        boolean today = sameYear && current.get(Calendar.DAY_OF_YEAR) == date.get(Calendar.DAY_OF_YEAR);
        Cache cache = CACHE.get(); cache.formats++;
        ResultKey key = new ResultKey(timestamp, locale, zone, sameYear, today);
        NativeMessageTime cached = cache.results.get(key);
        if (cached != null) { cache.hits++; return cached; }
        boolean english = !locale.equals("ja") && !locale.equals("zh-CN");
        String clock = "HH:mm";
        String day = sameYear ? "MM/dd" : english ? "MM/dd/yyyy" : "yyyy/MM/dd";
        String label = format(timestamp, today ? clock : day + (english ? ", " : " ") + clock, zone);
        String title = format(timestamp, english ? "M/d/yyyy, HH:mm:ss" : locale.equals("ja") ? "yyyy/M/d H:mm:ss" : "yyyy/M/d HH:mm:ss", zone);
        String iso = format(timestamp, "yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", TimeZone.getTimeZone("UTC"));
        int yearEnd = iso.indexOf('-');
        if (yearEnd > 4) iso = String.format(Locale.ROOT, "+%06d", Integer.parseInt(iso.substring(0, yearEnd))) + iso.substring(yearEnd);
        NativeMessageTime result = new NativeMessageTime(label, title, iso);
        cache.results.put(key, result);
        return result;
    }

    private static String format(long timestamp, String pattern, TimeZone zone) {
        Cache cache = CACHE.get();
        FormatterKey key = new FormatterKey(pattern, zone);
        SimpleDateFormat formatter = cache.formatters.get(key);
        if (formatter == null) {
            formatter = new SimpleDateFormat(pattern, Locale.ROOT); formatter.setTimeZone(key.zone);
            cache.formatters.put(key, formatter); cache.creates++;
        }
        return formatter.format(new Date(timestamp));
    }
}
