package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.text.Editable;
import android.text.SpannableString;
import android.text.Spanned;
import android.widget.EditText;

/** Editable CSS line boxes, bounded like Web AutosizeTextarea without disturbing history scroll. */
final class NativeAutosizeInput extends EditText {
    private final float lineMultiplier;
    private NativeLineHeight lineSpan;
    private float spanHeight;
    private float cssHeight;
    private CharSequence styledHint;

    float cssHeight() { return cssHeight; }

    @android.annotation.SuppressLint("InlinedApi") // Constant is inlined; the setter exists on API 23.
    NativeAutosizeInput(NativeUi ui, String hint, int size, float lineMultiplier, int rows) {
        super(ui.context);
        this.lineMultiplier = lineMultiplier;
        ui.typography(this, size, ui.text, 400);
        setInputType(android.text.InputType.TYPE_CLASS_TEXT
                | android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE
                | android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
        setMinLines(1); setMaxLines(rows);
        setGravity(Gravity.TOP | Gravity.START);
        setBackground(null); setHint(hint); setHintTextColor(ui.subtle);
        setBreakStrategy(android.graphics.text.LineBreaker.BREAK_STRATEGY_SIMPLE);
        setHyphenationFrequency(android.text.Layout.HYPHENATION_FREQUENCY_NONE);
        ui.preciseText(this);
    }

    private void refreshLineBoxes() {
        float height = getTextSize() * lineMultiplier;
        Editable text = getText();
        if (lineSpan == null || spanHeight != height) {
            if (lineSpan != null) text.removeSpan(lineSpan);
            lineSpan = new NativeLineHeight(height); spanHeight = height;
            styledHint = null;
        }
        if (text.getSpanStart(lineSpan) != 0 || text.getSpanEnd(lineSpan) != text.length())
            text.setSpan(lineSpan, 0, text.length(), Spanned.SPAN_INCLUSIVE_INCLUSIVE);
        if (getHint() != null && getHint() != styledHint) {
            SpannableString hint = new SpannableString(getHint());
            for (NativeLineHeight old : hint.getSpans(0, hint.length(), NativeLineHeight.class)) hint.removeSpan(old);
            hint.setSpan(lineSpan, 0, hint.length(), Spanned.SPAN_INCLUSIVE_INCLUSIVE);
            setHint(hint);
            styledHint = getHint();
        }
        // LineHeightSpan supplies complete, cumulatively rounded CSS boxes, including the last line.
        if (getLineSpacingExtra() != 0 || getLineSpacingMultiplier() != 1) setLineSpacing(0, 1);
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        refreshLineBoxes();
        // Empty DynamicLayout ignores paragraph spans; native gravity centers its real caret
        // inside the same one-line CSS box already used by the styled hint.
        int gravity = (length() == 0 ? Gravity.CENTER_VERTICAL : Gravity.TOP) | Gravity.START;
        if (getGravity() != gravity) setGravity(gravity);
        super.onMeasure(widthSpec, heightSpec);
        if (MeasureSpec.getMode(heightSpec) != MeasureSpec.EXACTLY) {
            float density = getResources().getDisplayMetrics().density;
            float padding = Math.round(getPaddingTop() / density) + Math.round(getPaddingBottom() / density);
            float line = spanHeight / density;
            int minimum = (int) Math.ceil(getMinLines() * line + padding);
            int maximum = (int) Math.ceil(getMaxLines() * line + padding);
            // scrollHeight, min and max are rounded in CSS pixels before device scaling.
            cssHeight = Math.max(minimum, Math.min(maximum, Math.round(Math.max(1, getLineCount()) * line + padding))) * density;
            int height = Math.round(cssHeight);
            setMeasuredDimension(getMeasuredWidth(), resolveSize(height, heightSpec));
        }
    }
}
