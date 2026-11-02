package com.cloudgeek.spool.android;

import static org.junit.Assert.*;

import android.app.Activity;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.text.PrecomputedText;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.widget.TextView;
import java.nio.file.Files;
import java.nio.file.Path;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

/** Keep measured tracking and actual native glyph painting independently observable. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = 35)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeTrackingTest {
    @Test public void productionCaretGeometryAndPrecomputedPaintControlRemainIndependent() throws Exception {
        JSONArray samples = new JSONArray();
        for (String density : new String[] {"mdpi", "420dpi", "xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h800dp-" + density);
            try (ActivityController<Activity> controller = Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui = new NativeUi(controller.get(), false);
                for (boolean selectable : new boolean[] {false, true}) for (boolean precomputed : new boolean[] {false, true}) {
                    TextView text = precomputed ? new TextView(ui.context) : ui.text("", 23, ui.text);
                    ui.typography(text, 23, ui.text, 650);
                    text.setTextSize(23.1f); text.setLetterSpacing(-.02f);
                    ui.preciseText(text); text.setTextIsSelectable(selectable);
                    String value = "agjpqy";
                    text.setText(precomputed ? PrecomputedText.create(value, text.getTextMetricsParams()) : value, TextView.BufferType.SPANNABLE);
                    controller.get().setContentView(text);
                    int width = ui.dp(324), height = ui.dp(80);
                    text.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
                    text.layout(0, 0, width, height);
                    Bitmap actual = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                    Bitmap expected = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                    Canvas canvas = new Canvas(actual); canvas.drawColor(Color.WHITE); text.draw(canvas);
                    Canvas direct = new Canvas(expected); direct.drawColor(Color.WHITE);
                    Paint paint = new Paint(text.getPaint());
                    direct.drawTextRun(value, 0, value.length(), 0, value.length(), text.getTotalPaddingLeft(), text.getBaseline(), false, paint);
                    int different = 0, maximum = 0;
                    for (int y = 0; y < height; y++) for (int x = 0; x < width; x++) {
                        int a = actual.getPixel(x, y), e = expected.getPixel(x, y), delta = 0;
                        for (int shift : new int[] {0, 8, 16, 24}) delta = Math.max(delta, Math.abs((a >>> shift & 255) - (e >>> shift & 255)));
                        if (delta > 1) different++;
                        maximum = Math.max(maximum, delta);
                    }
                    JSONArray positions = new JSONArray();
                    for (int offset = 0; offset <= value.length(); offset++) positions.put(text.getLayout().getPrimaryHorizontal(offset));
                    boolean distinctCarets = true;
                    for (int offset = 1; offset <= value.length(); offset++) distinctCarets &= positions.getDouble(offset) > positions.getDouble(offset - 1);
                    if (!precomputed) assertTrue("Production text must preserve each ASCII caret boundary", distinctCarets);
                    Object document = text.getText();
                    Selection.setSelection((Spannable) document, 1, 4);
                    text.measure(View.MeasureSpec.makeMeasureSpec(ui.dp(100), View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
                    text.layout(0, 0, text.getMeasuredWidth(), height);
                    assertSame(document, text.getText());
                    assertEquals(1, text.getSelectionStart()); assertEquals(4, text.getSelectionEnd());
                    samples.put(new JSONObject().put("density", density).put("selectable", selectable).put("precomputed", precomputed)
                            .put("document", document.getClass().getName()).put("layout", text.getLayout().getClass().getName())
                            .put("paintAdvance", paint.getRunAdvance(value, 0, value.length(), 0, value.length(), false, value.length()))
                            .put("positions", positions).put("distinctCarets", distinctCarets)
                            .put("differentPixels", different).put("maximum", maximum));
                    actual.recycle(); expected.recycle();
                }
            }
        }
        Path output = Path.of(System.getProperty("spool.ui.artifacts")); Files.createDirectories(output);
        Files.writeString(output.resolve("native-tracking-probe.json"), samples.toString(2));
    }
}
