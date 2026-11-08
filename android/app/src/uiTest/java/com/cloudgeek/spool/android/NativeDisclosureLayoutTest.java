package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.TextView;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26, 35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeDisclosureLayoutTest {
    private void layout(View view, int width) {
        view.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
        view.layout(0, 0, width, view.getMeasuredHeight());
    }
    private TextView stats(NativeDisclosureRow row) {
        return org.robolectric.util.ReflectionHelpers.getField(row, "stats");
    }

    @Test public void rebindingTextDefersLayoutUntilTheRowKnowsItsNewWidth() {
        try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui = new NativeUi(controller.get(), false, "en");
            NativeDisclosureRow row = new NativeDisclosureRow(ui, "Old title", "book-open", "+1 -2", false, false, false, () -> {});
            layout(row, ui.dp(320)); assertNotNull(row.label.getLayout()); assertNotNull(stats(row).getLayout());
            row.bind("A much longer title for a different tool", "book-open", "+1234 -567");
            assertNull("Binding must not build a layout at the previous tool's width", row.label.getLayout());
            assertNull("Statistics also wait for their width constraint", stats(row).getLayout());
            layout(row, ui.dp(240)); assertNotNull(row.label.getLayout()); assertNotNull(stats(row).getLayout());
            Object label = row.label.getLayout(), count = stats(row).getLayout();
            row.bind("A much longer title for a different tool", "book-open", "+1234 -567");
            assertSame(label, row.label.getLayout()); assertSame(count, stats(row).getLayout());
        }
    }

    @Test public void deferredLayoutsPreservePixelsWidthsEllipsisAndHeightAcrossRecycledLabels() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"})
                for (int kind = 0; kind < 3; kind++) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, "zh-CN"); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeDisclosureRow actual = new NativeDisclosureRow(ui, "Initial", "book-open", "+1 -1", kind == 0, kind == 1, false, () -> {});
                NativeDisclosureRow previous = new NativeDisclosureRow(ui, "Initial", "book-open", "+1 -1", kind == 0, kind == 1, false, () -> {});
                previous.label.setLayoutParams(new FrameLayout.LayoutParams(-1, -1));
                stats(previous).setLayoutParams(new FrameLayout.LayoutParams(-1, -1));
                layout(actual, ui.dp(320)); layout(previous, ui.dp(320));
                String[] titles = {"Read src/Main.java", "查看工具结果：中文 English 日本語 long/path/".repeat(4), "短", "Another title"};
                String[] counts = {"+20 -30", "+12345 -67", "", "+4 -8"};
                for (int i = 0; i < titles.length; i++) {
                    CharSequence title = i == 1 ? NativeMarkdown.preview(ui, "**" + titles[i] + "**") : titles[i];
                    actual.bind(title, "book-open", counts[i]); previous.bind(title, "book-open", counts[i]);
                    previous.label.forceLayout();
                    if (stats(previous) != null) { stats(previous).setLayoutParams(new FrameLayout.LayoutParams(-1, -1)); stats(previous).forceLayout(); }
                    int width = ui.dp(i % 2 == 0 ? 320 : 240);
                    layout(actual, width); layout(previous, width);
                    assertEquals(previous.getHeight(), actual.getHeight()); assertEquals(previous.label.getWidth(), actual.label.getWidth());
                    assertEquals(previous.label.getBaseline(), actual.label.getBaseline());
                    assertEquals(previous.label.getLayout().getEllipsisCount(0), actual.label.getLayout().getEllipsisCount(0));
                    Bitmap a = Bitmap.createBitmap(width, actual.getHeight(), Bitmap.Config.ARGB_8888);
                    Bitmap b = Bitmap.createBitmap(width, previous.getHeight(), Bitmap.Config.ARGB_8888);
                    try { actual.draw(new Canvas(a)); previous.draw(new Canvas(b)); assertTrue(density + "/" + kind + "/" + i, a.sameAs(b)); }
                    finally { a.recycle(); b.recycle(); }
                }
            }
        }
    }

    @Test public void plainFallbackPreservesPixelsAndEllipsisAcrossLanguagesAndModeChanges() {
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (var controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false, "en"); ui.cjkFonts = NativeCjkFallbackTest.runtimeFonts();
                NativeDisclosureRow actual = new NativeDisclosureRow(ui, "", "book-open", "", false, false, false, () -> {});
                NativeDisclosureRow previous = new NativeDisclosureRow(ui, "", "book-open", "", false, false, false, () -> {});
                String[] titles = {"Read src/Main.java", "读取中文工具详情 English 日本語 long/path/".repeat(4),
                        "中文 العربية שלום", "中文 👩🏽‍💻 🏳️‍🌈 🇨🇳 é 𠀀".repeat(8), "短"};
                for (String title : titles) for (int dp : new int[] {240, 320}) {
                    actual.bind(title, "book-open", "+12 -3", true);
                    previous.bind(title, "book-open", "+12 -3", false);
                    int width = ui.dp(dp); layout(actual, width); layout(previous, width);
                    assertEquals(previous.getHeight(), actual.getHeight()); assertEquals(previous.label.getWidth(), actual.label.getWidth());
                    assertEquals(previous.label.getBaseline(), actual.label.getBaseline());
                    assertEquals(previous.label.getLayout().getEllipsisCount(0), actual.label.getLayout().getEllipsisCount(0));
                    Bitmap a = Bitmap.createBitmap(width, actual.getHeight(), Bitmap.Config.ARGB_8888);
                    Bitmap b = Bitmap.createBitmap(width, previous.getHeight(), Bitmap.Config.ARGB_8888);
                    try { actual.draw(new Canvas(a)); previous.draw(new Canvas(b)); assertTrue(density + "/" + dp + "/" + title, a.sameAs(b)); }
                    finally { a.recycle(); b.recycle(); }
                    // Reusing a holder with the same title still changes its text representation.
                    if (android.os.Build.VERSION.SDK_INT >= 28 && NativeCjkFallback.containsCjk(title)) {
                        assertTrue(actual.label.getText() instanceof String);
                        actual.bind(title, "book-open", "+12 -3", false);
                        assertTrue(actual.label.getText() instanceof android.text.Spanned);
                        actual.bind(title, "book-open", "+12 -3", true);
                        assertTrue(actual.label.getText() instanceof String);
                    }
                }
            }
        }
    }
}
