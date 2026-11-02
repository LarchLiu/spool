package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.Typeface;
import android.view.Gravity;
import android.widget.TextView;

/** Selectable native source with unwrapped lines and viewport-only gutters, not thousands of views. */
final class NativeFileSource extends TextView {
    private final NativeUi ui;
    private final Paint number = new Paint(Paint.ANTI_ALIAS_FLAG), fill = new Paint();
    private final Rect clip = new Rect();
    private final NativeSourcePalette palette;
    private final int gutter;
    final float character;
    final int targetLine;

    @android.annotation.SuppressLint("InlinedApi") // Constant is inlined; TextView's setter exists since API 23.
    NativeFileSource(NativeUi ui, String contents, int line) {
        this(ui, "", contents, line);
    }

    @android.annotation.SuppressLint("InlinedApi")
    NativeFileSource(NativeUi ui, String path, String contents, int line) {
        super(ui.context);
        this.ui = ui; targetLine = line;
        palette = new NativeSourcePalette(ui.dark);
        ui.typography(this, 11, palette.foreground, 400);
        setTypeface(Typeface.MONOSPACE);
        getPaint().setLinearText(true);
        getPaint().setSubpixelText(true);
        int digits = 1, lineCount = 1;
        for (int i = 0; i < contents.length(); i++) if (contents.charAt(i) == '\n') lineCount++;
        digits = String.valueOf(lineCount).length();
        float[] advances = new float[1];
        // measureText() rounds up; ch-based gutters must retain the fractional glyph advance.
        getPaint().getTextWidths("0", advances);
        character = advances[0];
        gutter = Math.round((digits + 3) * character) + ui.dp(2);
        setIncludeFontPadding(false);
        setGravity(Gravity.TOP | Gravity.START);
        setPadding(gutter + Math.round(character), ui.dp(20), Math.round(character * 2), ui.dp(32));
        setHorizontallyScrolling(true);
        setTextIsSelectable(true);
        NativeCodeTabs.setText(this, contents);
        setLineSpacing(ui.dp(20) - (getPaint().getFontMetrics().descent - getPaint().getFontMetrics().ascent), 1);
        setBreakStrategy(android.graphics.text.LineBreaker.BREAK_STRATEGY_SIMPLE);
        number.setTypeface(Typeface.MONOSPACE);
        number.setLinearText(true);
        number.setSubpixelText(true);
        number.setTextSize(getTextSize());
        number.setTextAlign(Paint.Align.RIGHT);
        number.setColor(ui.subtle);
        setContentDescription(ui.t("Source"));
        if (ui.syntax != null && !path.isEmpty()) ui.syntax.bind(this, path, contents, ui.dark, lines -> NativeSyntax.apply(this, lines));
    }

    @Override protected void onDraw(Canvas canvas) {
        android.text.Layout layout = getLayout();
        int first = 0, last = -1;
        if (layout != null) {
            canvas.getClipBounds(clip);
            first = layout.getLineForVertical(Math.max(0, clip.top - getPaddingTop()));
            last = layout.getLineForVertical(Math.max(0, clip.bottom - getPaddingTop()));
            for (int i = first; i <= last; i++) {
                float top = getPaddingTop() + layout.getLineTop(i) - ui.dp(4), bottom = top + ui.dp(20);
                if (i + 1 == targetLine) {
                    fill.setColor(palette.selection);
                    canvas.drawRect(0, top, getWidth(), bottom, fill);
                }
            }
        }
        super.onDraw(canvas);
        if (layout != null) {
            // Pierre keeps the gutter sticky while source text scrolls underneath it.
            int left = getParent() instanceof android.widget.HorizontalScrollView ? ((android.widget.HorizontalScrollView) getParent()).getScrollX() : 0;
            fill.setColor(ui.elevated);
            canvas.drawRect(left, clip.top, left + gutter, clip.bottom, fill);
            for (int i = first; i <= last; i++) {
                float top = getPaddingTop() + layout.getLineTop(i) - ui.dp(4), bottom = top + ui.dp(20);
                if (i + 1 == targetLine) {
                    fill.setColor(palette.gutterSelection);
                    canvas.drawRect(left, top, left + gutter - ui.dp(2), bottom, fill);
                }
                number.setColor(i + 1 == targetLine ? palette.selectedNumber : ui.subtle);
                canvas.drawText(String.valueOf(i + 1), left + gutter - character - ui.dp(2), getPaddingTop() + layout.getLineBaseline(i), number);
            }
        }
    }

    int targetTop() { return getLayout() == null || targetLine < 1 || targetLine > getLineCount() ? -1 : getPaddingTop() + getLayout().getLineTop(targetLine - 1) - ui.dp(4); }
}
