package com.cloudgeek.spool.android;

import java.text.SimpleDateFormat;
import java.util.Calendar;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;

/** Web MessageFooter's three supported locale formats, independent of the Android UI locale. */
final class NativeMessageTime {
    final String label, title, dateTime;

    private NativeMessageTime(String label, String title, String dateTime) {
        this.label = label; this.title = title; this.dateTime = dateTime;
    }

    static NativeMessageTime format(long timestamp, String locale, long now, TimeZone zone) {
        if (timestamp <= 0 || timestamp > 8_640_000_000_000_000L) return null;
        Calendar current = Calendar.getInstance(zone, Locale.ROOT), date = Calendar.getInstance(zone, Locale.ROOT);
        current.setTimeInMillis(now); date.setTimeInMillis(timestamp);
        boolean sameYear = current.get(Calendar.YEAR) == date.get(Calendar.YEAR);
        boolean today = sameYear && current.get(Calendar.DAY_OF_YEAR) == date.get(Calendar.DAY_OF_YEAR);
        boolean english = !locale.equals("ja") && !locale.equals("zh-CN");
        String clock = "HH:mm";
        String day = sameYear ? "MM/dd" : english ? "MM/dd/yyyy" : "yyyy/MM/dd";
        String label = format(timestamp, today ? clock : day + (english ? ", " : " ") + clock, zone);
        String title = format(timestamp, english ? "M/d/yyyy, HH:mm:ss" : locale.equals("ja") ? "yyyy/M/d H:mm:ss" : "yyyy/M/d HH:mm:ss", zone);
        String iso = format(timestamp, "yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", TimeZone.getTimeZone("UTC"));
        int yearEnd = iso.indexOf('-');
        if (yearEnd > 4) iso = String.format(Locale.ROOT, "+%06d", Integer.parseInt(iso.substring(0, yearEnd))) + iso.substring(yearEnd);
        return new NativeMessageTime(label, title, iso);
    }

    private static String format(long timestamp, String pattern, TimeZone zone) {
        SimpleDateFormat formatter = new SimpleDateFormat(pattern, Locale.ROOT);
        formatter.setTimeZone(zone);
        return formatter.format(new Date(timestamp));
    }
}
