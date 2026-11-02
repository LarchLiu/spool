package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Typeface;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.io.File;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeCodeTabsTest {
    private String fixture() throws Exception {
        try (java.io.InputStream input = getClass().getResourceAsStream("/web-behavior.json")) {
            return new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8)).getString("codeTabContents");
        }
    }
    private TextView code(View view) {
        if (view instanceof TextView && ((TextView) view).getText().toString().contains("\t")) return (TextView) view;
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) view).getChildCount(); i++) { TextView result = code(((ViewGroup) view).getChildAt(i)); if (result != null) return result; }
        return null;
    }
    private JSONArray positions(TextView view) throws Exception {
        JSONArray positions = new JSONArray(); String contents = view.getText().toString(); android.text.Layout layout = view.getLayout();
        for (int i = 0; i < contents.length(); i++) if (contents.charAt(i) == '\t') {
            int start = layout.getLineStart(layout.getLineForOffset(i));
            positions.put(new JSONObject().put("after", layout.getPrimaryHorizontal(i + 1) - layout.getPrimaryHorizontal(start)));
        }
        return positions;
    }
    private JSONArray canvasPositions(NativeUi ui, String contents) throws Exception {
        android.graphics.Paint paint = new android.graphics.Paint(android.graphics.Paint.ANTI_ALIAS_FLAG); paint.setTypeface(Typeface.MONOSPACE); paint.setTextSize(13 * ui.context.getResources().getDisplayMetrics().scaledDensity); paint.setLinearText(true); paint.setSubpixelText(true);
        JSONArray positions = new JSONArray();
        for (String line : contents.split("\n")) for (int i = 0; i < line.length(); i++) if (line.charAt(i) == '\t') positions.put(new JSONObject().put("after", NativeCodeTabs.width(paint, line, i + 1)));
        return positions;
    }
    @Test public void sourceAndMarkdownKeepOriginalTabsAndMatchWebStopPositions() throws Exception {
        String contents = fixture();
        for (int width : new int[] {320,360,412}) { RuntimeEnvironment.setQualifiers("w" + width + "dp-h800dp-mdpi"); for (boolean dark : new boolean[] {false,true}) {
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), dark); LinearLayout page = ui.column();
                NativeFileSource source = new NativeFileSource(ui, contents, 0); page.addView(source, new LinearLayout.LayoutParams(-1, -2));
                String fence = "```text\n" + contents + "\n```";
                LinearLayout markdown = NativeMarkdown.view(ui, fence, false, address -> {}); page.addView(markdown, new LinearLayout.LayoutParams(-1, -2));
                LinearLayout detail = NativeMarkdown.detail(ui, fence, address -> {}); page.addView(detail, new LinearLayout.LayoutParams(-1, -2));
                LinearLayout preview = NativeMarkdown.filePreview(ui, fence, address -> {}); page.addView(preview, new LinearLayout.LayoutParams(-1, -2)); controller.get().setContentView(page);
                page.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY)); page.layout(0, 0, width, 800);
                TextView block = code(markdown); assertNotNull(block); assertTrue(block.isTextSelectable()); assertEquals(contents, block.getText().toString()); assertEquals(contents, source.getText().toString());
                TextView tool = code(detail); assertNotNull(tool); assertEquals(contents, tool.getText().toString());
                TextView file = code(preview); assertNotNull(file); assertEquals(contents, file.getText().toString());
                JSONObject report = new JSONObject().put("source", positions(source)).put("markdown", positions(block)).put("detail", positions(tool)).put("preview", positions(file)).put("diff", canvasPositions(ui, contents));
                java.nio.file.Files.writeString(new File(System.getProperty("spool.ui.artifacts"), "native-code-tabs-" + width + (dark ? "-dark" : "-light") + ".json").toPath(), report.toString(2));
            }
        }}
    }
    @Test @Config(sdk = 26) @GraphicsMode(GraphicsMode.Mode.LEGACY)
    public void plainAndEmptyCodeStayUnchangedOnMinimumSdk() {
        try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
            TextView text = new TextView(controller.get()); text.setTypeface(Typeface.MONOSPACE);
            for (String source : new String[] {"", "plain\ntext\n", "\t", "\t\n", "a\tb\n\tc"}) {
                NativeCodeTabs.setText(text, source); assertEquals(source, text.getText().toString());
            }
        }
    }
    @Test public void singleLineTabsRemainScrollableAndSelectableAtBothPixelDensities() {
        for (String density : new String[] {"mdpi","xhdpi"}) { RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false); String literal = "\t".repeat(40) + "X";
                LinearLayout page = NativeMarkdown.view(ui, "```text\n" + literal + "\n```", false, address -> {}); controller.get().setContentView(page);
                page.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(360), View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(ui.dp(800), View.MeasureSpec.EXACTLY)); page.layout(0, 0, ui.dp(360), ui.dp(800));
                TextView block = code(page); assertNotNull(block); assertEquals(literal, block.getText().toString()); assertTrue(block.isTextSelectable());
                float end = block.getLayout().getPrimaryHorizontal(literal.length()); assertTrue(end > ui.dp(360));
                assertTrue("Wrap-content measurement must retain the tab grid, not measure raw glyphs", block.getWidth() >= Math.floor(end) + block.getPaddingLeft() + block.getPaddingRight());
                android.text.Selection.setSelection((android.text.Spannable) block.getText(), 0, literal.length()); assertEquals(literal, block.getText().subSequence(block.getSelectionStart(), block.getSelectionEnd()).toString());
                android.graphics.Paint paint = block.getPaint(); String canvasText = "AB\tX"; float[] space = new float[1]; paint.getTextWidths(" ", space); float expected = space[0] * 4;
                android.graphics.Bitmap actual = android.graphics.Bitmap.createBitmap(ui.dp(160), ui.dp(60), android.graphics.Bitmap.Config.ARGB_8888), reference = android.graphics.Bitmap.createBitmap(ui.dp(160), ui.dp(60), android.graphics.Bitmap.Config.ARGB_8888);
                NativeCodeTabs.draw(new android.graphics.Canvas(actual), paint, canvasText, 0, ui.dp(30));
                android.graphics.Canvas target = new android.graphics.Canvas(reference); target.drawText("AB", 0, ui.dp(30), paint); target.drawText("X", expected, ui.dp(30), paint);
                assertTrue("Diff canvas draws text at the same measured two-character stops", actual.sameAs(reference)); actual.recycle(); reference.recycle();
            }
        }
    }
}
