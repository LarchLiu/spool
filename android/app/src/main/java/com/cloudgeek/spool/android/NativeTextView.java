package com.cloudgeek.spool.android;

import android.content.Context;
import android.graphics.drawable.Drawable;
import android.os.Build;
import android.widget.TextView;

/** Shared native CSS text: fallback faces, complete line boxes and stable selectable layout. */
class NativeTextView extends TextView {
    float lineBox;
    float cssNormalAscent = Float.NaN;
    boolean fixedLineBox;
    boolean cssFontMetrics;
    boolean roundWidth;
    boolean ceilWidth;
    int fontWeight = 400;
    NativeCjkFallback fallback;
    boolean normalFallback;
    NativeLineHeight lineMetrics;
    boolean cssPositioned, observingOrigin, aligningOrigin;
    float cssRoundingY;
    private NativeLineHeight alignedMetrics;
    private android.text.Layout alignedLayout;
    private float alignedTop, alignedRoundingY;
    private final android.view.ViewTreeObserver.OnPreDrawListener originObserver = this::onOriginPreDraw;
    private NativeLineHeight alignedOnce;
    private int alignedPadding;
    /** Baseline re-snaps that rebuilt this document's layout; tests pin warm-document reuse with it. */
    int slowAligns;
    // Debug-only process counters for the spool-performance dump; no text content is retained.
    static long originAligns, originFresh, originNanos, textSets, textSetNanos;
    static long originTopChanges, originRoundingChanges, originMetricChanges;
    static org.json.JSONObject diagnostics() {
        org.json.JSONObject result = new org.json.JSONObject();
        try { result.put("aligns", originAligns).put("freshAligns", originFresh).put("alignMs", originNanos / 1_000_000d)
                .put("topChanges", originTopChanges).put("roundingChanges", originRoundingChanges).put("metricChanges", originMetricChanges)
                .put("textSets", textSets).put("textSetMs", textSetNanos / 1_000_000d); }
        catch (org.json.JSONException impossible) { throw new IllegalStateException(impossible); }
        return result;
    }
    NativeTextView(Context context) { super(context); }
    @Override public boolean onTextContextMenuItem(int id) {
        return NativeCssWrap.source(this, () -> super.onTextContextMenuItem(id));
    }
    @Override public void onInitializeAccessibilityNodeInfo(android.view.accessibility.AccessibilityNodeInfo info) {
        NativeCssWrap.source(this, () -> { super.onInitializeAccessibilityNodeInfo(info); return null; });
        if (NativeCssWrap.active(this)) info.setText(getText());
    }
    @Override public void onPopulateAccessibilityEvent(android.view.accessibility.AccessibilityEvent event) {
        NativeCssWrap.source(this, () -> {
            super.onPopulateAccessibilityEvent(event);
            // Events outlive this scope; do not retain a live display-only transformation.
            if (NativeCssWrap.active(this)) for (int i = 0; i < event.getText().size(); i++)
                event.getText().set(i, new android.text.SpannedString(event.getText().get(i)));
            return null;
        });
    }
    void observeOrigin() {
        if (cssPositioned && isAttachedToWindow() && !observingOrigin) {
            getViewTreeObserver().addOnPreDrawListener(originObserver); observingOrigin = true;
        }
    }
    @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); alignedMetrics = null; observeOrigin(); }
    @Override protected void onDetachedFromWindow() {
        if (observingOrigin && getViewTreeObserver().isAlive()) getViewTreeObserver().removeOnPreDrawListener(originObserver);
        observingOrigin = false; alignedMetrics = null; alignedLayout = null; super.onDetachedFromWindow();
    }
    @Override public void requestLayout() {
        if (!aligningOrigin) alignedMetrics = null;
        super.requestLayout();
    }
    @Override public void setGravity(int gravity) {
        int previous = getGravity(); super.setGravity(gravity);
        // TextView changes vertical gravity without requesting a layout.
        if (previous != getGravity()) alignedMetrics = null;
    }
    boolean alignOrigin() {
        if (aligningOrigin || !cssPositioned || lineMetrics == null) return false;
        // A transcript row released during this pre-draw pass still receives the pass's
        // remaining callbacks. Its detached ancestor chain yields a different origin; snapping
        // and relaying out a document nobody can see wastes a layout now and another on remount.
        if (!isAttachedToWindow()) return false;
        float top = NativeUi.cssBlockOrigin(this);
        // Scrolling does not change document coordinates. Avoid TextView's native
        // padding and layout queries on every frame of an unchanged selectable document.
        if (alignedMetrics == lineMetrics && alignedTop == top && alignedRoundingY == cssRoundingY) return false;
        if (getLayout() == null || getWidth() <= 0 || getHeight() <= 0 || !(getText() instanceof android.text.Spannable)) return false;
        boolean repeat = alignedOnce == lineMetrics;
        int padding = getTotalPaddingTop();
        if (repeat) {
            if (alignedTop != top) originTopChanges++;
            if (alignedRoundingY != cssRoundingY) originRoundingChanges++;
            if (alignedPadding != padding) originMetricChanges++;
        }
        alignedLayout = getLayout();
        alignedMetrics = lineMetrics; alignedTop = top; alignedRoundingY = cssRoundingY;
        if (!lineMetrics.origin(top + padding, cssRoundingY)) return false;
        boolean fresh = !repeat;
        long started = System.nanoTime();
        aligningOrigin = true;
        try {
            android.text.Spannable document = (android.text.Spannable) getText();
            document.setSpan(lineMetrics, document.getSpanStart(lineMetrics), document.getSpanEnd(lineMetrics), document.getSpanFlags(lineMetrics));
            forceLayout();
            measure(MeasureSpec.makeMeasureSpec(getWidth(), MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(getHeight(), MeasureSpec.EXACTLY));
        } finally {
            alignedLayout = getLayout(); aligningOrigin = false; alignedOnce = lineMetrics; alignedPadding = padding; slowAligns++;
            originAligns++; if (fresh) originFresh++; originNanos += System.nanoTime() - started;
        }
        return true;
    }
    // TextView owns a separate one-shot onPreDraw() for cursor/selection and scrolling.
    boolean onOriginPreDraw() { return !alignOrigin(); }
    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        if (changed) alignedMetrics = null;
        alignOrigin();
    }
    @Override public int getBaseline() {
        int baseline = super.getBaseline();
        // CSS inline boxes align to the logical strut, not the glyph baseline snapped later for paint.
        return cssPositioned && lineMetrics != null && getLayout() != null
                ? baseline - getLayout().getLineBaseline(0) + lineMetrics.intrinsicBaseline() : baseline;
    }
    void refreshFallback() {
        int start = getSelectionStart(), end = getSelectionEnd();
        setText(getText(), BufferType.SPANNABLE);
        if (start >= 0 && end >= 0 && getText() instanceof android.text.Spannable)
            android.text.Selection.setSelection((android.text.Spannable) getText(), Math.min(start, length()), Math.min(end, length()));
    }
    void applyLineBox() {
        int start = getSelectionStart(), end = getSelectionEnd();
        setLineSpacing(0, 1);
        setText(getText(), BufferType.SPANNABLE);
        if (start >= 0 && end >= 0 && getText() instanceof android.text.Spannable)
            android.text.Selection.setSelection((android.text.Spannable) getText(), Math.min(start, length()), Math.min(end, length()));
    }
    private CharSequence fallback(CharSequence value) {
        if (fallback != null) {
            if (normalFallback && Build.VERSION.SDK_INT < 28) value = fallback.apply(value, getTextLocale().toLanguageTag(), fontWeight);
            value = fallback.applyCss(value, getTextLocale().toLanguageTag(), fontWeight);
        }
        return value;
    }
    void bindMarkdownText(CharSequence value) {
        if (!android.text.TextUtils.equals(getText(), value) || !(getText() instanceof android.text.Spannable)) {
            boolean selected = getSelectionStart() >= 0 && getSelectionEnd() >= 0;
            setText(value);
            if (selected && getText() instanceof android.text.Spannable) android.text.Selection.setSelection((android.text.Spannable) getText(), 0);
            return;
        }
        value = fallback(value);
        android.text.Spannable document = (android.text.Spannable) getText();
        // Formatting/href updates must not replace an unchanged selectable document.
        for (Object span : document.getSpans(0, document.length(), Object.class))
            if (!(span instanceof android.text.NoCopySpan) && span != lineMetrics) document.removeSpan(span);
        if (value instanceof android.text.Spanned) {
            android.text.Spanned spans = (android.text.Spanned) value;
            for (Object span : spans.getSpans(0, spans.length(), Object.class))
                if (!(span instanceof android.text.NoCopySpan) && !(span instanceof NativeLineHeight))
                    document.setSpan(span, spans.getSpanStart(span), spans.getSpanEnd(span), spans.getSpanFlags(span));
        }
        requestLayout(); invalidate();
    }
    @Override public void setText(CharSequence value, BufferType type) {
        long started = System.nanoTime();
        try { setTextTimed(value, type); } finally { textSets++; textSetNanos += System.nanoTime() - started; }
    }
    private void setTextTimed(CharSequence value, BufferType type) {
        value = fallback(value);
        if (fixedLineBox && lineBox > 0 && value != null && value.length() > 0) {
            android.text.SpannableString rendered = new android.text.SpannableString(value);
            for (NativeLineHeight old : rendered.getSpans(0, rendered.length(), NativeLineHeight.class)) rendered.removeSpan(old);
            lineMetrics = new NativeLineHeight(lineBox, cssFontMetrics ? getResources().getDisplayMetrics().density : 0, getPaint(), cssNormalAscent);
            rendered.setSpan(lineMetrics, 0, rendered.length(), android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            value = rendered;
        } else lineMetrics = null;
        super.setText(value, type);
    }
    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        super.onMeasure(widthSpec, heightSpec);
        // A parent-only remeasure can reuse the same document layout and origin.
        if (!aligningOrigin && alignedLayout != getLayout()) alignedMetrics = null;
        float natural = getPaint().getFontMetrics().descent - getPaint().getFontMetrics().ascent;
        int extra = Math.max(0, Math.round(lineBox - natural));
        if (MeasureSpec.getMode(heightSpec) != MeasureSpec.EXACTLY) {
            Drawable[] icons = getCompoundDrawables();
            int iconHeight = Math.max(icons[0] == null ? 0 : icons[0].getBounds().height(),
                    icons[2] == null ? 0 : icons[2].getBounds().height());
            int height = fixedLineBox ? Math.max(iconHeight, Math.round(lineBox * getLineCount())) + getCompoundPaddingTop() + getCompoundPaddingBottom()
                    : getMeasuredHeight() + extra;
            setMeasuredDimension(getMeasuredWidth(), resolveSize(height, heightSpec));
        }
        boolean legacyParagraphs = roundWidth && Build.VERSION.SDK_INT < 28
                && MeasureSpec.getMode(widthSpec) != MeasureSpec.EXACTLY && getLineCount() > 1 && hardLineBreaksOnly();
        if (roundWidth && getLayout() != null && (getLineCount() == 1 || legacyParagraphs)
                && !getLayout().getLineContainsTab(0) && MeasureSpec.getMode(widthSpec) != MeasureSpec.EXACTLY) {
            int start = getLayout().getLineStart(0), end = getLayout().getLineEnd(0);
            if (end > start && getText().charAt(end - 1) == '\n') end--;
            // Layout/measureText ceil each run; round the fractional glyph advance only once.
            boolean styled = getText() instanceof android.text.Spanned && ((android.text.Spanned) getText())
                    .getSpans(start, end, android.text.style.MetricAffectingSpan.class).length > 0;
            // Legacy TextView can overestimate intrinsic multiline width inside a bubble.
            // Never shrink a soft-wrapped paragraph or discard native tab-stop geometry.
            float advance = legacyParagraphs ? android.text.Layout.getDesiredWidth(getText(), getPaint())
                    : styled ? android.text.Layout.getDesiredWidth(getText(), start, end, getPaint())
                    : getPaint().getRunAdvance(getText(), start, end, start, end, getLayout().getParagraphDirection(0) < 0, end);
            // A wrapping paragraph must retain its fractional advance or the last glyph reflows after layout.
            int width = Math.max(Math.max(getSuggestedMinimumWidth(), getMinWidth()), (ceilWidth ? (int) Math.ceil(advance) : Math.round(advance)) + getCompoundPaddingLeft() + getCompoundPaddingRight());
            if (getMaxWidth() >= 0) width = Math.min(width, getMaxWidth());
            setMeasuredDimension(resolveSize(width, widthSpec), getMeasuredHeight());
        }
    }

    private boolean hardLineBreaksOnly() {
        android.text.Layout layout = getLayout();
        if (layout == null) return false;
        for (int line = 0; line < layout.getLineCount(); line++) {
            if (layout.getLineContainsTab(line)) return false;
            int end = layout.getLineEnd(line);
            if (line + 1 < layout.getLineCount() && (end == 0 || getText().charAt(end - 1) != '\n')) return false;
        }
        return true;
    }
}
